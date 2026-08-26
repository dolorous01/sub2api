package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var imageEditorDocumentColumns = []string{
	"id", "public_id", "project_id", "node_id", "base_asset_id", "current_asset_id",
	"document", "version", "created_at", "updated_at",
}

var ownedImageEditorDocumentColumns = append(
	append([]string(nil), imageEditorDocumentColumns...),
	"project_public_id", "user_id",
)

func TestImageEditorRepositoryGetOwnedMasksAnotherUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &imageEditorRepository{db: db}

	mock.ExpectQuery("FROM image_editor_documents d").
		WithArgs("ied_public_123", int64(99)).
		WillReturnRows(sqlmock.NewRows(ownedImageEditorDocumentColumns))

	_, err = repository.GetOwned(context.Background(), 99, " ied_public_123 ")
	if !errors.Is(err, service.ErrImageEditorDocumentNotFound) {
		t.Fatalf("GetOwned() error = %v, want ErrImageEditorDocumentNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageEditorRepositoryUpdateRejectsStaleVersionBeforeWriting(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &imageEditorRepository{db: db}
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE OF d").
		WithArgs("ied_public_123", int64(99)).
		WillReturnRows(imageEditorOwnedDocumentRows(now, 5, 10, 10))
	mock.ExpectRollback()

	_, err = repository.Update(context.Background(), service.ImageEditorDocumentUpdate{
		UserID: 99, PublicID: "ied_public_123", Version: 4,
		Document: json.RawMessage(`{"schema_version":1}`),
	})
	if !errors.Is(err, service.ErrImageEditorDocumentVersionConflict) {
		t.Fatalf("Update() error = %v, want ErrImageEditorDocumentVersionConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageEditorRepositoryCreateRejectsDifferentBaseForExistingNode(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &imageEditorRepository{db: db}
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("FROM image_canvas_projects").
		WithArgs("icp_public_123", int64(99)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery("FROM image_assets").
		WithArgs("asset_new_123", int64(99)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(11)))
	mock.ExpectQuery("INSERT INTO image_editor_documents").
		WithArgs(sqlmock.AnyArg(), int64(7), "node-a", int64(11), `{"schema_version":1}`).
		WillReturnRows(sqlmock.NewRows(imageEditorDocumentColumns))
	mock.ExpectQuery("FROM image_editor_documents").
		WithArgs(int64(7), "node-a").
		WillReturnRows(imageEditorDocumentRows(now, 1, 12, 12))
	mock.ExpectRollback()

	_, created, err := repository.CreateOrGet(context.Background(), service.ImageEditorDocumentCreate{
		UserID: 99, ProjectPublicID: "icp_public_123", NodeID: "node-a",
		BaseAssetPublicID: "asset_new_123", Document: json.RawMessage(`{"schema_version":1}`),
	})
	if created || !errors.Is(err, service.ErrImageEditorDocumentConflict) {
		t.Fatalf("CreateOrGet() = created %v, error %v; want conflict", created, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageEditorRepositoryUpdateRollsBackWhenRevisionAppendFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &imageEditorRepository{db: db}
	now := time.Now().UTC()
	raw := json.RawMessage(`{"schema_version":1}`)

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE OF d").
		WithArgs("ied_public_123", int64(99)).
		WillReturnRows(imageEditorOwnedDocumentRows(now, 1, 10, 10))
	mock.ExpectQuery("UPDATE image_editor_documents").
		WithArgs(int64(1), int64(1), string(raw), int64(10)).
		WillReturnRows(imageEditorDocumentRows(now, 2, 10, 10))
	mock.ExpectExec("DELETE FROM image_editor_asset_references").
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO image_editor_asset_references").
		WithArgs(int64(1), int64(10), "source", "base").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO image_editor_asset_references").
		WithArgs(int64(1), int64(10), "result", "current").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO image_editor_revisions").
		WithArgs(sqlmock.AnyArg(), int64(1), int64(2), int64(10), "crop", "{}").
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	_, err = repository.Update(context.Background(), service.ImageEditorDocumentUpdate{
		UserID: 99, PublicID: "ied_public_123", Version: 1, Document: raw,
		Operation: "crop", Parameters: json.RawMessage("{}"),
		AssetReferences: []service.ImageEditorAssetReference{},
	})
	if err == nil {
		t.Fatal("Update() succeeded after revision insert failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func imageEditorDocumentRows(
	now time.Time,
	version, baseAssetID, currentAssetID int64,
) *sqlmock.Rows {
	return sqlmock.NewRows(imageEditorDocumentColumns).AddRow(
		int64(1), "ied_public_123", int64(7), "node-a", baseAssetID, currentAssetID,
		[]byte(`{"schema_version":1}`), version, now, now,
	)
}

func imageEditorOwnedDocumentRows(
	now time.Time,
	version, baseAssetID, currentAssetID int64,
) *sqlmock.Rows {
	return sqlmock.NewRows(ownedImageEditorDocumentColumns).AddRow(
		int64(1), "ied_public_123", int64(7), "node-a", baseAssetID, currentAssetID,
		[]byte(`{"schema_version":1}`), version, now, now, "icp_public_123", int64(99),
	)
}
