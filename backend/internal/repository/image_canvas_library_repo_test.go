package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var imageCanvasLibraryItemColumns = []string{
	"id", "public_id", "client_id", "user_id", "kind", "asset_id", "asset_public_id",
	"title", "content", "tags", "source", "note", "metadata", "version", "created_at", "updated_at",
}

func TestImageCanvasRepositoryCreateLibraryItemReturnsIdempotentItem(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &imageCanvasRepository{db: db}
	now := time.Now().UTC()
	input := service.ImageCanvasLibraryItemWrite{
		PublicID: "lib_new", ClientID: "local-1", UserID: 42, Kind: "text",
		Title: "Reusable prompt", Content: "Describe the scene", Tags: []string{"prompt"},
		Source: "canvas", Note: "saved", Metadata: json.RawMessage(`{"origin":"canvas"}`),
	}

	mock.ExpectBegin()
	mock.ExpectQuery("WITH inserted AS").
		WithArgs(
			input.PublicID, input.ClientID, input.UserID, input.Kind, nil, input.Title,
			input.Content, `["prompt"]`, input.Source, input.Note, string(input.Metadata),
		).
		WillReturnRows(sqlmock.NewRows(imageCanvasLibraryItemColumns))
	mock.ExpectQuery("WHERE item.user_id = \\$1 AND item.client_id = \\$2").
		WithArgs(input.UserID, input.ClientID).
		WillReturnRows(imageCanvasLibraryItemRows(now, "lib_existing", input))
	mock.ExpectCommit()

	item, created, err := repository.CreateLibraryItem(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateLibraryItem() error = %v", err)
	}
	if created || item == nil || item.PublicID != "lib_existing" || item.ClientID != input.ClientID || item.Version != 1 {
		t.Fatalf("CreateLibraryItem() = item %+v, created %v", item, created)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageCanvasRepositoryUpdateLibraryItemRejectsStaleVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &imageCanvasRepository{db: db}
	input := service.ImageCanvasLibraryItemWrite{
		PublicID: "lib_1", UserID: 42, Version: 2, Kind: "text",
		Title: "Updated prompt", Content: "Updated content", Tags: []string{"updated"},
		Source: "canvas", Note: "new", Metadata: json.RawMessage(`{"revision":2}`),
	}

	mock.ExpectBegin()
	mock.ExpectQuery("WITH updated AS").
		WithArgs(
			input.PublicID, input.UserID, input.Version, input.Kind, nil, input.Title,
			input.Content, `["updated"]`, input.Source, input.Note, string(input.Metadata),
		).
		WillReturnRows(sqlmock.NewRows(imageCanvasLibraryItemColumns))
	mock.ExpectQuery("SELECT EXISTS\\(").
		WithArgs(input.PublicID, input.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	_, err = repository.UpdateLibraryItem(context.Background(), input)
	if !errors.Is(err, service.ErrImageCanvasLibraryVersionConflict) {
		t.Fatalf("UpdateLibraryItem() error = %v, want ErrImageCanvasLibraryVersionConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageCanvasRepositoryDeleteLibraryItemSoftDeletes(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &imageCanvasRepository{db: db}

	mock.ExpectExec("UPDATE image_canvas_library_items").
		WithArgs("lib_1", int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repository.DeleteLibraryItem(context.Background(), 42, " lib_1 "); err != nil {
		t.Fatalf("DeleteLibraryItem() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func imageCanvasLibraryItemRows(
	now time.Time,
	publicID string,
	input service.ImageCanvasLibraryItemWrite,
) *sqlmock.Rows {
	return sqlmock.NewRows(imageCanvasLibraryItemColumns).AddRow(
		int64(7), publicID, input.ClientID, input.UserID, input.Kind, nil, "",
		input.Title, input.Content, []byte(`["prompt"]`), input.Source, input.Note,
		[]byte(input.Metadata), int64(1), now, now,
	)
}
