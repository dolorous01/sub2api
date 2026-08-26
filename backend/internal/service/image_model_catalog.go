package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const imageCanvasCatalogPageSize = 1000

const (
	ImageCanvasAPIKeyUnavailableDisabled                = "key_disabled"
	ImageCanvasAPIKeyUnavailableExpired                 = "key_expired"
	ImageCanvasAPIKeyUnavailableQuotaExhausted          = "quota_exhausted"
	ImageCanvasAPIKeyUnavailableGroupMissing            = "group_missing"
	ImageCanvasAPIKeyUnavailableGroupDisabled           = "group_disabled"
	ImageCanvasAPIKeyUnavailableImageGenerationDisabled = "image_generation_disabled"
	ImageCanvasAPIKeyUnavailableNoImageModel            = "no_image_model"
)

// ImageModelCatalog resolves the server-authoritative image capability set.
// It intentionally exposes no upstream account or API key credentials.
type ImageModelCatalog interface {
	ListOwnedAPIKeys(ctx context.Context, userID int64) ([]ImageCanvasAPIKey, error)
	ForAPIKey(ctx context.Context, userID, apiKeyID int64) (map[string]ImageModelCapability, error)
	ListSchedulable(ctx context.Context) (map[string]ImageModelCapability, error)
}

type imageModelCatalog struct {
	apiKeys        APIKeyRepository
	groups         GroupRepository
	accounts       AccountRepository
	maxInputImages int
	maxOutputs     int
	runtime        *ImageJobRuntimeSettingsService
}

func NewImageModelCatalog(
	apiKeys APIKeyRepository,
	groups GroupRepository,
	accounts AccountRepository,
	cfg *config.Config,
) ImageModelCatalog {
	return newImageModelCatalog(apiKeys, groups, accounts, cfg)
}

func newImageModelCatalog(
	apiKeys APIKeyRepository,
	groups GroupRepository,
	accounts AccountRepository,
	cfg *config.Config,
) *imageModelCatalog {
	defaults := config.DefaultImageJobsConfig()
	maxInputImages := defaults.MaxInputImages
	maxOutputs := defaults.MaxOutputsPerJob
	if cfg != nil {
		if cfg.Gateway.ImageJobs.MaxInputImages > 0 {
			maxInputImages = cfg.Gateway.ImageJobs.MaxInputImages
		}
		if cfg.Gateway.ImageJobs.MaxOutputsPerJob > 0 {
			maxOutputs = cfg.Gateway.ImageJobs.MaxOutputsPerJob
		}
	}
	return &imageModelCatalog{
		apiKeys: apiKeys, groups: groups, accounts: accounts,
		maxInputImages: maxInputImages, maxOutputs: maxOutputs,
	}
}

func ProvideImageModelCatalog(
	apiKeys APIKeyRepository,
	groups GroupRepository,
	accounts AccountRepository,
	cfg *config.Config,
	runtime *ImageJobRuntimeSettingsService,
) ImageModelCatalog {
	catalog := newImageModelCatalog(apiKeys, groups, accounts, cfg)
	catalog.runtime = runtime
	return catalog
}

func (c *imageModelCatalog) ListOwnedAPIKeys(ctx context.Context, userID int64) ([]ImageCanvasAPIKey, error) {
	if c == nil || c.apiKeys == nil || c.groups == nil {
		return nil, fmt.Errorf("image model catalog dependencies are required")
	}
	keys, _, err := c.apiKeys.ListByUserID(ctx, userID, pagination.PaginationParams{
		Page: 1, PageSize: imageCanvasCatalogPageSize,
	}, APIKeyListFilters{})
	if err != nil {
		return nil, fmt.Errorf("list image canvas api keys: %w", err)
	}
	result := make([]ImageCanvasAPIKey, 0, len(keys))
	modelReasonByGroup := make(map[int64]string)
	checkedModelGroups := make(map[int64]struct{})
	for index := range keys {
		key := &keys[index]
		if key.UserID != userID {
			continue
		}
		item := ImageCanvasAPIKey{ID: key.ID, Name: key.Name}
		reason := imageCanvasAPIKeyUnavailableReason(key, userID)
		var group *Group
		if key.GroupID != nil && *key.GroupID > 0 {
			item.GroupID = *key.GroupID
		}
		if item.GroupID > 0 {
			group, err = c.groupForAPIKey(ctx, key)
			if err == nil {
				item.GroupID = group.ID
				item.GroupName = group.Name
			} else if reason == "" {
				reason = ImageCanvasAPIKeyUnavailableGroupMissing
			}
		}
		if reason == "" {
			reason = imageCanvasGroupUnavailableReason(group)
		}
		if reason == "" && c.accounts != nil {
			groupReason := modelReasonByGroup[group.ID]
			if _, ok := checkedModelGroups[group.ID]; !ok {
				accounts, listErr := c.accounts.ListSchedulableByGroupID(ctx, group.ID)
				if listErr != nil {
					return nil, fmt.Errorf("list schedulable image accounts for group %d: %w", group.ID, listErr)
				}
				if len(filterImageCanvasModelsForGroup(c.capabilitiesForAccounts(accounts), group)) == 0 {
					groupReason = ImageCanvasAPIKeyUnavailableNoImageModel
				}
				modelReasonByGroup[group.ID] = groupReason
				checkedModelGroups[group.ID] = struct{}{}
			}
			reason = groupReason
		}
		item.Available = reason == ""
		item.UnavailableReason = reason
		result = append(result, item)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Available != result[right].Available {
			return result[left].Available
		}
		if result[left].Name == result[right].Name {
			return result[left].ID < result[right].ID
		}
		return result[left].Name < result[right].Name
	})
	return result, nil
}

func (c *imageModelCatalog) ForAPIKey(ctx context.Context, userID, apiKeyID int64) (map[string]ImageModelCapability, error) {
	if c == nil || c.apiKeys == nil || c.groups == nil || c.accounts == nil {
		return nil, fmt.Errorf("image model catalog dependencies are required")
	}
	key, err := c.apiKeys.GetByID(ctx, apiKeyID)
	if err != nil || !imageCanvasAPIKeyAvailable(key, userID) {
		return nil, ErrImageCanvasAPIKeyNotFound
	}
	group, err := c.groupForAPIKey(ctx, key)
	if err != nil || !imageCanvasGroupAvailable(group) {
		return nil, ErrImageCanvasAPIKeyNotFound
	}
	accounts, err := c.accounts.ListSchedulableByGroupID(ctx, group.ID)
	if err != nil {
		return nil, fmt.Errorf("list schedulable image accounts for group %d: %w", group.ID, err)
	}
	models := c.capabilitiesForAccounts(accounts)
	return filterImageCanvasModelsForGroup(models, group), nil
}

func (c *imageModelCatalog) ListSchedulable(ctx context.Context) (map[string]ImageModelCapability, error) {
	if c == nil || c.accounts == nil {
		return nil, fmt.Errorf("image model catalog account source is required")
	}
	accounts, err := c.accounts.ListSchedulable(ctx)
	if err != nil {
		return nil, fmt.Errorf("list schedulable image accounts: %w", err)
	}
	return c.capabilitiesForAccounts(accounts), nil
}

func (c *imageModelCatalog) groupForAPIKey(ctx context.Context, key *APIKey) (*Group, error) {
	if key == nil || key.GroupID == nil || *key.GroupID <= 0 {
		return nil, ErrImageCanvasAPIKeyNotFound
	}
	if key.Group != nil && key.Group.ID == *key.GroupID {
		return key.Group, nil
	}
	group, err := c.groups.GetByID(ctx, *key.GroupID)
	if err != nil || group == nil {
		return nil, ErrImageCanvasAPIKeyNotFound
	}
	return group, nil
}

func (c *imageModelCatalog) capabilitiesForAccounts(accounts []Account) map[string]ImageModelCapability {
	maxInputImages, maxOutputs := c.maxInputImages, c.maxOutputs
	var runtimeSettings ImageJobRuntimeSettings
	if c.runtime != nil {
		runtimeSettings = c.runtime.Current()
		maxInputImages = runtimeSettings.MaxInputImages
		maxOutputs = runtimeSettings.MaxOutputsPerJob
	}
	result := make(map[string]ImageModelCapability)
	for index := range accounts {
		account := &accounts[index]
		if !account.IsSchedulable() {
			continue
		}
		for model, capability := range account.ImageCanvasModelCatalog(maxInputImages, maxOutputs) {
			result[model] = mergeImageModelCapability(result[model], capability)
		}
	}
	if c.runtime != nil {
		applyImageModelRuntimeOverrides(result, runtimeSettings)
	}
	return result
}

func applyImageModelRuntimeOverrides(capabilities map[string]ImageModelCapability, settings ImageJobRuntimeSettings) {
	for _, override := range settings.DefaultOverrides {
		capability, exists := capabilities[override.Model]
		if !exists {
			continue
		}
		capability.Defaults = cloneImageModelDefaults(override.Parameters)
		capabilities[override.Model] = capability
	}
	for _, override := range settings.CustomPresets {
		capability, exists := capabilities[override.Model]
		if !exists {
			continue
		}
		preset := ImageModelPreset{
			ID: override.ID, Label: override.Label,
			Parameters: cloneImageModelDefaults(override.Parameters), Experimental: override.Experimental,
		}
		replaced := false
		for index := range capability.Presets {
			if capability.Presets[index].ID == preset.ID {
				capability.Presets[index] = preset
				replaced = true
				break
			}
		}
		if !replaced {
			capability.Presets = append(capability.Presets, preset)
		}
		capabilities[override.Model] = capability
	}
}

func imageCanvasAPIKeyAvailable(key *APIKey, userID int64) bool {
	return imageCanvasAPIKeyUnavailableReason(key, userID) == ""
}

func imageCanvasGroupAvailable(group *Group) bool {
	return imageCanvasGroupUnavailableReason(group) == ""
}

func imageCanvasAPIKeyUnavailableReason(key *APIKey, userID int64) string {
	if key == nil || key.ID <= 0 || key.UserID != userID {
		return ImageCanvasAPIKeyUnavailableDisabled
	}
	if key.Status == StatusAPIKeyExpired || key.IsExpired() {
		return ImageCanvasAPIKeyUnavailableExpired
	}
	if key.Status == StatusAPIKeyQuotaExhausted || key.IsQuotaExhausted() {
		return ImageCanvasAPIKeyUnavailableQuotaExhausted
	}
	if key.Status != StatusAPIKeyActive {
		return ImageCanvasAPIKeyUnavailableDisabled
	}
	if key.GroupID == nil || *key.GroupID <= 0 {
		return ImageCanvasAPIKeyUnavailableGroupMissing
	}
	return ""
}

func imageCanvasGroupUnavailableReason(group *Group) string {
	if group == nil || group.ID <= 0 {
		return ImageCanvasAPIKeyUnavailableGroupMissing
	}
	if group.Status != StatusActive {
		return ImageCanvasAPIKeyUnavailableGroupDisabled
	}
	if !GroupAllowsImageGeneration(group) {
		return ImageCanvasAPIKeyUnavailableImageGenerationDisabled
	}
	return ""
}

func filterImageCanvasModelsForGroup(models map[string]ImageModelCapability, group *Group) map[string]ImageModelCapability {
	if len(models) == 0 || group == nil || !group.CustomModelsListEnabled() {
		return cloneImageModelCapabilities(models)
	}
	result := make(map[string]ImageModelCapability)
	for _, model := range group.ModelsListConfig.Models {
		model = strings.TrimSpace(model)
		if capability, ok := models[model]; ok {
			result[model] = capability
		}
	}
	return result
}

func cloneImageModelCapabilities(input map[string]ImageModelCapability) map[string]ImageModelCapability {
	result := make(map[string]ImageModelCapability, len(input))
	for model, capability := range input {
		result[model] = cloneImageModelCapability(capability)
	}
	return result
}

func cloneImageModelCapability(capability ImageModelCapability) ImageModelCapability {
	capability.Sizes = append([]string(nil), capability.Sizes...)
	capability.AspectRatios = append([]string(nil), capability.AspectRatios...)
	capability.Resolutions = append([]string(nil), capability.Resolutions...)
	capability.Qualities = append([]string(nil), capability.Qualities...)
	capability.OutputFormats = append([]string(nil), capability.OutputFormats...)
	capability.Backgrounds = append([]string(nil), capability.Backgrounds...)
	capability.ExperimentalSizes = append([]string(nil), capability.ExperimentalSizes...)
	capability.VideoSeconds = append([]int(nil), capability.VideoSeconds...)
	capability.AudioVoices = append([]string(nil), capability.AudioVoices...)
	capability.AudioFormats = append([]string(nil), capability.AudioFormats...)
	capability.Presets = append([]ImageModelPreset(nil), capability.Presets...)
	if capability.CustomSize != nil {
		constraints := *capability.CustomSize
		capability.CustomSize = &constraints
	}
	if capability.Defaults.OutputCompression != nil {
		compression := *capability.Defaults.OutputCompression
		capability.Defaults.OutputCompression = &compression
	}
	return capability
}

func mergeImageModelCapability(left, right ImageModelCapability) ImageModelCapability {
	if left.MediaKind != "" && right.MediaKind != "" && left.MediaKind != right.MediaKind {
		return cloneImageModelCapability(left)
	}
	mediaKind := firstNonEmptyString(left.MediaKind, right.MediaKind)
	provider := strings.TrimSpace(left.Provider)
	if provider == "" {
		provider = strings.TrimSpace(right.Provider)
	} else if right.Provider != "" && !strings.EqualFold(provider, right.Provider) {
		// A single public model name cannot safely switch parameter protocols.
		// Preserve the first schedulable provider instead of advertising a union
		// that no individual account actually supports.
		return cloneImageModelCapability(left)
	}
	dimensionMode := strings.TrimSpace(left.DimensionMode)
	if dimensionMode == "" {
		dimensionMode = strings.TrimSpace(right.DimensionMode)
	}
	merged := ImageModelCapability{
		MediaKind:         mediaKind,
		Provider:          provider,
		DimensionMode:     dimensionMode,
		Generation:        left.Generation || right.Generation,
		Edit:              left.Edit || right.Edit,
		MultiImage:        left.MultiImage || right.MultiImage,
		Mask:              left.Mask || right.Mask,
		MaxInputImages:    max(left.MaxInputImages, right.MaxInputImages),
		MaxOutputs:        max(left.MaxOutputs, right.MaxOutputs),
		Sizes:             append(append([]string(nil), left.Sizes...), right.Sizes...),
		AspectRatios:      append(append([]string(nil), left.AspectRatios...), right.AspectRatios...),
		Resolutions:       append(append([]string(nil), left.Resolutions...), right.Resolutions...),
		Qualities:         append(append([]string(nil), left.Qualities...), right.Qualities...),
		OutputFormats:     append(append([]string(nil), left.OutputFormats...), right.OutputFormats...),
		Backgrounds:       append(append([]string(nil), left.Backgrounds...), right.Backgrounds...),
		OutputCompression: left.OutputCompression || right.OutputCompression,
		PartialImages:     left.PartialImages || right.PartialImages,
		MaxPartialImages:  max(left.MaxPartialImages, right.MaxPartialImages),
		ExperimentalSizes: append(append([]string(nil), left.ExperimentalSizes...), right.ExperimentalSizes...),
		VideoSeconds:      append(append([]int(nil), left.VideoSeconds...), right.VideoSeconds...),
		AudioVoices:       append(append([]string(nil), left.AudioVoices...), right.AudioVoices...),
		AudioFormats:      append(append([]string(nil), left.AudioFormats...), right.AudioFormats...),
		AudioSpeedMin:     maxNonZeroFloat(left.AudioSpeedMin, right.AudioSpeedMin),
		AudioSpeedMax:     math.Max(left.AudioSpeedMax, right.AudioSpeedMax),
	}
	if left.CustomSize != nil {
		constraints := *left.CustomSize
		merged.CustomSize = &constraints
	} else if right.CustomSize != nil {
		constraints := *right.CustomSize
		merged.CustomSize = &constraints
	}
	if left.Defaults != (ImageModelDefaults{}) {
		merged.Defaults = left.Defaults
	} else {
		merged.Defaults = right.Defaults
	}
	if len(left.Presets) > 0 {
		merged.Presets = append([]ImageModelPreset(nil), left.Presets...)
	} else {
		merged.Presets = append([]ImageModelPreset(nil), right.Presets...)
	}
	merged.Sizes = normalizeImageCanvasStrings(merged.Sizes)
	merged.AspectRatios = normalizeImageCanvasStrings(merged.AspectRatios)
	merged.Resolutions = normalizeImageCanvasStrings(merged.Resolutions)
	merged.Qualities = normalizeImageCanvasStrings(merged.Qualities)
	merged.OutputFormats = normalizeImageCanvasStrings(merged.OutputFormats)
	merged.Backgrounds = normalizeImageCanvasStrings(merged.Backgrounds)
	merged.ExperimentalSizes = normalizeImageCanvasStrings(merged.ExperimentalSizes)
	merged.VideoSeconds = normalizeImageCanvasInts(merged.VideoSeconds)
	merged.AudioVoices = normalizeImageCanvasStrings(merged.AudioVoices)
	merged.AudioFormats = normalizeImageCanvasStrings(merged.AudioFormats)
	return merged
}

func normalizeImageCanvasInts(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}

func maxNonZeroFloat(left, right float64) float64 {
	if left == 0 {
		return right
	}
	if right == 0 || left < right {
		return left
	}
	return right
}
