package service

import (
	"bytes"
	"context"
	"fmt"
	"testing"
)

func TestNormalizeImageJobRequestStoresUploadsAndDigest(t *testing.T) {
	store := newMemoryImageJobObjectStore()
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesEditsEndpoint,
		Model:    "gpt-image-2",
		Prompt:   "change sky",
		N:        1,
		Uploads: []OpenAIImagesUpload{{
			FieldName:   "image[]",
			FileName:    "a.png",
			ContentType: "image/png",
			Data:        []byte("a"),
		}},
		MaskUpload: &OpenAIImagesUpload{
			FieldName:   "mask",
			FileName:    "mask.png",
			ContentType: "image/png",
			Data:        []byte("m"),
		},
	}

	req, inputs, digest, err := NormalizeImageJobRequest(context.Background(), store, "42/imgjob_x", parsed, nil, 4)
	if err != nil {
		t.Fatalf("NormalizeImageJobRequest() error = %v", err)
	}
	if len(inputs) != 2 {
		t.Fatalf("len(inputs) = %d, want 2", len(inputs))
	}
	if len(req.Inputs) != 1 {
		t.Fatalf("len(req.Inputs) = %d, want 1", len(req.Inputs))
	}
	wantInputKey := "image-jobs/42/imgjob_x/inputs/0-" + inputs[0].SHA256 + ".png"
	if inputs[0].ObjectKey != wantInputKey {
		t.Errorf("inputs[0].ObjectKey = %q, want %q", inputs[0].ObjectKey, wantInputKey)
	}
	if req.Mask == nil {
		t.Fatal("req.Mask = nil, want mask reference")
	}
	wantMaskKey := "image-jobs/42/imgjob_x/mask/" + inputs[1].SHA256 + ".png"
	if inputs[1].ObjectKey != wantMaskKey {
		t.Errorf("inputs[1].ObjectKey = %q, want %q", inputs[1].ObjectKey, wantMaskKey)
	}
	if len(digest) != 64 {
		t.Errorf("len(digest) = %d, want 64", len(digest))
	}
	if got := store.mustGet(t, wantInputKey); !bytes.Equal(got.Data, []byte("a")) || got.ContentType != "image/png" {
		t.Errorf("stored input = %#v, want image/png bytes a", got)
	}
	if got := store.mustGet(t, wantMaskKey); !bytes.Equal(got.Data, []byte("m")) || got.ContentType != "image/png" {
		t.Errorf("stored mask = %#v, want image/png bytes m", got)
	}
}

type memoryImageJobObjectStore struct {
	objects map[string]ImageJobObject
}

func newMemoryImageJobObjectStore() *memoryImageJobObjectStore {
	return &memoryImageJobObjectStore{objects: make(map[string]ImageJobObject)}
}

func (s *memoryImageJobObjectStore) Put(_ context.Context, key string, data []byte, contentType string) error {
	if _, exists := s.objects[key]; exists {
		return fmt.Errorf("object %q already exists", key)
	}
	s.objects[key] = ImageJobObject{Data: bytes.Clone(data), ContentType: contentType, Size: int64(len(data))}
	return nil
}

func (s *memoryImageJobObjectStore) Get(_ context.Context, key string) (*ImageJobObject, error) {
	object, exists := s.objects[key]
	if !exists {
		return nil, fmt.Errorf("object %q not found", key)
	}
	object.Data = bytes.Clone(object.Data)
	return &object, nil
}

func (s *memoryImageJobObjectStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func (s *memoryImageJobObjectStore) Health(context.Context) error { return nil }

func (s *memoryImageJobObjectStore) mustGet(t *testing.T, key string) ImageJobObject {
	t.Helper()
	object, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q) error = %v", key, err)
	}
	return *object
}
