package service

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageJobStatusTransitions(t *testing.T) {
	require.True(t, ImageJobStatusCompleted.Terminal())
	require.False(t, ImageJobStatusRunning.Terminal())
	require.NoError(t, ValidateImageJobTransition(ImageJobStatusQueued, ImageJobStatusRunning))
	require.NoError(t, ValidateImageJobTransition(ImageJobStatusRunning, ImageJobStatusPartial))
	require.Error(t, ValidateImageJobTransition(ImageJobStatusCompleted, ImageJobStatusRunning))

	statuses := []ImageJobStatus{
		ImageJobStatusQueued,
		ImageJobStatusRunning,
		ImageJobStatusCompleted,
		ImageJobStatusPartial,
		ImageJobStatusFailed,
		ImageJobStatusIndeterminate,
		ImageJobStatusCanceled,
		ImageJobStatusExpired,
	}
	allowed := map[[2]ImageJobStatus]struct{}{
		{ImageJobStatusQueued, ImageJobStatusRunning}:        {},
		{ImageJobStatusQueued, ImageJobStatusCanceled}:       {},
		{ImageJobStatusQueued, ImageJobStatusFailed}:         {},
		{ImageJobStatusRunning, ImageJobStatusQueued}:        {},
		{ImageJobStatusRunning, ImageJobStatusCompleted}:     {},
		{ImageJobStatusRunning, ImageJobStatusPartial}:       {},
		{ImageJobStatusRunning, ImageJobStatusFailed}:        {},
		{ImageJobStatusRunning, ImageJobStatusIndeterminate}: {},
		{ImageJobStatusRunning, ImageJobStatusCanceled}:      {},
		{ImageJobStatusCompleted, ImageJobStatusExpired}:     {},
		{ImageJobStatusPartial, ImageJobStatusExpired}:       {},
		{ImageJobStatusFailed, ImageJobStatusExpired}:        {},
		{ImageJobStatusIndeterminate, ImageJobStatusExpired}: {},
		{ImageJobStatusCanceled, ImageJobStatusExpired}:      {},
	}

	for _, from := range statuses {
		for _, to := range statuses {
			from, to := from, to
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				_, wantAllowed := allowed[[2]ImageJobStatus{from, to}]
				err := ValidateImageJobTransition(from, to)
				if wantAllowed {
					require.NoError(t, err)
					return
				}
				require.ErrorIs(t, err, ErrImageJobInvalidTransition)
			})
		}
	}

	for _, tc := range []struct {
		name string
		from ImageJobStatus
		to   ImageJobStatus
	}{
		{name: "unknown source", from: ImageJobStatus("unknown"), to: ImageJobStatusRunning},
		{name: "unknown destination", from: ImageJobStatusQueued, to: ImageJobStatus("unknown")},
		{name: "zero source", from: "", to: ImageJobStatusRunning},
		{name: "zero destination", from: ImageJobStatusQueued, to: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, ValidateImageJobTransition(tc.from, tc.to), ErrImageJobInvalidTransition)
		})
	}
}

func TestImageJobTerminalStatuses(t *testing.T) {
	tests := []struct {
		status   ImageJobStatus
		terminal bool
	}{
		{status: ImageJobStatusQueued, terminal: false},
		{status: ImageJobStatusRunning, terminal: false},
		{status: ImageJobStatusCompleted, terminal: true},
		{status: ImageJobStatusPartial, terminal: true},
		{status: ImageJobStatusFailed, terminal: true},
		{status: ImageJobStatusIndeterminate, terminal: true},
		{status: ImageJobStatusCanceled, terminal: true},
		{status: ImageJobStatusExpired, terminal: true},
		{status: ImageJobStatus("unknown"), terminal: false},
		{status: "", terminal: false},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			require.Equal(t, tc.terminal, tc.status.Terminal())
		})
	}
}

func TestImageJobRepositoryMethodSet(t *testing.T) {
	repositoryType := reflect.TypeOf((*ImageJobRepository)(nil)).Elem()
	methods := make([]string, 0, repositoryType.NumMethod())
	for i := 0; i < repositoryType.NumMethod(); i++ {
		methods = append(methods, repositoryType.Method(i).Name)
	}
	sort.Strings(methods)

	require.Equal(t, []string{
		"CancelAdmin",
		"CancelOwned",
		"ClaimNext",
		"CreateReserved",
		"GetAdmin",
		"GetOwned",
		"Heartbeat",
		"IsCancelRequested",
		"ListExpired",
		"MarkExpired",
		"MarkTerminal",
		"MarkUpstreamStarted",
		"RecoverStale",
		"UpsertResult",
	}, methods)
}

func TestImageJobStatusRequestJSONContainsOnlyObjectMetadata(t *testing.T) {
	compression := 90
	partialImages := 2
	request := ImageJobRequest{
		Endpoint:          "/v1/images/edits",
		Model:             "gpt-image-2",
		Prompt:            "change the sky",
		N:                 1,
		Size:              "1024x1024",
		ResponseFormat:    "url",
		Quality:           "high",
		Background:        "transparent",
		OutputFormat:      "png",
		OutputCompression: &compression,
		Moderation:        "low",
		InputFidelity:     "high",
		Style:             "vivid",
		PartialImages:     &partialImages,
		InputURLs:         []string{"https://example.test/input.png"},
		MaskURL:           "https://example.test/mask.png",
		Inputs: []ImageJobInputRef{{
			Kind:      "image",
			Index:     0,
			ObjectKey: "image-jobs/7/imgjob_test/inputs/0.png",
			MIMEType:  "image/png",
			ByteSize:  3,
			SHA256:    "digest",
			FieldName: "image[]",
		}},
		Mask: &ImageJobInputRef{
			Kind:      "mask",
			Index:     0,
			ObjectKey: "image-jobs/7/imgjob_test/inputs/mask.png",
			MIMEType:  "image/png",
			ByteSize:  4,
			SHA256:    "mask-digest",
			FieldName: "mask",
		},
		Scenes: []string{"sunrise", "night"},
	}

	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"endpoint":"/v1/images/edits",
		"model":"gpt-image-2",
		"prompt":"change the sky",
		"n":1,
		"size":"1024x1024",
		"response_format":"url",
		"quality":"high",
		"background":"transparent",
		"output_format":"png",
		"output_compression":90,
		"moderation":"low",
		"input_fidelity":"high",
		"style":"vivid",
		"partial_images":2,
		"input_urls":["https://example.test/input.png"],
		"mask_url":"https://example.test/mask.png",
		"inputs":[{
			"kind":"image",
			"index":0,
			"object_key":"image-jobs/7/imgjob_test/inputs/0.png",
			"mime_type":"image/png",
			"byte_size":3,
			"sha256":"digest",
			"field_name":"image[]"
		}],
		"mask":{
			"kind":"mask",
			"index":0,
			"object_key":"image-jobs/7/imgjob_test/inputs/mask.png",
			"mime_type":"image/png",
			"byte_size":4,
			"sha256":"mask-digest",
			"field_name":"mask"
		},
		"scenes":["sunrise","night"]
	}`, string(encoded))
}
