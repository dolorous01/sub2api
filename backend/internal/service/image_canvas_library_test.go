package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type imageCanvasLibraryRepositoryStub struct {
	ImageCanvasRepository
	asset       *ImageAsset
	assetErr    error
	assetUserID int64
	assetID     string
	created     *ImageCanvasLibraryItemWrite
}

func (stub *imageCanvasLibraryRepositoryStub) GetAsset(_ context.Context, userID int64, publicID string) (*ImageAsset, error) {
	stub.assetUserID = userID
	stub.assetID = publicID
	return stub.asset, stub.assetErr
}

func (stub *imageCanvasLibraryRepositoryStub) CreateLibraryItem(_ context.Context, input ImageCanvasLibraryItemWrite) (*ImageCanvasLibraryItem, bool, error) {
	stub.created = &input
	return &ImageCanvasLibraryItem{PublicID: "lib_1", ClientID: input.ClientID}, true, nil
}

func TestImageCanvasLibraryCreateNormalizesTextInput(t *testing.T) {
	repository := &imageCanvasLibraryRepositoryStub{}
	item, created, err := NewImageCanvasProjectService(repository, nil).CreateLibraryItem(context.Background(), 42, ImageCanvasLibraryItemWrite{
		ClientID: " local-1 ",
		Kind:     " TEXT ",
		Title:    " Prompt ",
		Content:  "  keep surrounding whitespace  ",
		Tags:     []string{" reusable ", "REUSABLE", "product", ""},
		Source:   " Canvas ",
		Note:     " Note ",
		Metadata: json.RawMessage(`{"source":"canvas"}`),
	})
	if err != nil {
		t.Fatalf("CreateLibraryItem() error = %v", err)
	}
	if !created || item.PublicID != "lib_1" || repository.created == nil {
		t.Fatalf("CreateLibraryItem() = %+v, %v", item, created)
	}
	input := repository.created
	if input.UserID != 42 || input.ClientID != "local-1" || input.Kind != "text" || input.Title != "Prompt" {
		t.Fatalf("normalized identity fields = %+v", input)
	}
	if input.Content != "  keep surrounding whitespace  " || input.Source != "Canvas" || input.Note != "Note" {
		t.Fatalf("normalized content fields = %+v", input)
	}
	if len(input.Tags) != 2 || input.Tags[0] != "reusable" || input.Tags[1] != "product" {
		t.Fatalf("normalized tags = %#v", input.Tags)
	}
}

func TestImageCanvasLibraryCreateValidatesOwnedMediaKind(t *testing.T) {
	repository := &imageCanvasLibraryRepositoryStub{asset: &ImageAsset{PublicID: "asset_1", MediaKind: "audio"}}
	_, _, err := NewImageCanvasProjectService(repository, nil).CreateLibraryItem(context.Background(), 7, ImageCanvasLibraryItemWrite{
		ClientID:      "local-1",
		Kind:          "image",
		AssetPublicID: "asset_1",
		Title:         "Reference",
		Metadata:      json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrImageCanvasLibraryItemInvalid) {
		t.Fatalf("CreateLibraryItem() error = %v, want ErrImageCanvasLibraryItemInvalid", err)
	}
	if repository.assetUserID != 7 || repository.assetID != "asset_1" || repository.created != nil {
		t.Fatalf("asset validation = user %d, id %q, created %+v", repository.assetUserID, repository.assetID, repository.created)
	}
}

func TestImageCanvasLibraryCreateAcceptsOwnedMedia(t *testing.T) {
	repository := &imageCanvasLibraryRepositoryStub{asset: &ImageAsset{PublicID: "asset_1", MediaKind: "image"}}
	_, _, err := NewImageCanvasProjectService(repository, nil).CreateLibraryItem(context.Background(), 7, ImageCanvasLibraryItemWrite{
		ClientID:      "local-1",
		Kind:          "image",
		AssetPublicID: "asset_1",
		Title:         "Reference",
	})
	if err != nil {
		t.Fatalf("CreateLibraryItem() error = %v", err)
	}
	if repository.created == nil || repository.created.AssetPublicID != "asset_1" || string(repository.created.Metadata) != "{}" {
		t.Fatalf("created input = %+v", repository.created)
	}
}

func TestImageCanvasLibraryUpdateRequiresVersion(t *testing.T) {
	repository := &imageCanvasLibraryRepositoryStub{}
	_, err := NewImageCanvasProjectService(repository, nil).UpdateLibraryItem(context.Background(), 7, "lib_1", ImageCanvasLibraryItemWrite{
		Kind: "text", Title: "Prompt", Content: "Content",
	})
	if !errors.Is(err, ErrImageCanvasLibraryItemInvalid) {
		t.Fatalf("UpdateLibraryItem() error = %v, want ErrImageCanvasLibraryItemInvalid", err)
	}
}
