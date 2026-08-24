package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type imageCanvasProjectRepositoryStub struct {
	ImageCanvasRepository
	projects []ImageCanvasProject
	jobs     map[int64][]ImageJob
}

func (stub *imageCanvasProjectRepositoryStub) ListProjects(context.Context, int64) ([]ImageCanvasProject, error) {
	return append([]ImageCanvasProject(nil), stub.projects...), nil
}

func (stub *imageCanvasProjectRepositoryStub) ListOpenJobs(_ context.Context, _ int64, projectID int64) ([]ImageJob, error) {
	return append([]ImageJob(nil), stub.jobs[projectID]...), nil
}

func TestImageCanvasProjectServiceListIncludesRecoverableJobs(t *testing.T) {
	repository := &imageCanvasProjectRepositoryStub{
		projects: []ImageCanvasProject{{ID: 7, PublicID: "project-1"}},
		jobs: map[int64][]ImageJob{
			7: {{PublicID: "job-1", Status: ImageJobStatusRunning}},
		},
	}
	projects, err := NewImageCanvasProjectService(repository, nil).List(context.Background(), 99)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(projects) != 1 || len(projects[0].OpenJobs) != 1 || projects[0].OpenJobs[0].PublicID != "job-1" {
		t.Fatalf("List() projects = %+v", projects)
	}
}

func TestValidateImageCanvasDocumentAcceptsUpstreamSchema(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version":2,
		"nodes":[
			{"id":"source","type":"image","metadata":{"storageKey":"asset:asset_12345678"}},
			{"id":"target","type":"video","metadata":{}}
		],
		"connections":[{"id":"edge-1","fromNodeId":"source","toNodeId":"target"}],
		"chat_sessions":[],"active_chat_id":null,"background_mode":"lines",
		"show_image_info":false,"viewport":{"x":0,"y":0,"k":1}
	}`)

	refs, err := ValidateImageCanvasDocument(raw)
	if err != nil {
		t.Fatalf("ValidateImageCanvasDocument() error = %v", err)
	}
	if len(refs) != 1 || refs[0].AssetPublicID != "asset_12345678" || refs[0].NodeID != "source" {
		t.Fatalf("unexpected asset refs: %#v", refs)
	}
}

func TestValidateImageCanvasDocumentAcceptsLegacySchema(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version":1,
		"nodes":[{"id":"a"},{"id":"b"}],
		"edges":[{"id":"edge-1","source":"a","target":"b"}],
		"viewport":{"x":0,"y":0,"k":1}
	}`)
	if _, err := ValidateImageCanvasDocument(raw); err != nil {
		t.Fatalf("ValidateImageCanvasDocument() error = %v", err)
	}
}

func TestValidateImageCanvasDocumentRejectsDanglingConnection(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version":2,
		"nodes":[{"id":"a"}],
		"connections":[{"id":"edge-1","fromNodeId":"a","toNodeId":"missing"}]
	}`)
	if _, err := ValidateImageCanvasDocument(raw); !errors.Is(err, ErrImageCanvasDocumentInvalid) {
		t.Fatalf("expected ErrImageCanvasDocumentInvalid, got %v", err)
	}
}

func TestValidateImageCanvasDocumentRejectsTransientMediaURL(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version":2,
		"nodes":[{"id":"a","metadata":{"content":"blob:https://example.test/id"}}],
		"connections":[]
	}`)
	if _, err := ValidateImageCanvasDocument(raw); !errors.Is(err, ErrImageCanvasDocumentInvalid) {
		t.Fatalf("expected ErrImageCanvasDocumentInvalid, got %v", err)
	}
}

func TestValidateImageCanvasDocumentRejectsExternalMediaURL(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version":2,
		"nodes":[{"id":"a","metadata":{"content":"https://provider.example/result.png"}}],
		"connections":[]
	}`)
	if _, err := ValidateImageCanvasDocument(raw); !errors.Is(err, ErrImageCanvasDocumentInvalid) {
		t.Fatalf("expected ErrImageCanvasDocumentInvalid, got %v", err)
	}
}

func TestValidateImageCanvasDocumentRejectsCredentialShapedFields(t *testing.T) {
	for _, field := range []string{
		"api_key", "providerApiKey", "apiKeyValue", "base_url", "customBaseURL",
		"accessToken", "refresh_token", "authToken", "authorizationHeader", "password",
		"clientSecret", "providerCredential", "credentials", "thumbnailObjectKey",
	} {
		t.Run(field, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{
				"schema_version": 2,
				"nodes": []any{map[string]any{
					"id": "node-a", "metadata": map[string]any{field: "sensitive-value"},
				}},
				"connections": []any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateImageCanvasDocument(raw); !errors.Is(err, ErrImageCanvasDocumentInvalid) {
				t.Fatalf("ValidateImageCanvasDocument() error = %v, want ErrImageCanvasDocumentInvalid", err)
			}
		})
	}
}

func TestValidateImageCanvasDocumentRejectsInvalidViewport(t *testing.T) {
	raw := json.RawMessage(`{
		"schema_version":2,
		"nodes":[],
		"connections":[],
		"viewport":{"x":0,"y":0,"k":0}
	}`)
	if _, err := ValidateImageCanvasDocument(raw); !errors.Is(err, ErrImageCanvasDocumentInvalid) {
		t.Fatalf("expected ErrImageCanvasDocumentInvalid, got %v", err)
	}
}

func TestValidateImageCanvasProjectPublicID(t *testing.T) {
	for _, id := range []string{"", "V1StGXR8_Z5jdHi6B-myT", "icp_0123456789abcdef"} {
		if _, err := validateImageCanvasProjectPublicID(id); err != nil {
			t.Fatalf("validateImageCanvasProjectPublicID(%q) error = %v", id, err)
		}
	}
	for _, id := range []string{"short", "contains/slash", "contains space"} {
		if _, err := validateImageCanvasProjectPublicID(id); !errors.Is(err, ErrImageCanvasDocumentInvalid) {
			t.Fatalf("validateImageCanvasProjectPublicID(%q) = %v", id, err)
		}
	}
}

func TestDetectImageCanvasAssetType(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		claimed  string
		kind     string
		mimeType string
	}{
		{name: "png", data: []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, claimed: "image/png", kind: "image", mimeType: "image/png"},
		{name: "mp4", data: []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, claimed: "video/mp4", kind: "video", mimeType: "video/mp4"},
		{name: "m4a", data: []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}, claimed: "audio/mp4", kind: "audio", mimeType: "audio/mp4"},
		{name: "mp3", data: []byte{'I', 'D', '3', 4}, claimed: "audio/mpeg", kind: "audio", mimeType: "audio/mpeg"},
		{name: "mp3 frame", data: []byte{0xff, 0xfb, 0x90, 0x64}, claimed: "audio/mpeg", kind: "audio", mimeType: "audio/mpeg"},
		{name: "aac frame", data: []byte{0xff, 0xf1, 0x50, 0x80}, claimed: "audio/aac", kind: "audio", mimeType: "audio/aac"},
		{name: "wav", data: []byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'A', 'V', 'E'}, claimed: "audio/wav", kind: "audio", mimeType: "audio/wav"},
		{name: "webm", data: []byte{0x1a, 0x45, 0xdf, 0xa3}, claimed: "video/webm", kind: "video", mimeType: "video/webm"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, mimeType, _, err := detectImageCanvasAssetType(test.data, test.claimed)
			if err != nil || kind != test.kind || mimeType != test.mimeType {
				t.Fatalf("detectImageCanvasAssetType() = %q, %q, %v", kind, mimeType, err)
			}
		})
	}
}

func TestDetectImageCanvasAssetTypeRejectsMIMEConflict(t *testing.T) {
	_, _, _, err := detectImageCanvasAssetType([]byte{'I', 'D', '3', 4}, "video/mp4")
	if !errors.Is(err, ErrImageAssetInvalid) {
		t.Fatalf("expected ErrImageAssetInvalid, got %v", err)
	}
}

func TestDetectImageCanvasAssetTypeRejectsMalformedMIME(t *testing.T) {
	_, _, _, err := detectImageCanvasAssetType([]byte{'I', 'D', '3', 4}, "audio/mpeg; invalid")
	if !errors.Is(err, ErrImageAssetInvalid) {
		t.Fatalf("expected ErrImageAssetInvalid, got %v", err)
	}
}
