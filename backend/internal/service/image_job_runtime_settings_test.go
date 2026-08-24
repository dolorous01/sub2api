package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func validImageJobRuntimeSettingsForTest() ImageJobRuntimeSettings {
	return ImageJobRuntimeSettings{
		WorkerConcurrency:    2,
		MaxActiveJobsPerUser: 8,
		TaskTimeoutSeconds:   1800,
		MaxOutputsPerJob:     4,
		MaxInputImages:       8,
		ResultTTLSeconds:     86400,
		DefaultOverrides:     []ImageModelDefaultOverride{},
		CustomPresets:        []ImageModelPresetOverride{},
	}
}

func TestNormalizeImageJobRuntimeSettingsAcceptsUnicodePresetLabels(t *testing.T) {
	settings := validImageJobRuntimeSettingsForTest()
	settings.CustomPresets = []ImageModelPresetOverride{{
		Model: " gpt-image-1 ",
		ID:    " chinese-label ",
		Label: "  " + strings.Repeat("\u56fe", 80) + "  ",
	}}

	normalized, err := normalizeImageJobRuntimeSettings(settings)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-1", normalized.CustomPresets[0].Model)
	require.Equal(t, "chinese-label", normalized.CustomPresets[0].ID)
	require.Equal(t, strings.Repeat("\u56fe", 80), normalized.CustomPresets[0].Label)

	settings.CustomPresets[0].Label = strings.Repeat("\u56fe", 81)
	_, err = normalizeImageJobRuntimeSettings(settings)
	require.ErrorContains(t, err, "between 1 and 80 characters")
}

func TestNormalizeImageJobRuntimeSettingsRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ImageJobRuntimeSettings)
		message string
	}{
		{
			name: "worker concurrency below minimum",
			mutate: func(settings *ImageJobRuntimeSettings) {
				settings.WorkerConcurrency = 0
			},
			message: "worker_concurrency",
		},
		{
			name: "duplicate default model after trimming",
			mutate: func(settings *ImageJobRuntimeSettings) {
				settings.DefaultOverrides = []ImageModelDefaultOverride{
					{Model: "gpt-image-1"},
					{Model: " gpt-image-1 "},
				}
			},
			message: "duplicate default override",
		},
		{
			name: "invalid preset id",
			mutate: func(settings *ImageJobRuntimeSettings) {
				settings.CustomPresets = []ImageModelPresetOverride{{
					Model: "gpt-image-1", ID: "not allowed", Label: "Preset",
				}}
			},
			message: "custom preset id",
		},
		{
			name: "compression above maximum",
			mutate: func(settings *ImageJobRuntimeSettings) {
				compression := 101
				settings.DefaultOverrides = []ImageModelDefaultOverride{{
					Model:      "gpt-image-1",
					Parameters: ImageModelDefaults{OutputCompression: &compression},
				}}
			},
			message: "output_compression",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := validImageJobRuntimeSettingsForTest()
			test.mutate(&settings)

			_, err := normalizeImageJobRuntimeSettings(settings)
			require.ErrorContains(t, err, test.message)
		})
	}
}
