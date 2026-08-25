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

func TestCanvasMediaTaskRepositoryRenewLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &canvasMediaTaskRepository{db: db}
	leaseUntil := time.Now().Add(time.Minute)

	mock.ExpectExec("UPDATE canvas_media_tasks SET lease_expires_at").
		WithArgs(int64(42), "worker-1", leaseUntil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repository.RenewLease(context.Background(), 42, "worker-1", leaseUntil); err != nil {
		t.Fatalf("RenewLease() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasMediaTaskRepositoryRenewLeaseRejectsLostLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &canvasMediaTaskRepository{db: db}
	leaseUntil := time.Now().Add(time.Minute)

	mock.ExpectExec("UPDATE canvas_media_tasks SET lease_expires_at").
		WithArgs(int64(42), "stale-worker", leaseUntil).
		WillReturnResult(sqlmock.NewResult(0, 0))
	err = repository.RenewLease(context.Background(), 42, "stale-worker", leaseUntil)
	if !errors.Is(err, service.ErrCanvasMediaTaskConflict) {
		t.Fatalf("RenewLease() error = %v, want ErrCanvasMediaTaskConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasMediaTaskRepositoryCreateReturnsIdempotentTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &canvasMediaTaskRepository{db: db}
	input := canvasMediaTaskInsertFixture("hash-a")
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO canvas_media_tasks").
		WithArgs(input.PublicID, input.Kind, input.UserID, input.APIKeyID, input.ProjectID,
			input.ClientNodeID, input.SelectedModel, input.Prompt, string(input.Request),
			input.RequestHash, input.IdempotencyKey, input.BillingType, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT id FROM canvas_media_tasks").
		WithArgs(input.UserID, input.IdempotencyKey).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))
	mock.ExpectQuery("WHERE t.id = \\$1").
		WithArgs(int64(42)).
		WillReturnRows(canvasMediaTaskRows(now, input.RequestHash, service.ImageJobStatusQueued, "", false, 0))
	mock.ExpectCommit()

	task, created, err := repository.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created || task == nil || task.ID != 42 || task.RequestHash != input.RequestHash {
		t.Fatalf("Create() = task %+v, created %v", task, created)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasMediaTaskRepositoryCreateRejectsIdempotencyConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &canvasMediaTaskRepository{db: db}
	input := canvasMediaTaskInsertFixture("hash-new")
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO canvas_media_tasks").
		WithArgs(input.PublicID, input.Kind, input.UserID, input.APIKeyID, input.ProjectID,
			input.ClientNodeID, input.SelectedModel, input.Prompt, string(input.Request),
			input.RequestHash, input.IdempotencyKey, input.BillingType, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT id FROM canvas_media_tasks").
		WithArgs(input.UserID, input.IdempotencyKey).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))
	mock.ExpectQuery("WHERE t.id = \\$1").
		WithArgs(int64(42)).
		WillReturnRows(canvasMediaTaskRows(now, "hash-existing", service.ImageJobStatusQueued, "", false, 0))
	mock.ExpectRollback()

	_, created, err := repository.Create(context.Background(), input)
	if created || !errors.Is(err, service.ErrCanvasMediaTaskIdempotencyConflict) {
		t.Fatalf("Create() = created %v, error %v, want ErrCanvasMediaTaskIdempotencyConflict", created, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasMediaTaskRepositoryGetOwnedDoesNotReturnAnotherUsersTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &canvasMediaTaskRepository{db: db}

	mock.ExpectQuery("WHERE t.user_id = \\$1 AND t.public_id = \\$2").
		WithArgs(int64(99), "media-1").
		WillReturnRows(sqlmock.NewRows(canvasMediaTaskColumns))

	_, err = repository.GetOwned(context.Background(), 99, " media-1 ")
	if !errors.Is(err, service.ErrCanvasMediaTaskNotFound) {
		t.Fatalf("GetOwned() error = %v, want ErrCanvasMediaTaskNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasMediaTaskRepositoryListsRecoverableProjectTasks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &canvasMediaTaskRepository{db: db}
	now := time.Now().UTC()

	mock.ExpectQuery("t.updated_at > p.updated_at").
		WithArgs(int64(99), int64(7)).
		WillReturnRows(canvasMediaTaskRows(now, "hash-a", service.ImageJobStatusRunning, "", false, 1))

	tasks, err := repository.ListRecoverable(context.Background(), 99, 7)
	if err != nil {
		t.Fatalf("ListRecoverable() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].PublicID != "media-1" || tasks[0].ProjectID != 7 {
		t.Fatalf("ListRecoverable() tasks = %+v", tasks)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasMediaTaskRepositoryCancelOwned(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &canvasMediaTaskRepository{db: db}
	now := time.Now().UTC()

	mock.ExpectExec("UPDATE canvas_media_tasks SET status = 'canceled'").
		WithArgs(int64(99), "media-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("WHERE t.user_id = \\$1 AND t.public_id = \\$2").
		WithArgs(int64(99), "media-1").
		WillReturnRows(canvasMediaTaskRows(now, "hash-a", service.ImageJobStatusCanceled, "", true, 1))

	task, err := repository.CancelOwned(context.Background(), 99, " media-1 ")
	if err != nil {
		t.Fatalf("CancelOwned() error = %v", err)
	}
	if task == nil || task.Status != service.ImageJobStatusCanceled || !task.CancelRequested {
		t.Fatalf("CancelOwned() task = %+v", task)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasMediaTaskRepositoryClaimsExpiredVideoLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &canvasMediaTaskRepository{db: db}
	now := time.Now().UTC()
	leaseUntil := now.Add(time.Minute)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM canvas_media_tasks").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))
	mock.ExpectExec("UPDATE canvas_media_tasks").
		WithArgs(int64(42), "worker-1", leaseUntil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("WHERE t.id = \\$1").
		WithArgs(int64(42)).
		WillReturnRows(canvasMediaTaskRows(now, "hash-a", service.ImageJobStatusRunning, "worker-1", false, 2))
	mock.ExpectCommit()

	task, err := repository.ClaimNextVideo(context.Background(), "worker-1", leaseUntil)
	if err != nil {
		t.Fatalf("ClaimNextVideo() error = %v", err)
	}
	if task == nil || task.ID != 42 || task.Status != service.ImageJobStatusRunning || task.LeaseOwner != "worker-1" {
		t.Fatalf("ClaimNextVideo() task = %+v", task)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasMediaTaskRepositoryClaimsQueuedAudio(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository := &canvasMediaTaskRepository{db: db}

	mock.ExpectBegin()
	mock.ExpectQuery("kind = 'audio'").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	task, err := repository.ClaimNextAudio(context.Background(), "worker-1", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimNextAudio() error = %v", err)
	}
	if task != nil {
		t.Fatalf("ClaimNextAudio() task = %+v, want nil", task)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func canvasMediaTaskInsertFixture(requestHash string) service.CanvasMediaTaskInsert {
	return service.CanvasMediaTaskInsert{
		PublicID: "media-1", Kind: service.CanvasMediaKindVideo,
		UserID: 99, APIKeyID: 12, ProjectID: 7, ClientNodeID: "node-1",
		SelectedModel: "grok-imagine-video", Prompt: "animate this",
		Request: json.RawMessage(`{"seconds":5}`), RequestHash: requestHash,
		IdempotencyKey: "idem-1", BillingType: 1,
	}
}

var canvasMediaTaskColumns = []string{
	"id", "public_id", "kind", "status", "phase", "user_id", "api_key_id",
	"project_id", "project_public_id", "client_node_id", "selected_model",
	"successful_model", "prompt", "request", "request_hash", "idempotency_key",
	"upstream_request_id", "upstream_account_id", "result_asset_id",
	"result_asset_public_id", "result_mime_type", "error", "cancel_requested",
	"attempt_count", "next_attempt_at", "lease_owner", "lease_expires_at",
	"billing_type", "billing_subscription_id", "billing_recorded_at", "created_at",
	"updated_at", "completed_at",
}

func canvasMediaTaskRows(
	now time.Time,
	requestHash string,
	status service.ImageJobStatus,
	leaseOwner string,
	cancelRequested bool,
	attemptCount int,
) *sqlmock.Rows {
	phase := "preflight"
	switch status {
	case service.ImageJobStatusRunning:
		phase = "submitting"
	case service.ImageJobStatusCanceled:
		phase = "canceled"
	}
	var completedAt any
	if status.Terminal() {
		completedAt = now
	}
	return sqlmock.NewRows(canvasMediaTaskColumns).AddRow(
		int64(42), "media-1", string(service.CanvasMediaKindVideo), string(status), phase,
		int64(99), int64(12), int64(7), "project-1", "node-1", "grok-imagine-video",
		"", "animate this", []byte(`{"seconds":5}`), requestHash, "idem-1", "",
		nil, nil, "", "", nil, cancelRequested, attemptCount, now, leaseOwner, nil,
		int64(1), nil, nil, now, now, completedAt,
	)
}
