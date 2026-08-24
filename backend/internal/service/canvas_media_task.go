package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrCanvasMediaTaskNotFound            = errors.New("canvas media task not found")
	ErrCanvasMediaTaskInvalid             = errors.New("canvas media task request is invalid")
	ErrCanvasMediaTaskConflict            = errors.New("canvas media task conflict")
	ErrCanvasMediaTaskIdempotencyConflict = errors.New("canvas media task idempotency conflict")
	ErrCanvasMediaCapabilityUnavailable   = errors.New("canvas media capability is unavailable")
	ErrCanvasMediaUpstreamUnavailable     = errors.New("canvas media upstream is unavailable")
)

type CanvasMediaKind string

const (
	CanvasMediaKindVideo CanvasMediaKind = "video"
	CanvasMediaKindAudio CanvasMediaKind = "audio"
)

type CanvasMediaTaskError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type CanvasMediaTask struct {
	ID                    int64
	PublicID              string
	Kind                  CanvasMediaKind
	Status                ImageJobStatus
	Phase                 string
	UserID                int64
	APIKeyID              int64
	ProjectID             int64
	ProjectPublicID       string
	ClientNodeID          string
	SelectedModel         string
	SuccessfulModel       string
	Prompt                string
	Request               json.RawMessage
	RequestHash           string
	IdempotencyKey        string
	UpstreamRequestID     string
	UpstreamAccountID     *int64
	ResultAssetID         *int64
	ResultAssetPublicID   string
	ResultMIMEType        string
	Error                 *CanvasMediaTaskError
	CancelRequested       bool
	AttemptCount          int
	NextAttemptAt         time.Time
	LeaseOwner            string
	LeaseExpiresAt        *time.Time
	BillingType           int8
	BillingSubscriptionID *int64
	BillingRecordedAt     *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
	CompletedAt           *time.Time
}

func (t CanvasMediaTask) Terminal() bool {
	return t.Status.Terminal()
}

type CanvasMediaTaskInsert struct {
	PublicID              string
	Kind                  CanvasMediaKind
	UserID                int64
	APIKeyID              int64
	ProjectID             int64
	ClientNodeID          string
	SelectedModel         string
	Prompt                string
	Request               json.RawMessage
	RequestHash           string
	IdempotencyKey        string
	BillingType           int8
	BillingSubscriptionID *int64
}

type CanvasMediaTaskRepository interface {
	Create(ctx context.Context, input CanvasMediaTaskInsert) (*CanvasMediaTask, bool, error)
	GetOwned(ctx context.Context, userID int64, publicID string) (*CanvasMediaTask, error)
	ListRecoverable(ctx context.Context, userID, projectID int64) ([]CanvasMediaTask, error)
	ClaimNextVideo(ctx context.Context, owner string, leaseUntil time.Time) (*CanvasMediaTask, error)
	ClaimNextAudio(ctx context.Context, owner string, leaseUntil time.Time) (*CanvasMediaTask, error)
	StartOwned(ctx context.Context, id, userID int64, owner string, leaseUntil time.Time) (*CanvasMediaTask, error)
	RenewLease(ctx context.Context, id int64, owner string, leaseUntil time.Time) error
	MarkSubmitted(ctx context.Context, id int64, owner, upstreamRequestID string, upstreamAccountID int64, nextAttemptAt time.Time) error
	ReschedulePoll(ctx context.Context, id int64, owner string, nextAttemptAt time.Time) error
	RetryQueued(ctx context.Context, id int64, owner string, nextAttemptAt time.Time, taskError *CanvasMediaTaskError) error
	MarkBillingRecorded(ctx context.Context, id int64) error
	Complete(ctx context.Context, id int64, owner string, assetID int64, successfulModel string) error
	Fail(ctx context.Context, id int64, owner string, status ImageJobStatus, taskError CanvasMediaTaskError) error
	CancelOwned(ctx context.Context, userID int64, publicID string) (*CanvasMediaTask, error)
}
