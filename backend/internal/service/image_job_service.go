package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	APIKey          *APIKey
	Subscription    *UserSubscription
	Parsed          *OpenAIImagesRequest
	Scenes          []string
	Mode            string
	IdempotencyKey  string
	MappedModel     string
	ChannelMapping  ChannelMappingResult
	CandidateModels []string
	Canvas          *ImageCanvasJobMetadata
	ChannelUsageFields
}

type ImageJobResponse struct {
	ID             string                   `json:"id"`
	Object         string                   `json:"object"`
	Status         ImageJobStatus           `json:"status"`
	StatusURL      string                   `json:"status_url"`
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
	repo          ImageJobRepository
	store         ImageJobObjectStore
	executor      ImageExecutor
	apiKeys       ImageJobAPIKeyProvider
	subscriptions ImageJobSubscriptionProvider
	billing       *ImageJobBilling
	cfg           *config.Config
	metrics       *ImageJobMetrics
	worker        *ImageJobWorker
	runtime       *ImageJobRuntimeSettingsService
}

func NewImageJobService(
	repo ImageJobRepository,
	store ImageJobObjectStore,
	dependencies ...any,
) *ImageJobService {
	service := &ImageJobService{repo: repo, store: store}
	for _, dependency := range dependencies {
		switch value := dependency.(type) {
		case ImageExecutor:
			service.executor = value
		case ImageJobAPIKeyProvider:
			service.apiKeys = value
		case ImageJobSubscriptionProvider:
			service.subscriptions = value
		case *ImageJobBilling:
			service.billing = value
		case *config.Config:
			service.cfg = value
		case *ImageJobMetrics:
			service.metrics = value
		case *ImageJobRuntimeSettingsService:
			service.runtime = value
		}
	}
	if service.metrics == nil {
		service.metrics = &ImageJobMetrics{}
	}
	if service.executor != nil && service.apiKeys != nil && service.billing != nil && service.cfg != nil {
		service.worker = NewImageJobWorker(repo, store, service.executor, service.apiKeys, service.subscriptions, service.billing, service.cfg, service.metrics, service.runtime)
	}
	return service
}

func (s *ImageJobService) Start() {
	if s != nil && s.worker != nil {
		s.worker.Start()
	}
}

func (s *ImageJobService) Stop() {
	if s != nil && s.worker != nil {
		s.worker.Stop()
	}
}

func (s *ImageJobService) MetricsSnapshot() ImageJobMetricsSnapshot {
	if s == nil || s.metrics == nil {
		return ImageJobMetricsSnapshot{}
	}
	return s.metrics.Snapshot()
}

func (s *ImageJobService) WorkerSnapshot() ImageJobWorkerSnapshot {
	if s == nil || s.worker == nil {
		return ImageJobWorkerSnapshot{}
	}
	return s.worker.Snapshot()
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
	runtimeSettings := s.runtimeSettings()
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "batch"
	}
	if mode != "batch" && mode != "sequence" {
		return nil, false, fmt.Errorf("%w: mode must be batch or sequence", ErrImageJobInvalidRequest)
	}
	scenes := append([]string(nil), input.Scenes...)
	if mode == "sequence" {
		if len(scenes) < 2 || len(scenes) > 4 {
			return nil, false, fmt.Errorf("%w: sequence requires between 2 and 4 scenes", ErrImageJobInvalidRequest)
		}
		for index := range scenes {
			scenes[index] = strings.TrimSpace(scenes[index])
			if scenes[index] == "" {
				return nil, false, fmt.Errorf("%w: scene %d must not be empty", ErrImageJobInvalidRequest, index)
			}
		}
	}
	idempotencyHash, err := imageJobIdempotencyHash(input.APIKey.ID, input.IdempotencyKey)
	if err != nil {
		return nil, false, err
	}

	publicID := NewImageJobPublicID()
	objectSuffix := fmt.Sprintf("%d/%s", input.APIKey.ID, publicID)
	request, inputs, uploads, digest, err := prepareImageJobRequest(
		objectSuffix, input.Parsed, scenes, runtimeSettings.MaxInputImages,
	)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrImageJobInvalidRequest, err)
	}
	if mode == "sequence" {
		referenceCount := len(request.Inputs) + len(request.InputURLs)
		requiredFrameInputs := referenceCount + 1
		if requiredFrameInputs < 2 {
			requiredFrameInputs = 2
		}
		if requiredFrameInputs > runtimeSettings.MaxInputImages {
			return nil, false, fmt.Errorf("%w: sequence frames require at least %d inputs, maximum is %d", ErrImageJobInvalidRequest, requiredFrameInputs, runtimeSettings.MaxInputImages)
		}
		if request.Mask != nil || strings.TrimSpace(request.MaskURL) != "" {
			return nil, false, fmt.Errorf("%w: sequence requests do not support masks", ErrImageJobInvalidRequest)
		}
	}
	requestedCount := request.N
	if len(request.Scenes) > 0 {
		requestedCount = len(request.Scenes)
	}
	if requestedCount < 1 || requestedCount > runtimeSettings.MaxOutputsPerJob {
		return nil, false, fmt.Errorf("%w: output count must be between 1 and %d", ErrImageJobInvalidRequest, runtimeSettings.MaxOutputsPerJob)
	}
	var reservation ImageJobReservation
	if len(input.CandidateModels) > 0 {
		reservation, err = s.billing.EstimateCandidates(ctx, input.APIKey, input.Subscription, request, input.CandidateModels)
	} else {
		reservation, err = s.billing.Estimate(ctx, input.APIKey, input.Subscription, request)
	}
	if err != nil {
		return nil, false, err
	}
	if input.Canvas != nil {
		digest, err = digestImageCanvasJobRequest(digest, input.APIKey.ID, input.Canvas)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %v", ErrImageJobInvalidRequest, err)
		}
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
	operation := imageJobOperation(request.Endpoint)
	if mode == "sequence" {
		operation = "sequence"
	}
	create := &ImageJobCreate{
		PublicID: publicID, UserID: input.APIKey.UserID, APIKeyID: input.APIKey.ID, GroupID: *input.APIKey.GroupID,
		Endpoint: request.Endpoint, Operation: operation, Mode: mode,
		RequestedModel: request.Model, MappedModel: mappedModel, RequestedCount: requestedCount,
		Request: request, RequestDigest: digest, IdempotencyKeyHash: idempotencyHash,
		ReservedUSD: reservation.AmountUSD, ReservationBillingType: reservation.BillingType,
		ReservationSubscriptionID: reservation.SubscriptionID,
		ExpiresAt:                 now.Add(time.Duration(runtimeSettings.ResultTTLSeconds) * time.Second),
		MaxActiveJobsPerUser:      runtimeSettings.MaxActiveJobsPerUser,
		Inputs:                    inputs,
		Canvas:                    cloneImageCanvasJobMetadata(input.Canvas),
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
	if s.metrics != nil {
		s.metrics.JobCreated(mode)
	}
	return job, false, nil
}

func (s *ImageJobService) runtimeSettings() ImageJobRuntimeSettings {
	if s != nil && s.runtime != nil {
		return s.runtime.Current()
	}
	if s != nil {
		return defaultImageJobRuntimeSettings(s.cfg)
	}
	return defaultImageJobRuntimeSettings(nil)
}

func digestImageCanvasJobRequest(baseDigest string, apiKeyID int64, metadata *ImageCanvasJobMetadata) (string, error) {
	if metadata == nil {
		return baseDigest, nil
	}
	payload := struct {
		BaseDigest    string   `json:"base_digest"`
		APIKeyID      int64    `json:"api_key_id"`
		ProjectID     *int64   `json:"project_id"`
		ClientNodeID  string   `json:"client_node_id"`
		SelectedModel string   `json:"selected_model"`
		PolicyVersion int64    `json:"policy_version"`
		AttemptPlan   []string `json:"attempt_plan"`
	}{
		BaseDigest: baseDigest, APIKeyID: apiKeyID, ProjectID: metadata.ProjectID,
		ClientNodeID: metadata.ClientNodeID, SelectedModel: metadata.SelectedModel,
		PolicyVersion: metadata.PolicyVersion, AttemptPlan: metadata.AttemptPlan,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func cloneImageCanvasJobMetadata(metadata *ImageCanvasJobMetadata) *ImageCanvasJobMetadata {
	if metadata == nil {
		return nil
	}
	clone := *metadata
	clone.AttemptPlan = append([]string(nil), metadata.AttemptPlan...)
	if metadata.ProjectID != nil {
		projectID := *metadata.ProjectID
		clone.ProjectID = &projectID
	}
	return &clone
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

// ListRecentAdmin returns a bounded, metadata-only-friendly set of recent
// jobs. The repository extension is optional so existing in-memory workers do
// not have to implement an operations-only query.
func (s *ImageJobService) ListRecentAdmin(ctx context.Context, limit int) ([]*ImageJob, error) {
	if s == nil || s.repo == nil {
		return nil, ErrImageJobUnavailable
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	lister, ok := s.repo.(ImageJobAdminLister)
	if !ok {
		return nil, ErrImageJobUnavailable
	}
	return lister.ListRecentAdmin(ctx, limit)
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
		ID: job.PublicID, Object: "image.job", Status: job.Status,
		StatusURL: fmt.Sprintf("/v1/images/jobs/%s", job.PublicID), Operation: job.Operation, Mode: job.Mode,
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
