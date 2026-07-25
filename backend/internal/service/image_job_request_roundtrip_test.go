package service

import (
	"bytes"
	"context"
	"mime"
	"strings"
	"testing"
)

func TestBuildOpenAIImagesRequestRoundTripsMultipartInputs(t *testing.T) {
	store := newMemoryImageJobObjectStore()
	compression := 80
	partialImages := 1
	parsed := &OpenAIImagesRequest{
		Endpoint:          openAIImagesEditsEndpoint,
		Model:             "gpt-image-2",
		Prompt:            "keep the subject and change the sky",
		N:                 4,
		Size:              "1024x1024",
		ResponseFormat:    "b64_json",
		Quality:           "high",
		Background:        "transparent",
		OutputFormat:      "png",
		OutputCompression: &compression,
		Moderation:        "low",
		InputFidelity:     "high",
		Style:             "vivid",
		PartialImages:     &partialImages,
		Uploads: []OpenAIImagesUpload{
			{FieldName: "image[]", FileName: "a.png", ContentType: "image/png", Data: []byte("first")},
			{FieldName: "image[]", FileName: "b.webp", ContentType: "image/webp", Data: []byte("second")},
		},
		MaskUpload: &OpenAIImagesUpload{FieldName: "mask", FileName: "mask.png", ContentType: "image/png", Data: []byte("mask")},
	}
	normalized, _, _, err := NormalizeImageJobRequest(context.Background(), store, "42/imgjob_roundtrip", parsed, nil, 4)
	if err != nil {
		t.Fatalf("NormalizeImageJobRequest() error = %v", err)
	}

	body, contentType, rebuilt, err := BuildOpenAIImagesRequest(context.Background(), store, normalized)
	if err != nil {
		t.Fatalf("BuildOpenAIImagesRequest() error = %v", err)
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("ParseMediaType() error = %v", err)
	}
	if mediaType != "multipart/form-data" {
		t.Errorf("content type = %q, want multipart/form-data", contentType)
	}
	if len(body) == 0 {
		t.Fatal("rebuilt body is empty")
	}
	if rebuilt.Endpoint != openAIImagesEditsEndpoint || !rebuilt.Multipart || !rebuilt.Stream {
		t.Errorf("rebuilt endpoint/multipart/stream = %q/%t/%t", rebuilt.Endpoint, rebuilt.Multipart, rebuilt.Stream)
	}
	if rebuilt.N != 4 || rebuilt.InputFidelity != "high" {
		t.Errorf("rebuilt n/input_fidelity = %d/%q, want 4/high", rebuilt.N, rebuilt.InputFidelity)
	}
	if len(rebuilt.Uploads) != 2 {
		t.Fatalf("len(rebuilt.Uploads) = %d, want 2", len(rebuilt.Uploads))
	}
	if rebuilt.Uploads[0].FieldName != "image[]" || rebuilt.Uploads[1].FieldName != "image[]" {
		t.Errorf("rebuilt field names = %q/%q, want image[]/image[]", rebuilt.Uploads[0].FieldName, rebuilt.Uploads[1].FieldName)
	}
	if !bytes.Equal(rebuilt.Uploads[0].Data, []byte("first")) || !bytes.Equal(rebuilt.Uploads[1].Data, []byte("second")) {
		t.Errorf("rebuilt upload bytes = %q/%q", rebuilt.Uploads[0].Data, rebuilt.Uploads[1].Data)
	}
	if rebuilt.MaskUpload == nil || !bytes.Equal(rebuilt.MaskUpload.Data, []byte("mask")) {
		t.Errorf("rebuilt mask = %#v, want mask bytes", rebuilt.MaskUpload)
	}
	if rebuilt.OutputCompression == nil || *rebuilt.OutputCompression != compression {
		t.Errorf("rebuilt output_compression = %v, want %d", rebuilt.OutputCompression, compression)
	}
	if rebuilt.PartialImages == nil || *rebuilt.PartialImages != partialImages {
		t.Errorf("rebuilt partial_images = %v, want %d", rebuilt.PartialImages, partialImages)
	}
	if rebuilt.Quality != "high" || rebuilt.Background != "transparent" || rebuilt.OutputFormat != "png" || rebuilt.Moderation != "low" || rebuilt.Style != "vivid" {
		t.Errorf("rebuilt advanced options were not preserved: %#v", rebuilt)
	}
	if rebuilt.RequiredCapability != OpenAIImagesCapabilityNative {
		t.Errorf("rebuilt capability = %q, want %q", rebuilt.RequiredCapability, OpenAIImagesCapabilityNative)
	}
}

func TestNormalizeImageJobRequestDigestIgnoresObjectSuffix(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesEditsEndpoint,
		Model:    "gpt-image-2",
		Prompt:   "same payload",
		N:        1,
		Uploads: []OpenAIImagesUpload{{
			FieldName: "image", FileName: "a.png", ContentType: "image/png", Data: []byte("same"),
		}},
	}

	_, _, first, err := NormalizeImageJobRequest(context.Background(), newMemoryImageJobObjectStore(), "42/first", parsed, []string{"scene"}, 4)
	if err != nil {
		t.Fatalf("first NormalizeImageJobRequest() error = %v", err)
	}
	_, _, second, err := NormalizeImageJobRequest(context.Background(), newMemoryImageJobObjectStore(), "42/second", parsed, []string{"scene"}, 4)
	if err != nil {
		t.Fatalf("second NormalizeImageJobRequest() error = %v", err)
	}
	if first != second || strings.TrimSpace(first) == "" {
		t.Errorf("digests = %q and %q, want same non-empty digest", first, second)
	}
}

func TestBuildOpenAIImagesRequestRoundTripsJSONURLs(t *testing.T) {
	store := newMemoryImageJobObjectStore()
	parsed := &OpenAIImagesRequest{
		Endpoint:       openAIImagesEditsEndpoint,
		Model:          "gpt-image-2",
		Prompt:         "combine references",
		N:              2,
		InputFidelity:  "high",
		InputImageURLs: []string{"https://example.test/a.png", "https://example.test/b.webp"},
		MaskImageURL:   "https://example.test/mask.png",
	}
	normalized, _, _, err := NormalizeImageJobRequest(context.Background(), store, "42/imgjob_urls", parsed, nil, 4)
	if err != nil {
		t.Fatalf("NormalizeImageJobRequest() error = %v", err)
	}

	_, contentType, rebuilt, err := BuildOpenAIImagesRequest(context.Background(), store, normalized)
	if err != nil {
		t.Fatalf("BuildOpenAIImagesRequest() error = %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("content type = %q, want application/json", contentType)
	}
	if !rebuilt.Stream || rebuilt.N != 2 || rebuilt.InputFidelity != "high" {
		t.Errorf("rebuilt stream/n/input_fidelity = %t/%d/%q", rebuilt.Stream, rebuilt.N, rebuilt.InputFidelity)
	}
	if len(rebuilt.InputImageURLs) != 2 || rebuilt.InputImageURLs[0] != parsed.InputImageURLs[0] || rebuilt.InputImageURLs[1] != parsed.InputImageURLs[1] {
		t.Errorf("rebuilt input URLs = %#v, want %#v", rebuilt.InputImageURLs, parsed.InputImageURLs)
	}
	if rebuilt.MaskImageURL != parsed.MaskImageURL || !rebuilt.HasMask {
		t.Errorf("rebuilt mask URL/flag = %q/%t, want %q/true", rebuilt.MaskImageURL, rebuilt.HasMask, parsed.MaskImageURL)
	}
}

func TestNormalizeImageJobRequestRejectsAbsoluteObjectSuffix(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesGenerationsEndpoint,
		Model:    "gpt-image-2",
		Prompt:   "test",
		N:        1,
	}
	if _, _, _, err := NormalizeImageJobRequest(context.Background(), newMemoryImageJobObjectStore(), "/42/imgjob_x", parsed, nil, 4); err == nil {
		t.Fatal("NormalizeImageJobRequest() error = nil, want invalid absolute suffix")
	}
}
