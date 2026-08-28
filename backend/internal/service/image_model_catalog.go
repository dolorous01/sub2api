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

var _ ImageModelCatalogAdmin = (*imageModelCatalog)(nil)

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

// ListAdmin builds the inventory used by the administrator console. It uses
// the same schedulable-account source as request routing, then adds coverage
// counts without ever returning credentials or prompts.
func (c *imageModelCatalog) ListAdmin(ctx context.Context) ([]ImageModelCatalogEntry, error) {
	if c == nil || c.accounts == nil {
		return nil, fmt.Errorf("image model catalog account source is required")
	}
	accounts, err := c.accounts.ListSchedulable(ctx)
	if err != nil {
		return nil, fmt.Errorf("list schedulable image accounts for admin catalog: %w", err)
	}

	maxInputImages, maxOutputs := c.maxInputImages, c.maxOutputs
	if c.runtime != nil {
		settings := c.runtime.Current()
		if settings.MaxInputImages > 0 {
			maxInputImages = settings.MaxInputImages
		}
		if settings.MaxOutputsPerJob > 0 {
			maxOutputs = settings.MaxOutputsPerJob
		}
	}

	type aggregate struct {
		capability ImageModelCapability
		accounts   map[int64]struct{}
		groups     map[int64]struct{}
		filtered   bool
		noGroup    bool
	}
	aggregates := make(map[string]*aggregate)
	groupCache := make(map[int64]*Group)
	for index := range accounts {
		for _, group := range accounts[index].Groups {
			if group != nil && group.ID > 0 {
				groupCache[group.ID] = group
			}
		}
	}

	groupForAccount := func(groupID int64) *Group {
		if groupID <= 0 || c.groups == nil {
			return nil
		}
		if group, ok := groupCache[groupID]; ok {
			return group
		}
		group, getErr := c.groups.GetByID(ctx, groupID)
		if getErr != nil || group == nil {
			// User admission requires a valid group, so a missing group must stay
			// fail-closed in the administrator coverage view too.
			groupCache[groupID] = nil
			return nil
		}
		groupCache[groupID] = group
		return group
	}

	for index := range accounts {
		account := &accounts[index]
		if !account.IsSchedulable() {
			continue
		}
		accountModels := account.ImageCanvasModelCatalog(maxInputImages, maxOutputs)
		if len(accountModels) == 0 {
			continue
		}
		groupIDs := append([]int64(nil), account.GroupIDs...)
		if len(groupIDs) == 0 && len(account.Groups) > 0 {
			for _, group := range account.Groups {
				if group != nil {
					groupIDs = append(groupIDs, group.ID)
				}
			}
		}
		for model, capability := range accountModels {
			item := aggregates[model]
			if item == nil {
				item = &aggregate{
					capability: capability,
					accounts:   make(map[int64]struct{}),
					groups:     make(map[int64]struct{}),
				}
				aggregates[model] = item
			} else {
				item.capability = mergeImageModelCapability(item.capability, capability)
			}

			if len(groupIDs) == 0 {
				item.noGroup = true
			}
			for _, groupID := range groupIDs {
				group := groupForAccount(groupID)
				if group == nil || !imageCanvasGroupAvailable(group) {
					item.noGroup = true
					continue
				}
				filtered := filterImageCanvasModelsForGroup(
					map[string]ImageModelCapability{model: capability}, group,
				)
				if _, ok := filtered[model]; !ok {
					item.filtered = true
					continue
				}
				accountID := account.ID
				if accountID <= 0 {
					// Test/dry-run repositories may omit IDs. Use a stable
					// per-slice sentinel so those rows still count once.
					accountID = int64(index + 1)
				}
				item.accounts[accountID] = struct{}{}
				if groupID > 0 {
					item.groups[groupID] = struct{}{}
				}
			}
		}
	}

	// Runtime overrides affect the capability contract shown to operators too.
	capabilities := make(map[string]ImageModelCapability, len(aggregates))
	for model, item := range aggregates {
		capabilities[model] = item.capability
	}
	if c.runtime != nil {
		applyImageModelRuntimeOverrides(capabilities, c.runtime.Current())
	}

	keyCountCache := make(map[int64]int)
	countKeys := func(groupID int64) (int, error) {
		if groupID <= 0 || c.apiKeys == nil {
			return 0, nil
		}
		if count, ok := keyCountCache[groupID]; ok {
			return count, nil
		}
		count := 0
		page := 1
		for {
			keys, pages, listErr := c.apiKeys.ListByGroupID(ctx, groupID, pagination.PaginationParams{
				Page: page, PageSize: imageCanvasCatalogPageSize, SortOrder: pagination.SortOrderAsc,
			})
			if listErr != nil {
				return 0, fmt.Errorf("list image canvas keys for group %d: %w", groupID, listErr)
			}
			for index := range keys {
				key := &keys[index]
				if imageCanvasAPIKeyUnavailableReason(key, key.UserID) == "" {
					count++
				}
			}
			if pages == nil || page >= pages.Pages || len(keys) == 0 {
				break
			}
			page++
		}
		keyCountCache[groupID] = count
		return count, nil
	}

	models := make([]string, 0, len(aggregates))
	for model := range aggregates {
		models = append(models, model)
	}
	sort.Strings(models)
	result := make([]ImageModelCatalogEntry, 0, len(models))
	for _, model := range models {
		item := aggregates[model]
		coverage := ImageModelCoverage{AccountCount: len(item.accounts), GroupCount: len(item.groups)}
		for groupID := range item.groups {
			keys, countErr := countKeys(groupID)
			if countErr != nil {
				return nil, countErr
			}
			coverage.APIKeyCount += keys
		}
		capability := capabilities[model]
		schedulable := coverage.AccountCount > 0 && coverage.GroupCount > 0
		reason := ""
		if !schedulable {
			switch {
			case item.filtered:
				reason = ImageModelSchedulabilityReasonGroupFiltered
			case item.noGroup:
				reason = ImageModelSchedulabilityReasonNoGroup
			default:
				reason = ImageModelSchedulabilityReasonNoAccount
			}
		}
		result = append(result, ImageModelCatalogEntry{
			Model: model, Provider: capability.Provider, MediaKind: capability.MediaKind,
			Capability: capability, Coverage: coverage, Schedulable: schedulable,
			SchedulabilityReason: reason,
		})
	}
	return result, nil
}

func (c *imageModelCatalog) CheckSchedulability(ctx context.Context, model string) (ImageModelCatalogEntry, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return ImageModelCatalogEntry{SchedulabilityReason: ImageModelSchedulabilityReasonUnknownModel}, nil
	}
	entries, err := c.ListAdmin(ctx)
	if err != nil {
		return ImageModelCatalogEntry{}, err
	}
	for _, entry := range entries {
		if entry.Model == model {
			return entry, nil
		}
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Model, model) {
			return entry, nil
		}
	}
	if capability, known := inferredImageCanvasCapability(model, c.maxInputImages, c.maxOutputs); known {
		return ImageModelCatalogEntry{
			Model: model, Provider: capability.Provider, MediaKind: capability.MediaKind,
			Capability: capability, Schedulable: false,
			SchedulabilityReason: ImageModelSchedulabilityReasonNoAccount,
		}, nil
	}
	return ImageModelCatalogEntry{
		Model: model, Schedulable: false,
		SchedulabilityReason: ImageModelSchedulabilityReasonUnknownModel,
	}, nil
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
