package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	SettingKeyImageJobRuntimeSettings       = "image_job_runtime_settings"
	ImageCanvasMaxObjectBytes         int64 = 64 << 20
)

var ErrImageJobRuntimeSettingsInvalid = errors.New("image job runtime settings are invalid")

// ImageJobRuntimeSettings contains the limits that can be changed without a
// process restart. Storage credentials and transport settings remain in the
// deployment configuration.
type ImageJobRuntimeSettings struct {
	WorkerConcurrency    int                         `json:"worker_concurrency"`
	MaxActiveJobsPerUser int                         `json:"max_active_jobs_per_user"`
	TaskTimeoutSeconds   int                         `json:"task_timeout_seconds"`
	MaxOutputsPerJob     int                         `json:"max_outputs_per_job"`
	MaxInputImages       int                         `json:"max_input_images"`
	ResultTTLSeconds     int                         `json:"result_ttl_seconds"`
	DefaultOverrides     []ImageModelDefaultOverride `json:"default_overrides"`
	CustomPresets        []ImageModelPresetOverride  `json:"custom_presets"`
}

type ImageModelDefaultOverride struct {
	Model      string             `json:"model"`
	Parameters ImageModelDefaults `json:"parameters"`
}

type ImageModelPresetOverride struct {
	Model        string             `json:"model"`
	ID           string             `json:"id"`
	Label        string             `json:"label"`
	Parameters   ImageModelDefaults `json:"parameters"`
	Experimental bool               `json:"experimental,omitempty"`
}

type ImageJobStorageInfo struct {
	Enabled                   bool   `json:"enabled"`
	Driver                    string `json:"driver"`
	Location                  string `json:"location"`
	Prefix                    string `json:"prefix"`
	MaxObjectBytes            int64  `json:"max_object_bytes"`
	GeneratedAssetsPermanent  bool   `json:"generated_assets_permanent"`
	RuntimeChangesNeedRestart bool   `json:"runtime_changes_need_restart"`
}

type ImageJobRuntimeSettingsService struct {
	repository SettingRepository
	cfg        *config.Config

	mu      sync.RWMutex
	current ImageJobRuntimeSettings
}

func NewImageJobRuntimeSettingsService(repository SettingRepository, cfg *config.Config) *ImageJobRuntimeSettingsService {
	service := &ImageJobRuntimeSettingsService{repository: repository, cfg: cfg}
	service.current = defaultImageJobRuntimeSettings(cfg)
	return service
}

func defaultImageJobRuntimeSettings(cfg *config.Config) ImageJobRuntimeSettings {
	settings := config.DefaultImageJobsConfig()
	if cfg != nil {
		settings = cfg.Gateway.ImageJobs
	}
	workerConcurrency := settings.WorkerConcurrency
	if workerConcurrency <= 0 {
		workerConcurrency = 1
	}
	return ImageJobRuntimeSettings{
		WorkerConcurrency:    workerConcurrency,
		MaxActiveJobsPerUser: 8,
		TaskTimeoutSeconds:   positiveOr(settings.TaskTimeoutSeconds, 1800),
		MaxOutputsPerJob:     positiveOr(settings.MaxOutputsPerJob, 4),
		MaxInputImages:       positiveOr(settings.MaxInputImages, 4),
		ResultTTLSeconds:     positiveOr(settings.ResultTTLSeconds, 86400),
		DefaultOverrides:     []ImageModelDefaultOverride{},
		CustomPresets:        []ImageModelPresetOverride{},
	}
}

func positiveOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func (s *ImageJobRuntimeSettingsService) Defaults() ImageJobRuntimeSettings {
	if s == nil {
		return defaultImageJobRuntimeSettings(nil)
	}
	return cloneImageJobRuntimeSettings(defaultImageJobRuntimeSettings(s.cfg))
}

func (s *ImageJobRuntimeSettingsService) Current() ImageJobRuntimeSettings {
	if s == nil {
		return defaultImageJobRuntimeSettings(nil)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneImageJobRuntimeSettings(s.current)
}

func (s *ImageJobRuntimeSettingsService) Get(ctx context.Context) (ImageJobRuntimeSettings, error) {
	if s == nil {
		return ImageJobRuntimeSettings{}, fmt.Errorf("image job runtime settings service is required")
	}
	if err := s.Refresh(ctx); err != nil {
		return ImageJobRuntimeSettings{}, err
	}
	return s.Current(), nil
}

func (s *ImageJobRuntimeSettingsService) Refresh(ctx context.Context) error {
	if s == nil || s.repository == nil {
		return nil
	}
	raw, err := s.repository.GetValue(ctx, SettingKeyImageJobRuntimeSettings)
	if errors.Is(err, ErrSettingNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load image job runtime settings: %w", err)
	}
	var settings ImageJobRuntimeSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return fmt.Errorf("decode image job runtime settings: %w", err)
	}
	normalized, err := normalizeImageJobRuntimeSettings(settings)
	if err != nil {
		return fmt.Errorf("validate stored image job runtime settings: %w", err)
	}
	s.storeCurrent(normalized)
	return nil
}

func (s *ImageJobRuntimeSettingsService) Update(ctx context.Context, settings ImageJobRuntimeSettings) (ImageJobRuntimeSettings, error) {
	if s == nil || s.repository == nil {
		return ImageJobRuntimeSettings{}, fmt.Errorf("image job runtime settings repository is required")
	}
	normalized, err := normalizeImageJobRuntimeSettings(settings)
	if err != nil {
		return ImageJobRuntimeSettings{}, fmt.Errorf("%w: %v", ErrImageJobRuntimeSettingsInvalid, err)
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return ImageJobRuntimeSettings{}, fmt.Errorf("encode image job runtime settings: %w", err)
	}
	if err := s.repository.Set(ctx, SettingKeyImageJobRuntimeSettings, string(raw)); err != nil {
		return ImageJobRuntimeSettings{}, fmt.Errorf("store image job runtime settings: %w", err)
	}
	s.storeCurrent(normalized)
	return cloneImageJobRuntimeSettings(normalized), nil
}

func (s *ImageJobRuntimeSettingsService) StorageInfo() ImageJobStorageInfo {
	info := ImageJobStorageInfo{
		MaxObjectBytes:            ImageCanvasMaxObjectBytes,
		GeneratedAssetsPermanent:  true,
		RuntimeChangesNeedRestart: false,
	}
	if s == nil || s.cfg == nil {
		return info
	}
	settings := s.cfg.Gateway.ImageJobs
	info.Enabled = settings.Enabled
	info.Driver = strings.ToLower(strings.TrimSpace(settings.Storage.Driver))
	info.Prefix = strings.TrimSpace(settings.Storage.Prefix)
	switch info.Driver {
	case "local":
		info.Location = strings.TrimSpace(settings.Storage.LocalDirectory)
	case "s3":
		bucket := strings.TrimSpace(settings.Storage.Bucket)
		region := strings.TrimSpace(settings.Storage.Region)
		if region != "" {
			info.Location = bucket + " (" + region + ")"
		} else {
			info.Location = bucket
		}
	}
	return info
}

func (s *ImageJobRuntimeSettingsService) storeCurrent(settings ImageJobRuntimeSettings) {
	s.mu.Lock()
	s.current = cloneImageJobRuntimeSettings(settings)
	s.mu.Unlock()
}

func normalizeImageJobRuntimeSettings(settings ImageJobRuntimeSettings) (ImageJobRuntimeSettings, error) {
	switch {
	case settings.WorkerConcurrency < 1 || settings.WorkerConcurrency > 32:
		return ImageJobRuntimeSettings{}, fmt.Errorf("worker_concurrency must be between 1 and 32")
	case settings.MaxActiveJobsPerUser < 1 || settings.MaxActiveJobsPerUser > 200:
		return ImageJobRuntimeSettings{}, fmt.Errorf("max_active_jobs_per_user must be between 1 and 200")
	case settings.TaskTimeoutSeconds < 60 || settings.TaskTimeoutSeconds > 7200:
		return ImageJobRuntimeSettings{}, fmt.Errorf("task_timeout_seconds must be between 60 and 7200")
	case settings.MaxOutputsPerJob < 1 || settings.MaxOutputsPerJob > 4:
		return ImageJobRuntimeSettings{}, fmt.Errorf("max_outputs_per_job must be between 1 and 4")
	case settings.MaxInputImages < 1 || settings.MaxInputImages > 16:
		return ImageJobRuntimeSettings{}, fmt.Errorf("max_input_images must be between 1 and 16")
	case settings.ResultTTLSeconds < 3600 || settings.ResultTTLSeconds > 31536000:
		return ImageJobRuntimeSettings{}, fmt.Errorf("result_ttl_seconds must be between 3600 and 31536000")
	case len(settings.DefaultOverrides) > 64:
		return ImageJobRuntimeSettings{}, fmt.Errorf("default_overrides must contain at most 64 entries")
	case len(settings.CustomPresets) > 128:
		return ImageJobRuntimeSettings{}, fmt.Errorf("custom_presets must contain at most 128 entries")
	}

	defaultModels := make(map[string]struct{}, len(settings.DefaultOverrides))
	defaults := make([]ImageModelDefaultOverride, 0, len(settings.DefaultOverrides))
	for _, override := range settings.DefaultOverrides {
		override.Model = strings.TrimSpace(override.Model)
		if override.Model == "" || len(override.Model) > 128 {
			return ImageJobRuntimeSettings{}, fmt.Errorf("default override model must be between 1 and 128 bytes")
		}
		if _, exists := defaultModels[override.Model]; exists {
			return ImageJobRuntimeSettings{}, fmt.Errorf("duplicate default override for model %q", override.Model)
		}
		if err := validateImageModelDefaultsShape(override.Parameters); err != nil {
			return ImageJobRuntimeSettings{}, fmt.Errorf("default override for %q: %w", override.Model, err)
		}
		defaultModels[override.Model] = struct{}{}
		defaults = append(defaults, override)
	}

	presetIDs := make(map[string]struct{}, len(settings.CustomPresets))
	presets := make([]ImageModelPresetOverride, 0, len(settings.CustomPresets))
	for _, preset := range settings.CustomPresets {
		preset.Model = strings.TrimSpace(preset.Model)
		preset.ID = strings.TrimSpace(preset.ID)
		preset.Label = strings.TrimSpace(preset.Label)
		if preset.Model == "" || len(preset.Model) > 128 {
			return ImageJobRuntimeSettings{}, fmt.Errorf("custom preset model must be between 1 and 128 bytes")
		}
		if !validImagePresetID(preset.ID) {
			return ImageJobRuntimeSettings{}, fmt.Errorf("custom preset id %q is invalid", preset.ID)
		}
		if preset.Label == "" || utf8.RuneCountInString(preset.Label) > 80 {
			return ImageJobRuntimeSettings{}, fmt.Errorf("custom preset label must be between 1 and 80 characters")
		}
		key := preset.Model + "\x00" + preset.ID
		if _, exists := presetIDs[key]; exists {
			return ImageJobRuntimeSettings{}, fmt.Errorf("duplicate custom preset %q for model %q", preset.ID, preset.Model)
		}
		if err := validateImageModelDefaultsShape(preset.Parameters); err != nil {
			return ImageJobRuntimeSettings{}, fmt.Errorf("custom preset %q for %q: %w", preset.ID, preset.Model, err)
		}
		presetIDs[key] = struct{}{}
		presets = append(presets, preset)
	}
	settings.DefaultOverrides = defaults
	settings.CustomPresets = presets
	return settings, nil
}

func validateImageModelDefaultsShape(parameters ImageModelDefaults) error {
	if parameters.OutputCompression != nil && (*parameters.OutputCompression < 0 || *parameters.OutputCompression > 100) {
		return fmt.Errorf("output_compression must be between 0 and 100")
	}
	return nil
}

func validImagePresetID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func cloneImageJobRuntimeSettings(settings ImageJobRuntimeSettings) ImageJobRuntimeSettings {
	settings.DefaultOverrides = append([]ImageModelDefaultOverride(nil), settings.DefaultOverrides...)
	for index := range settings.DefaultOverrides {
		settings.DefaultOverrides[index].Parameters = cloneImageModelDefaults(settings.DefaultOverrides[index].Parameters)
	}
	settings.CustomPresets = append([]ImageModelPresetOverride(nil), settings.CustomPresets...)
	for index := range settings.CustomPresets {
		settings.CustomPresets[index].Parameters = cloneImageModelDefaults(settings.CustomPresets[index].Parameters)
	}
	return settings
}

func cloneImageModelDefaults(defaults ImageModelDefaults) ImageModelDefaults {
	if defaults.OutputCompression != nil {
		value := *defaults.OutputCompression
		defaults.OutputCompression = &value
	}
	return defaults
}
