package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validImageEditorDocumentJSON() json.RawMessage {
	return json.RawMessage(`{
		"schema_version": 1,
		"viewport": {"zoom": 1, "x": 0, "y": 0},
		"canvas": {"width": 1024, "height": 768, "background": "transparent"}
	}`)
}

func TestValidateImageEditorDocumentAcceptsV1(t *testing.T) {
	document, err := ValidateImageEditorDocument(validImageEditorDocumentJSON())
	if err != nil {
		t.Fatalf("ValidateImageEditorDocument() error = %v", err)
	}
	if document.SchemaVersion != 1 || document.Canvas.Width != 1024 || document.Viewport.Zoom != 1 {
		t.Fatalf("ValidateImageEditorDocument() = %+v", document)
	}
}

func TestValidateImageEditorDocumentRejectsUnsafeOrUnknownState(t *testing.T) {
	tests := map[string]json.RawMessage{
		"external URL": json.RawMessage(`{
			"schema_version":1,
			"viewport":{"zoom":1,"x":0,"y":0},
			"canvas":{"width":512,"height":512,"background":"white"},
			"preview_url":"https://provider.example/output.png"
		}`),
		"sensitive field": json.RawMessage(`{
			"schema_version":1,
			"viewport":{"zoom":1,"x":0,"y":0},
			"canvas":{"width":512,"height":512,"background":"white"},
			"api_key":"secret"
		}`),
		"unknown field": json.RawMessage(`{
			"schema_version":1,
			"viewport":{"zoom":1,"x":0,"y":0},
			"canvas":{"width":512,"height":512,"background":"white"},
			"layers":[]
		}`),
		"oversized canvas": json.RawMessage(`{
			"schema_version":1,
			"viewport":{"zoom":1,"x":0,"y":0},
			"canvas":{"width":16384,"height":16384,"background":"white"}
		}`),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateImageEditorDocument(raw); !errors.Is(err, ErrImageEditorDocumentInvalid) {
				t.Fatalf("ValidateImageEditorDocument() error = %v, want ErrImageEditorDocumentInvalid", err)
			}
		})
	}
}

func TestImageEditorDocumentJSONRedactsInternalStorageFields(t *testing.T) {
	document := ImageEditorDocument{
		ID: 12, UserID: 34, ProjectID: 56, BaseAssetID: 78, CurrentAssetID: 90,
		PublicID: "ied_public_123", ProjectPublicID: "icp_public_123", NodeID: "node-a",
		BaseAsset: &ImageAsset{
			ID: 78, OwnerUserID: 34, PublicID: "asset_public_123",
			ObjectKey: "private/object/key.png", ThumbnailObjectKey: "private/thumbnail.jpg",
		},
		CurrentAsset: &ImageAsset{ID: 90, PublicID: "asset_result_123", ObjectKey: "private/result.png"},
		Document:     validImageEditorDocumentJSON(),
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private/object", "private/thumbnail", "private/result", `"user_id"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("editor document leaked %q: %s", forbidden, encoded)
		}
	}
	for _, expected := range []string{"ied_public_123", "icp_public_123", "asset_public_123"} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("editor document omitted public id %q: %s", expected, encoded)
		}
	}
}

type imageEditorRepositoryStub struct {
	document    *ImageEditorDocument
	updateInput ImageEditorDocumentUpdate
	updateErr   error
}

func (s *imageEditorRepositoryStub) CreateOrGet(context.Context, ImageEditorDocumentCreate) (*ImageEditorDocument, bool, error) {
	return s.document, true, nil
}

func (s *imageEditorRepositoryStub) GetOwned(context.Context, int64, string) (*ImageEditorDocument, error) {
	if s.document == nil {
		return nil, ErrImageEditorDocumentNotFound
	}
	return s.document, nil
}

func (s *imageEditorRepositoryStub) Update(_ context.Context, input ImageEditorDocumentUpdate) (*ImageEditorDocument, error) {
	s.updateInput = input
	return s.document, s.updateErr
}

func TestImageEditorServiceUpdateRequiresCompleteRevisionInput(t *testing.T) {
	editor := &ImageEditorService{repository: &imageEditorRepositoryStub{}}
	for name, input := range map[string]ImageEditorDocumentUpdate{
		"asset only": {
			UserID: 1, PublicID: "ied_public_123", Version: 1,
			Document: validImageEditorDocumentJSON(), CurrentAssetPublicID: "asset_public_123",
		},
		"operation only": {
			UserID: 1, PublicID: "ied_public_123", Version: 1,
			Document: validImageEditorDocumentJSON(), Operation: "crop",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := editor.Update(context.Background(), input); !errors.Is(err, ErrImageEditorDocumentInvalid) {
				t.Fatalf("Update() error = %v, want ErrImageEditorDocumentInvalid", err)
			}
		})
	}
}

type imageEditorAssetServiceStub struct {
	asset       *ImageAsset
	uploadInput ImageAssetStreamUpload
}

func (s *imageEditorAssetServiceStub) GetAsset(context.Context, int64, string) (*ImageAsset, error) {
	if s.asset == nil {
		return nil, ErrImageAssetNotFound
	}
	return s.asset, nil
}

func (s *imageEditorAssetServiceStub) UploadAssetStream(_ context.Context, input ImageAssetStreamUpload) (*ImageAsset, error) {
	s.uploadInput = input
	return &ImageAsset{PublicID: "asset_derived_123", SourceType: "derived", MediaKind: "image"}, nil
}

func TestImageEditorServiceUploadDerivedAssetKeepsOwnedLineage(t *testing.T) {
	parent := &ImageAsset{PublicID: "asset_parent_123", MediaKind: "image"}
	repository := &imageEditorRepositoryStub{document: &ImageEditorDocument{
		PublicID: "ied_public_123", ProjectPublicID: "icp_public_123", BaseAsset: parent,
	}}
	assets := &imageEditorAssetServiceStub{asset: parent}
	editor := &ImageEditorService{repository: repository, assets: assets}

	result, err := editor.UploadDerivedAssetStream(context.Background(), ImageEditorDerivedAssetUpload{
		UserID: 1, DocumentPublicID: "ied_public_123", ParentAssetPublicID: parent.PublicID,
		FileName: "crop.png", MIMEType: "image/png", ByteSize: 8, Reader: strings.NewReader("12345678"),
	})
	if err != nil {
		t.Fatalf("UploadDerivedAssetStream() error = %v", err)
	}
	if result.SourceType != "derived" || assets.uploadInput.SourceType != "derived" ||
		assets.uploadInput.ProjectPublicID != "icp_public_123" ||
		string(assets.uploadInput.ParentAssetIDs) != `["asset_parent_123"]` {
		t.Fatalf("unexpected derived upload: result=%+v input=%+v", result, assets.uploadInput)
	}
}
