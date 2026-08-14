package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/google/uuid"
)

type CreateImageJobInput struct {
	APIKey         *APIKey
	Subscription   *UserSubscription
	Parsed         *OpenAIImagesRequest
	Scenes         []string
	Mode           string
	IdempotencyKey string
	MappedModel    string
	ChannelMapping ChannelMappingResult
	ChannelUsageFields
}

type ImageJobResponse struct {
	ID             string                   `json:"id"`
	Object         string                   `json:"object"`
	Status         ImageJobStatus           `json:"status"`
	Operation      string                   `json:"operation"`
	Mode           string                   `json:"mode,omitempty"`
	Model          string                   `json:"model"`
	RequestedCount int                      `json:"requested_count"`
	CompletedCount int                      `json:"completed_count"`
	Data           []ImageJobResultResponse `json:"data"`
	Error          *ImageJobError           `json:"error,omitempty"`
	CreatedAt      int64                    `json:"created_at"`
	StartedAt      *int64                   `json:"started_at,omitempty"`
	ExpiresAt      int64                    `json:"expires_at"`
}

type ImageJobResultResponse struct {
	Index         int    `json:"index"`
	Status        string `json:"status"`
	URL           string `json:"url"`
	MIMEType      string `json:"mime_type"`
	Size          string `json:"size,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type ImageJobService struct {
	repo    ImageJobRepository
	store   ImageJobObjectStore
	billing *ImageJobBilling
	cfg     *config.Config
}

func NewImageJobService(
	repo ImageJobRepository,
	store ImageJobObjectStore,
	billing *ImageJobBilling,
	cfg *config.Config,
) *ImageJobService {
	return &ImageJobService{repo: repo, store: store, billing: billing, cfg: cfg}
}

func NewImageJobPublicID() string {
	return "imgjob_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func (s *ImageJobService) Create(ctx context.Context, input CreateImageJobInput) (*ImageJob, bool, error) {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.ImageJobs.Enabled {
		return nil, false, ErrImageJobDisabled
	}
	if s.repo == nil || s.store == nil || s.billing == nil {
		return nil, false, fmt.Errorf("%w: service dependencies are unavailable", ErrImageJobUnavailable)
	}
	if err := s.store.Health(ctx); err != nil {
		return nil, false, fmt.Errorf("%w: object storage health check: %v", ErrImageJobUnavailable, err)
	}
	if input.APIKey == nil || input.APIKey.ID <= 0 || input.APIKey.UserID <= 0 || input.APIKey.GroupID == nil || *input.APIKey.GroupID <= 0 || input.APIKey.Group == nil {
		return nil, false, fmt.Errorf("%w: API key with a group is required", ErrImageJobInvalidRequest)
	}
	if !GroupAllowsImageGeneration(input.APIKey.Group) {
		return nil, false, ErrImageJobPermissionDenied
	}
	if input.Parsed == nil {
		return nil, false, fmt.Errorf("%w: parsed image request is required", ErrImageJobInvalidRequest)
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "batch"
	}
	if mode != "batch" && mode != "sequence" {
		return nil, false, fmt.Errorf("%w: mode must be batch or sequence", ErrImageJobInvalidRequest)
	}
	idempotencyHash, err := imageJobIdempotencyHash(input.APIKey.ID, input.IdempotencyKey)
	if err != nil {
		return nil, false, err
	}

	publicID := NewImageJobPublicID()
	request, inputs, uploads, digest, err := prepareImageJobRequest(
		publicID, input.Parsed, input.Scenes, s.cfg.Gateway.ImageJobs.MaxInputImages,
	)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrImageJobInvalidRequest, err)
	}
	requestedCount := request.N
	if len(request.Scenes) > 0 {
		requestedCount = len(request.Scenes)
	}
	if requestedCount < 1 || requestedCount > s.cfg.Gateway.ImageJobs.MaxOutputsPerJob {
		return nil, false, fmt.Errorf("%w: output count must be between 1 and %d", ErrImageJobInvalidRequest, s.cfg.Gateway.ImageJobs.MaxOutputsPerJob)
	}
	reservation, err := s.billing.Estimate(ctx, input.APIKey, input.Subscription, request)
	if err != nil {
		return nil, false, err
	}
	if err := storePreparedImageJobUploads(ctx, s.store, uploads); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrImageJobInvalidRequest, err)
	}
	cleanupInputs := func(cause error) error {
		errs := []error{cause}
		for index := len(inputs) - 1; index >= 0; index-- {
			if deleteErr := s.store.Delete(context.WithoutCancel(ctx), inputs[index].ObjectKey); deleteErr != nil {
				errs = append(errs, fmt.Errorf("delete image job input %q: %w", inputs[index].ObjectKey, deleteErr))
			}
		}
		return errors.Join(errs...)
	}
	mappedModel := strings.TrimSpace(input.MappedModel)
	if mappedModel == "" {
		mappedModel = strings.TrimSpace(input.ChannelMapping.MappedModel)
	}
	if mappedModel == "" {
		mappedModel = request.Model
	}
	now := timezone.Now()
	create := &ImageJobCreate{
		PublicID: publicID, UserID: input.APIKey.UserID, APIKeyID: input.APIKey.ID, GroupID: *input.APIKey.GroupID,
		Endpoint: request.Endpoint, Operation: imageJobOperation(request.Endpoint), Mode: mode,
		RequestedModel: request.Model, MappedModel: mappedModel, RequestedCount: requestedCount,
		Request: request, RequestDigest: digest, IdempotencyKeyHash: idempotencyHash,
		ReservedUSD: reservation.AmountUSD, ReservationBillingType: reservation.BillingType,
		ReservationSubscriptionID: reservation.SubscriptionID,
		ExpiresAt:                 now.Add(time.Duration(s.cfg.Gateway.ImageJobs.ResultTTLSeconds) * time.Second), Inputs: inputs,
	}
	created, job, err := s.repo.CreateReserved(ctx, create)
	if err != nil {
		return nil, false, cleanupInputs(err)
	}
	if job == nil {
		return nil, false, cleanupInputs(fmt.Errorf("%w: repository returned no image job", ErrImageJobUnavailable))
	}
	if !created {
		if cleanupErr := cleanupInputs(nil); cleanupErr != nil {
			return nil, false, cleanupErr
		}
		return job, true, nil
	}
	return job, false, nil
}

func (s *ImageJobService) GetOwned(ctx context.Context, publicID string, apiKeyID int64) (*ImageJob, error) {
	if s == nil || s.repo == nil {
		return nil, ErrImageJobUnavailable
	}
	return s.repo.GetOwned(ctx, strings.TrimSpace(publicID), apiKeyID)
}

func (s *ImageJobService) GetAdmin(ctx context.Context, publicID string) (*ImageJob, error) {
	if s == nil || s.repo == nil {
		return nil, ErrImageJobUnavailable
	}
	return s.repo.GetAdmin(ctx, strings.TrimSpace(publicID))
}

func (s *ImageJobService) GetOwnedResult(ctx context.Context, publicID string, apiKeyID int64, index int) (*ImageJobObject, error) {
	job, err := s.GetOwned(ctx, publicID, apiKeyID)
	if err != nil {
		return nil, err
	}
	return s.getResultObject(ctx, job, index)
}

func (s *ImageJobService) GetAdminResult(ctx context.Context, publicID string, index int) (*ImageJobObject, error) {
	job, err := s.GetAdmin(ctx, publicID)
	if err != nil {
		return nil, err
	}
	return s.getResultObject(ctx, job, index)
}

func (s *ImageJobService) getResultObject(ctx context.Context, job *ImageJob, index int) (*ImageJobObject, error) {
	if s == nil || s.store == nil {
		return nil, ErrImageJobUnavailable
	}
	if job == nil || index < 0 {
		return nil, ErrImageJobNotFound
	}
	if job.Status == ImageJobStatusExpired || (!job.ExpiresAt.IsZero() && !timezone.Now().Before(job.ExpiresAt)) {
		return nil, ErrImageJobExpired
	}
	for _, result := range job.Results {
		if result.Index != index || strings.TrimSpace(result.ObjectKey) == "" {
			continue
		}
		object, err := s.store.Get(ctx, result.ObjectKey)
		if err != nil || object == nil {
			return nil, fmt.Errorf("%w: result %d", ErrImageJobNotFound, index)
		}
		if strings.TrimSpace(object.ContentType) == "" {
			object.ContentType = result.MIMEType
		}
		if object.Size <= 0 {
			object.Size = int64(len(object.Data))
		}
		return object, nil
	}
	return nil, fmt.Errorf("%w: result %d", ErrImageJobNotFound, index)
}

func (s *ImageJobService) CancelOwned(ctx context.Context, publicID string, apiKeyID int64) (*ImageJob, error) {
	if s == nil || s.repo == nil {
		return nil, ErrImageJobUnavailable
	}
	return s.repo.CancelOwned(ctx, strings.TrimSpace(publicID), apiKeyID, timezone.Now())
}

func (s *ImageJobService) CancelAdmin(ctx context.Context, publicID string) (*ImageJob, error) {
	if s == nil || s.repo == nil {
		return nil, ErrImageJobUnavailable
	}
	return s.repo.CancelAdmin(ctx, strings.TrimSpace(publicID), timezone.Now())
}

func (job *ImageJob) ToResponse() ImageJobResponse {
	if job == nil {
		return ImageJobResponse{}
	}
	response := ImageJobResponse{
		ID: job.PublicID, Object: "image.job", Status: job.Status, Operation: job.Operation, Mode: job.Mode,
		Model: job.RequestedModel, RequestedCount: job.RequestedCount, CompletedCount: job.CompletedCount,
		Error: job.Error, CreatedAt: job.CreatedAt.Unix(), ExpiresAt: job.ExpiresAt.Unix(),
		Data: make([]ImageJobResultResponse, 0, len(job.Results)),
	}
	if job.StartedAt != nil {
		startedAt := job.StartedAt.Unix()
		response.StartedAt = &startedAt
	}
	for _, result := range job.Results {
		response.Data = append(response.Data, ImageJobResultResponse{
			Index: result.Index, Status: result.Status,
			URL:      fmt.Sprintf("/v1/images/jobs/%s/results/%d", job.PublicID, result.Index),
			MIMEType: result.MIMEType, Size: result.SizeTier, RevisedPrompt: result.RevisedPrompt,
		})
	}
	return response
}

func imageJobIdempotencyHash(apiKeyID int64, rawKey string) (*string, error) {
	if rawKey == "" {
		return nil, nil
	}
	key := strings.TrimSpace(rawKey)
	if len(key) < 1 || len(key) > 255 {
		return nil, fmt.Errorf("%w: Idempotency-Key must be between 1 and 255 bytes", ErrImageJobInvalidRequest)
	}
	sum := sha256.Sum256([]byte(strconv.FormatInt(apiKeyID, 10) + "\x00" + key))
	encoded := hex.EncodeToString(sum[:])
	return &encoded, nil
}

func imageJobOperation(endpoint string) string {
	if normalizeImageGenerationEndpoint(endpoint) == openAIImagesEditsEndpoint {
		return "edit"
	}
	return "generation"
}
