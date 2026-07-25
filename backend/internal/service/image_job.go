package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrImageJobInvalidTransition       = errors.New("invalid image job status transition")
	ErrImageJobNotFound                = errors.New("image job not found")
	ErrImageJobConflict                = errors.New("image job conflict")
	ErrImageJobIdempotencyConflict     = errors.New("image job idempotency conflict")
	ErrImageJobReservationInsufficient = errors.New("image job reservation insufficient")
	ErrImageJobReservationUnavailable  = errors.New("image job reservation unavailable")
	ErrImageJobCancelConflict          = errors.New("image job cannot be canceled")
	ErrImageJobAttemptMismatch         = errors.New("image job attempt mismatch")
)

type ImageJobStatus string

const (
	ImageJobStatusQueued        ImageJobStatus = "queued"
	ImageJobStatusRunning       ImageJobStatus = "running"
	ImageJobStatusCompleted     ImageJobStatus = "completed"
	ImageJobStatusPartial       ImageJobStatus = "partial"
	ImageJobStatusFailed        ImageJobStatus = "failed"
	ImageJobStatusIndeterminate ImageJobStatus = "indeterminate"
	ImageJobStatusCanceled      ImageJobStatus = "canceled"
	ImageJobStatusExpired       ImageJobStatus = "expired"
)

func (s ImageJobStatus) Terminal() bool {
	switch s {
	case ImageJobStatusCompleted,
		ImageJobStatusPartial,
		ImageJobStatusFailed,
		ImageJobStatusIndeterminate,
		ImageJobStatusCanceled,
		ImageJobStatusExpired:
		return true
	default:
		return false
	}
}

func ValidateImageJobTransition(from, to ImageJobStatus) error {
	if !knownImageJobStatus(from) || !knownImageJobStatus(to) || from == to {
		return fmt.Errorf("%w: %q -> %q", ErrImageJobInvalidTransition, from, to)
	}

	allowed := false
	switch from {
	case ImageJobStatusQueued:
		allowed = to == ImageJobStatusRunning ||
			to == ImageJobStatusCanceled ||
			to == ImageJobStatusFailed
	case ImageJobStatusRunning:
		allowed = to == ImageJobStatusQueued ||
			to == ImageJobStatusCompleted ||
			to == ImageJobStatusPartial ||
			to == ImageJobStatusFailed ||
			to == ImageJobStatusIndeterminate ||
			to == ImageJobStatusCanceled
	case ImageJobStatusCompleted,
		ImageJobStatusPartial,
		ImageJobStatusFailed,
		ImageJobStatusIndeterminate,
		ImageJobStatusCanceled:
		allowed = to == ImageJobStatusExpired
	}
	if !allowed {
		return fmt.Errorf("%w: %q -> %q", ErrImageJobInvalidTransition, from, to)
	}
	return nil
}

func knownImageJobStatus(status ImageJobStatus) bool {
	switch status {
	case ImageJobStatusQueued,
		ImageJobStatusRunning,
		ImageJobStatusCompleted,
		ImageJobStatusPartial,
		ImageJobStatusFailed,
		ImageJobStatusIndeterminate,
		ImageJobStatusCanceled,
		ImageJobStatusExpired:
		return true
	default:
		return false
	}
}

type ImageJobError struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type ImageJobRequest struct {
	Endpoint          string             `json:"endpoint"`
	Model             string             `json:"model"`
	Prompt            string             `json:"prompt"`
	N                 int                `json:"n"`
	Size              string             `json:"size,omitempty"`
	ResponseFormat    string             `json:"response_format,omitempty"`
	Quality           string             `json:"quality,omitempty"`
	Background        string             `json:"background,omitempty"`
	OutputFormat      string             `json:"output_format,omitempty"`
	OutputCompression *int               `json:"output_compression,omitempty"`
	Moderation        string             `json:"moderation,omitempty"`
	InputFidelity     string             `json:"input_fidelity,omitempty"`
	Style             string             `json:"style,omitempty"`
	PartialImages     *int               `json:"partial_images,omitempty"`
	InputURLs         []string           `json:"input_urls,omitempty"`
	MaskURL           string             `json:"mask_url,omitempty"`
	Inputs            []ImageJobInputRef `json:"inputs,omitempty"`
	Mask              *ImageJobInputRef  `json:"mask,omitempty"`
	Scenes            []string           `json:"scenes,omitempty"`
}

type ImageJobInputRef struct {
	Kind      string `json:"kind"`
	Index     int    `json:"index"`
	ObjectKey string `json:"object_key"`
	MIMEType  string `json:"mime_type"`
	ByteSize  int64  `json:"byte_size"`
	SHA256    string `json:"sha256"`
	FieldName string `json:"field_name,omitempty"`
}

type ImageJobObject struct {
	Data        []byte
	ContentType string
	Size        int64
}

type ImageJobObjectStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) (*ImageJobObject, error)
	Delete(ctx context.Context, key string) error
	Health(ctx context.Context) error
}

type ImageJobInput struct {
	ID        int64
	JobID     int64
	Index     int
	Kind      string
	ObjectKey string
	MIMEType  string
	ByteSize  int64
	SHA256    string
	CreatedAt time.Time
}

type ImageJobResult struct {
	ID               int64
	JobID            int64
	Index            int
	Status           string
	ObjectKey        string
	MIMEType         string
	ByteSize         int64
	Width            int
	Height           int
	SizeTier         string
	RevisedPrompt    string
	UpstreamOutputID string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ImageJob struct {
	ID                        int64
	PublicID                  string
	UserID                    int64
	APIKeyID                  int64
	GroupID                   int64
	Endpoint                  string
	Operation                 string
	Mode                      string
	RequestedModel            string
	MappedModel               string
	Status                    ImageJobStatus
	RequestedCount            int
	CompletedCount            int
	Request                   ImageJobRequest
	RequestDigest             string
	IdempotencyKeyHash        *string
	ReservedUSD               float64
	ReservationBillingType    int8
	ReservationSubscriptionID *int64
	ReservationStatus         string
	SettlementStatus          string
	AttemptID                 *string
	WorkerID                  *string
	ExecutionPhase            string
	HeartbeatAt               *time.Time
	CancelRequestedAt         *time.Time
	CanceledAt                *time.Time
	StartedAt                 *time.Time
	FinishedAt                *time.Time
	ExpiresAt                 time.Time
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	Error                     *ImageJobError
	Inputs                    []ImageJobInput
	Results                   []ImageJobResult
}

type ImageJobCreate struct {
	PublicID                  string
	UserID                    int64
	APIKeyID                  int64
	GroupID                   int64
	Endpoint                  string
	Operation                 string
	Mode                      string
	RequestedModel            string
	MappedModel               string
	RequestedCount            int
	Request                   ImageJobRequest
	RequestDigest             string
	IdempotencyKeyHash        *string
	ReservedUSD               float64
	ReservationBillingType    int8
	ReservationSubscriptionID *int64
	ExpiresAt                 time.Time
	Inputs                    []ImageJobInput
}

type ImageJobClaim struct {
	Job       *ImageJob
	AttemptID string
	WorkerID  string
}

type ImageJobTerminalUpdate struct {
	Status            ImageJobStatus
	CompletedCount    int
	Error             *ImageJobError
	Usage             json.RawMessage
	FinishedAt        time.Time
	CanceledAt        *time.Time
	ReservationStatus string
	SettlementStatus  string
}

type ImageJobRepository interface {
	CreateReserved(ctx context.Context, create *ImageJobCreate) (created bool, replay *ImageJob, err error)
	GetOwned(ctx context.Context, publicID string, apiKeyID int64) (*ImageJob, error)
	GetAdmin(ctx context.Context, publicID string) (*ImageJob, error)
	ClaimNext(ctx context.Context, workerID, attemptID string) (*ImageJobClaim, error)
	MarkUpstreamStarted(ctx context.Context, jobID int64, attemptID, mappedModel string) error
	Heartbeat(ctx context.Context, jobID int64, attemptID string, at time.Time) error
	UpsertResult(ctx context.Context, jobID int64, attemptID string, result *ImageJobResult) (inserted bool, err error)
	MarkTerminal(ctx context.Context, jobID int64, attemptID string, update ImageJobTerminalUpdate) error
	CancelOwned(ctx context.Context, publicID string, apiKeyID int64, at time.Time) (*ImageJob, error)
	CancelAdmin(ctx context.Context, publicID string, at time.Time) (*ImageJob, error)
	IsCancelRequested(ctx context.Context, jobID int64, attemptID string) (bool, error)
	RecoverStale(ctx context.Context, cutoff time.Time) (requeued, indeterminate int64, err error)
	ListExpired(ctx context.Context, now time.Time, limit int) ([]*ImageJob, error)
	MarkExpired(ctx context.Context, jobID int64, fromStatus ImageJobStatus, expiredAt time.Time) error
}
