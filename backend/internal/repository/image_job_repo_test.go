package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestImageJobRepositoryClaimNextUsesSkipLocked(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := NewImageJobRepository(nil, db)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE SKIP LOCKED")).
		WithArgs("worker-1", "attempt-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectCommit()

	claim, err := repo.ClaimNext(context.Background(), "worker-1", "attempt-1")
	if err != nil {
		t.Fatalf("ClaimNext() error = %v", err)
	}
	if claim != nil {
		t.Fatalf("ClaimNext() = %#v, want nil", claim)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}

func TestImageJobRepositoryHeartbeatRequiresMatchingAttempt(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := NewImageJobRepository(nil, db)
	at := time.Unix(1_800_000_000, 0).UTC()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE image_jobs")).
		WithArgs(int64(42), "attempt-1", at).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.Heartbeat(context.Background(), 42, "attempt-1", at)
	if !errors.Is(err, service.ErrImageJobAttemptMismatch) {
		t.Fatalf("Heartbeat() error = %v, want ErrImageJobAttemptMismatch", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}

func TestImageJobRepositoryMarkTerminalRejectsNonTerminalStatus(t *testing.T) {
	db, _ := newSQLMock(t)
	repo := NewImageJobRepository(nil, db)
	err := repo.MarkTerminal(context.Background(), 42, "attempt-1", service.ImageJobTerminalUpdate{
		Status: service.ImageJobStatusRunning,
	})
	if !errors.Is(err, service.ErrImageJobInvalidTransition) {
		t.Fatalf("MarkTerminal() error = %v, want ErrImageJobInvalidTransition", err)
	}
}

func TestImageJobRepositoryMarkExpiredClearsResourceMetadataInTransaction(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := NewImageJobRepository(nil, db)
	expiredAt := time.Unix(1_800_000_100, 0).UTC()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE image_jobs")).
		WithArgs(int64(42), string(service.ImageJobStatusCompleted), expiredAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM image_job_results WHERE job_id = $1")).
		WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM image_job_inputs WHERE job_id = $1")).
		WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	if err := repo.MarkExpired(context.Background(), 42, service.ImageJobStatusCompleted, expiredAt); err != nil {
		t.Fatalf("MarkExpired() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}
