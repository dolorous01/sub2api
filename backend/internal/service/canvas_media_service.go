package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	canvasMediaLeaseDuration            = 2 * time.Minute
	canvasMediaLeaseHeartbeat           = 15 * time.Second
	canvasMediaPollInterval             = 3 * time.Second
	canvasMediaWorkerIdleInterval       = time.Second
	canvasMediaVideoTimeout             = 30 * time.Minute
	canvasMediaReferenceMaxBytes        = 16 << 20
	canvasMediaAudioMaxBytes      int64 = 128 << 20
)

var errCanvasMediaAccountBusy = errors.New("canvas media upstream account is busy")

type CanvasVideoParameters struct {
	Seconds       int
	Size          string
	Resolution    string
	GenerateAudio bool
	Watermark     bool
}

type CanvasVideoTaskCreate struct {
	UserID            int64
	APIKeyID          int64
	ProjectPublicID   string
	ClientNodeID      string
	SelectedModel     string
	Prompt            string
	ReferenceAssetIDs []string
	Parameters        CanvasVideoParameters
	IdempotencyKey    string
}

type CanvasAudioParameters struct {
	Voice        string
	Format       string
	Speed        float64
	Instructions string
}

type CanvasAudioTaskCreate struct {
	UserID          int64
	APIKeyID        int64
	ProjectPublicID string
	ClientNodeID    string
	SelectedModel   string
	Prompt          string
	Parameters      CanvasAudioParameters
	IdempotencyKey  string
}

type canvasMediaValidatedRequest struct {
	project        *ImageCanvasProject
	apiKey         *APIKey
	subscription   *UserSubscription
	capability     ImageModelCapability
	billingType    int8
	subscriptionID *int64
}

type CanvasMediaService struct {
	tasks         CanvasMediaTaskRepository
	projects      *ImageCanvasProjectService
	policies      *ImageModelPolicyService
	catalog       ImageModelCatalog
	apiKeys       *APIKeyService
	subscriptions *SubscriptionService
	billing       *BillingCacheService
	gateway       *OpenAIGatewayService
	moderation    *ContentModerationService

	mu           sync.Mutex
	workerCancel context.CancelFunc
	workerDone   chan struct{}
	workerID     string
}

func NewCanvasMediaService(
	tasks CanvasMediaTaskRepository,
	projects *ImageCanvasProjectService,
	policies *ImageModelPolicyService,
	catalog ImageModelCatalog,
	apiKeys *APIKeyService,
	subscriptions *SubscriptionService,
	billing *BillingCacheService,
	gateway *OpenAIGatewayService,
	moderation *ContentModerationService,
) *CanvasMediaService {
	return &CanvasMediaService{
		tasks: tasks, projects: projects, policies: policies, catalog: catalog,
		apiKeys: apiKeys, subscriptions: subscriptions, billing: billing,
		gateway: gateway, moderation: moderation,
		workerID: "media-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
	}
}

func (s *CanvasMediaService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workerCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.workerCancel = cancel
	s.workerDone = make(chan struct{})
	go s.workerLoop(ctx, s.workerDone)
}

func (s *CanvasMediaService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel, done := s.workerCancel, s.workerDone
	s.workerCancel, s.workerDone = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func (s *CanvasMediaService) CreateVideo(ctx context.Context, input CanvasVideoTaskCreate) (*CanvasMediaTask, bool, error) {
	input.ProjectPublicID = strings.TrimSpace(input.ProjectPublicID)
	input.ClientNodeID = strings.TrimSpace(input.ClientNodeID)
	input.SelectedModel = strings.TrimSpace(input.SelectedModel)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if err := validateCanvasMediaIdentity(input.UserID, input.APIKeyID, input.ProjectPublicID, input.ClientNodeID, input.SelectedModel, input.Prompt, input.IdempotencyKey); err != nil {
		return nil, false, err
	}
	validated, err := s.validateRequest(ctx, input.UserID, input.APIKeyID, input.ProjectPublicID, input.SelectedModel, CanvasMediaKindVideo, input.Prompt)
	if err != nil {
		return nil, false, err
	}
	if len(input.ReferenceAssetIDs) > validated.capability.MaxInputImages {
		return nil, false, fmt.Errorf("%w: too many video reference images", ErrCanvasMediaTaskInvalid)
	}
	references := make([]string, 0, len(input.ReferenceAssetIDs))
	seen := make(map[string]struct{}, len(input.ReferenceAssetIDs))
	for _, publicID := range input.ReferenceAssetIDs {
		publicID = strings.TrimSpace(publicID)
		if publicID == "" {
			return nil, false, fmt.Errorf("%w: reference asset ID is required", ErrCanvasMediaTaskInvalid)
		}
		if _, duplicate := seen[publicID]; duplicate {
			continue
		}
		asset, assetErr := s.projects.GetAsset(ctx, input.UserID, publicID)
		if assetErr != nil || asset.MediaKind != "image" || asset.ByteSize > canvasMediaReferenceMaxBytes {
			return nil, false, fmt.Errorf("%w: video reference image is unavailable", ErrCanvasMediaTaskInvalid)
		}
		seen[publicID] = struct{}{}
		references = append(references, publicID)
	}
	input.ReferenceAssetIDs = references
	if err := validateCanvasVideoParameters(&input.Parameters, validated.capability); err != nil {
		return nil, false, err
	}
	request, err := json.Marshal(struct {
		References []string
		Parameters CanvasVideoParameters
	}{References: references, Parameters: input.Parameters})
	if err != nil {
		return nil, false, err
	}
	return s.insertTask(ctx, validated, CanvasMediaKindVideo, input.ClientNodeID, input.SelectedModel, input.Prompt, request, input.IdempotencyKey)
}

func (s *CanvasMediaService) GenerateAudio(ctx context.Context, input CanvasAudioTaskCreate) (*CanvasMediaTask, error) {
	input.ProjectPublicID = strings.TrimSpace(input.ProjectPublicID)
	input.ClientNodeID = strings.TrimSpace(input.ClientNodeID)
	input.SelectedModel = strings.TrimSpace(input.SelectedModel)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if err := validateCanvasMediaIdentity(input.UserID, input.APIKeyID, input.ProjectPublicID, input.ClientNodeID, input.SelectedModel, input.Prompt, input.IdempotencyKey); err != nil {
		return nil, err
	}
	validated, err := s.validateRequest(ctx, input.UserID, input.APIKeyID, input.ProjectPublicID, input.SelectedModel, CanvasMediaKindAudio, input.Prompt)
	if err != nil {
		return nil, err
	}
	if err := validateCanvasAudioParameters(&input.Parameters, validated.capability); err != nil {
		return nil, err
	}
	request, err := json.Marshal(input.Parameters)
	if err != nil {
		return nil, err
	}
	task, _, err := s.insertTask(ctx, validated, CanvasMediaKindAudio, input.ClientNodeID, input.SelectedModel, input.Prompt, request, input.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (s *CanvasMediaService) GetOwned(ctx context.Context, userID int64, publicID string) (*CanvasMediaTask, error) {
	if s == nil || s.tasks == nil {
		return nil, ErrCanvasMediaUpstreamUnavailable
	}
	return s.tasks.GetOwned(ctx, userID, strings.TrimSpace(publicID))
}

func (s *CanvasMediaService) ListRecoverable(ctx context.Context, userID, projectID int64) ([]CanvasMediaTask, error) {
	if s == nil || s.tasks == nil {
		return nil, ErrCanvasMediaUpstreamUnavailable
	}
	return s.tasks.ListRecoverable(ctx, userID, projectID)
}

func (s *CanvasMediaService) CancelOwned(ctx context.Context, userID int64, publicID string) (*CanvasMediaTask, error) {
	if s == nil || s.tasks == nil {
		return nil, ErrCanvasMediaUpstreamUnavailable
	}
	return s.tasks.CancelOwned(ctx, userID, strings.TrimSpace(publicID))
}

func (s *CanvasMediaService) insertTask(
	ctx context.Context,
	validated *canvasMediaValidatedRequest,
	kind CanvasMediaKind,
	clientNodeID, model, prompt string,
	request json.RawMessage,
	idempotencyKey string,
) (*CanvasMediaTask, bool, error) {
	hashPayload, _ := json.Marshal(struct {
		Kind      CanvasMediaKind
		APIKeyID  int64
		ProjectID string
		NodeID    string
		Model     string
		Prompt    string
		Request   json.RawMessage
	}{
		Kind: kind, APIKeyID: validated.apiKey.ID,
		ProjectID: validated.project.PublicID, NodeID: clientNodeID,
		Model: model, Prompt: prompt, Request: request,
	})
	digest := sha256.Sum256(hashPayload)
	return s.tasks.Create(ctx, CanvasMediaTaskInsert{
		PublicID: newImageCanvasServicePublicID("media"), Kind: kind,
		UserID: validated.apiKey.UserID, APIKeyID: validated.apiKey.ID,
		ProjectID: validated.project.ID, ClientNodeID: clientNodeID,
		SelectedModel: model, Prompt: prompt, Request: request,
		RequestHash: hex.EncodeToString(digest[:]), IdempotencyKey: idempotencyKey,
		BillingType: validated.billingType, BillingSubscriptionID: validated.subscriptionID,
	})
}

func (s *CanvasMediaService) validateRequest(
	ctx context.Context,
	userID, apiKeyID int64,
	projectPublicID, model string,
	kind CanvasMediaKind,
	prompt string,
) (*canvasMediaValidatedRequest, error) {
	if s == nil || s.tasks == nil || s.projects == nil || s.policies == nil || s.catalog == nil ||
		s.apiKeys == nil || s.gateway == nil {
		return nil, ErrCanvasMediaUpstreamUnavailable
	}
	project, err := s.projects.Get(ctx, userID, projectPublicID)
	if err != nil {
		return nil, err
	}
	apiKey, err := s.apiKeys.GetByID(ctx, apiKeyID)
	if err != nil || apiKey == nil || apiKey.UserID != userID || apiKey.GroupID == nil ||
		apiKey.Group == nil || !apiKey.IsActive() {
		return nil, ErrImageCanvasAPIKeyNotFound
	}
	if err := s.apiKeys.CheckAPIKeyQuotaAndExpiry(apiKey); err != nil {
		return nil, ErrImageCanvasAPIKeyNotFound
	}
	allowed, err := s.catalog.ForAPIKey(ctx, userID, apiKeyID)
	if err != nil {
		return nil, err
	}
	capability, ok := allowed[model]
	if !ok || capability.MediaKind != string(kind) || !capability.Generation {
		return nil, ErrCanvasMediaCapabilityUnavailable
	}
	expectedProvider := ImageProviderOpenAI
	if kind == CanvasMediaKindVideo {
		expectedProvider = ImageProviderGrok
	}
	if !strings.EqualFold(strings.TrimSpace(capability.Provider), expectedProvider) {
		return nil, ErrCanvasMediaCapabilityUnavailable
	}
	policy, err := s.policies.Get(ctx)
	if err != nil {
		return nil, err
	}
	modelEnabled := false
	if policy.Enabled {
		for _, item := range policy.Items {
			if item.Enabled && strings.TrimSpace(item.Model) == model {
				modelEnabled = true
				break
			}
		}
	}
	if !modelEnabled {
		return nil, ErrCanvasMediaCapabilityUnavailable
	}
	var subscription *UserSubscription
	billingType := BillingTypeBalance
	var subscriptionID *int64
	if apiKey.Group.IsSubscriptionType() {
		if s.subscriptions == nil {
			return nil, ErrImageJobReservationUnavailable
		}
		subscription, err = s.subscriptions.GetActiveSubscription(ctx, userID, *apiKey.GroupID)
		if err != nil {
			return nil, err
		}
		billingType = BillingTypeSubscription
		value := subscription.ID
		subscriptionID = &value
	}
	if s.billing != nil {
		if err := s.billing.CheckBillingEligibility(ctx, apiKey.User, apiKey, apiKey.Group, subscription, QuotaPlatform(ctx, apiKey)); err != nil {
			return nil, err
		}
	}
	if s.moderation != nil {
		endpoint := "/v1/audio/speech"
		if kind == CanvasMediaKindVideo {
			endpoint = "/v1/videos/generations"
		}
		body, _ := json.Marshal(map[string]any{"prompt": prompt})
		decision, checkErr := s.moderation.Check(ctx, ContentModerationCheckInput{
			UserID: userID, APIKeyID: apiKey.ID, APIKeyName: apiKey.Name,
			GroupID: apiKey.GroupID, GroupName: apiKey.Group.Name, Endpoint: endpoint,
			Provider: capability.Provider, Model: model,
			Protocol: ContentModerationProtocolOpenAIImages, Body: body,
		})
		if checkErr != nil {
			return nil, checkErr
		}
		if decision != nil && decision.Blocked {
			return nil, fmt.Errorf("%w: %s", ErrImageCanvasModerationBlocked, strings.TrimSpace(decision.Message))
		}
	}
	return &canvasMediaValidatedRequest{
		project: project, apiKey: apiKey, subscription: subscription, capability: capability,
		billingType: billingType, subscriptionID: subscriptionID,
	}, nil
}

func validateCanvasMediaIdentity(userID, apiKeyID int64, projectID, nodeID, model, prompt, idempotencyKey string) error {
	if userID <= 0 || apiKeyID <= 0 || projectID == "" || nodeID == "" || len(nodeID) > 128 ||
		model == "" || len(model) > 128 || prompt == "" || len(prompt) > 32<<10 ||
		idempotencyKey == "" || len(idempotencyKey) > 255 {
		return ErrCanvasMediaTaskInvalid
	}
	return nil
}

func validateCanvasVideoParameters(parameters *CanvasVideoParameters, capability ImageModelCapability) error {
	if parameters.Seconds <= 0 {
		if len(capability.VideoSeconds) > 0 {
			parameters.Seconds = capability.VideoSeconds[0]
		} else {
			parameters.Seconds = 6
		}
	}
	if len(capability.VideoSeconds) > 0 && !containsCanvasInt(capability.VideoSeconds, parameters.Seconds) {
		return fmt.Errorf("%w: video duration is unsupported", ErrCanvasMediaTaskInvalid)
	}
	if parameters.Seconds < 1 || parameters.Seconds > 20 {
		return fmt.Errorf("%w: video duration is invalid", ErrCanvasMediaTaskInvalid)
	}
	parameters.Size = strings.TrimSpace(parameters.Size)
	if parameters.Size == "" {
		parameters.Size = firstNonEmptyString(capability.Defaults.AspectRatio, "16:9")
	}
	parameters.Size = strings.TrimSpace(parameters.Size)
	if len(capability.AspectRatios) > 0 && !containsCanvasString(capability.AspectRatios, parameters.Size) {
		return fmt.Errorf("%w: video aspect ratio is unsupported", ErrCanvasMediaTaskInvalid)
	}
	parameters.Resolution = strings.TrimSpace(parameters.Resolution)
	if parameters.Resolution == "" {
		parameters.Resolution = firstNonEmptyString(capability.Defaults.Resolution, "720p")
	}
	parameters.Resolution = strings.ToLower(strings.TrimSpace(parameters.Resolution))
	if len(capability.Resolutions) > 0 && !containsCanvasString(capability.Resolutions, parameters.Resolution) {
		return fmt.Errorf("%w: video resolution is unsupported", ErrCanvasMediaTaskInvalid)
	}
	return nil
}

func validateCanvasAudioParameters(parameters *CanvasAudioParameters, capability ImageModelCapability) error {
	parameters.Voice = strings.ToLower(strings.TrimSpace(parameters.Voice))
	parameters.Format = strings.ToLower(strings.TrimSpace(parameters.Format))
	parameters.Instructions = strings.TrimSpace(parameters.Instructions)
	if parameters.Voice == "" {
		parameters.Voice = "alloy"
	}
	if parameters.Format == "" {
		parameters.Format = "mp3"
	}
	if parameters.Speed == 0 {
		parameters.Speed = 1
	}
	if len(capability.AudioVoices) > 0 && !containsCanvasString(capability.AudioVoices, parameters.Voice) {
		return fmt.Errorf("%w: audio voice is unsupported", ErrCanvasMediaTaskInvalid)
	}
	if len(capability.AudioFormats) > 0 && !containsCanvasString(capability.AudioFormats, parameters.Format) {
		return fmt.Errorf("%w: audio format is unsupported", ErrCanvasMediaTaskInvalid)
	}
	minSpeed, maxSpeed := capability.AudioSpeedMin, capability.AudioSpeedMax
	if minSpeed <= 0 {
		minSpeed = 0.25
	}
	if maxSpeed < minSpeed {
		maxSpeed = 4
	}
	if parameters.Speed < minSpeed || parameters.Speed > maxSpeed || len(parameters.Instructions) > 4096 {
		return fmt.Errorf("%w: audio parameters are invalid", ErrCanvasMediaTaskInvalid)
	}
	return nil
}

func containsCanvasString(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func containsCanvasInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *CanvasMediaService) workerLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		s.videoWorkerLoop(ctx)
	}()
	go func() {
		defer workers.Done()
		s.audioWorkerLoop(ctx)
	}()
	workers.Wait()
}

func (s *CanvasMediaService) videoWorkerLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		task, err := s.tasks.ClaimNextVideo(ctx, s.workerID, time.Now().Add(canvasMediaLeaseDuration))
		if err != nil {
			logger.LegacyPrintf("service.canvas_media", "claim video task: %v", err)
			timer.Reset(canvasMediaWorkerIdleInterval)
			continue
		}
		if task == nil {
			timer.Reset(canvasMediaWorkerIdleInterval)
			continue
		}
		taskCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		heartbeatDone := make(chan struct{})
		go s.maintainVideoLease(taskCtx, task.ID, cancel, heartbeatDone)
		err = s.processVideoTask(taskCtx, task)
		cancel()
		<-heartbeatDone
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.LegacyPrintf("service.canvas_media", "process video task %s: %v", task.PublicID, err)
		}
		timer.Reset(25 * time.Millisecond)
	}
}

func (s *CanvasMediaService) audioWorkerLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		task, err := s.tasks.ClaimNextAudio(ctx, s.workerID, time.Now().Add(canvasMediaLeaseDuration))
		if err != nil {
			logger.LegacyPrintf("service.canvas_media", "claim audio task: %v", err)
			timer.Reset(canvasMediaWorkerIdleInterval)
			continue
		}
		if task == nil {
			timer.Reset(canvasMediaWorkerIdleInterval)
			continue
		}
		taskCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		heartbeatDone := make(chan struct{})
		go s.maintainVideoLease(taskCtx, task.ID, cancel, heartbeatDone)
		err = s.processAudioTask(taskCtx, task)
		cancel()
		<-heartbeatDone
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.LegacyPrintf("service.canvas_media", "process audio task %s: %v", task.PublicID, err)
		}
		timer.Reset(25 * time.Millisecond)
	}
}

func (s *CanvasMediaService) maintainVideoLease(
	ctx context.Context,
	taskID int64,
	cancel context.CancelFunc,
	done chan<- struct{},
) {
	s.maintainVideoLeaseEvery(ctx, taskID, cancel, done, canvasMediaLeaseHeartbeat)
}

func (s *CanvasMediaService) maintainVideoLeaseEvery(
	ctx context.Context,
	taskID int64,
	cancel context.CancelFunc,
	done chan<- struct{},
	heartbeat time.Duration,
) {
	defer close(done)
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.tasks.RenewLease(ctx, taskID, s.workerID, time.Now().Add(canvasMediaLeaseDuration)); err != nil {
				cancel()
				return
			}
		}
	}
}

func (s *CanvasMediaService) processVideoTask(ctx context.Context, task *CanvasMediaTask) error {
	if task == nil {
		return nil
	}
	if time.Since(task.CreatedAt) > canvasMediaVideoTimeout {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, CanvasMediaTaskError{
			Code: "video_timeout", Message: "Video generation timed out", Retryable: false,
		})
	}
	if task.UpstreamRequestID == "" {
		if task.AttemptCount > 1 {
			return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusIndeterminate, CanvasMediaTaskError{
				Code: "submission_indeterminate", Message: "Video submission state could not be recovered", Retryable: false,
			})
		}
		return s.submitVideo(ctx, task)
	}
	return s.pollVideo(ctx, task)
}

func (s *CanvasMediaService) processAudioTask(ctx context.Context, task *CanvasMediaTask) error {
	if task == nil {
		return nil
	}
	if task.AttemptCount > 1 {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusIndeterminate, CanvasMediaTaskError{
			Code:      "audio_execution_indeterminate",
			Message:   "Audio execution state could not be recovered safely",
			Retryable: false,
		})
	}
	apiKey, err := s.loadTaskAPIKey(ctx, task)
	if err != nil {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(err, false))
	}
	validated := &canvasMediaValidatedRequest{apiKey: apiKey}
	if err := s.executeAudio(ctx, s.workerID, task, validated); err != nil {
		if errors.Is(err, errCanvasMediaAccountBusy) {
			taskError := canvasMediaTaskError(err, true)
			return s.tasks.RetryQueued(ctx, task.ID, s.workerID, time.Now().Add(canvasMediaWorkerIdleInterval), &taskError)
		}
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(err, false))
	}
	return nil
}

func (s *CanvasMediaService) submitVideo(ctx context.Context, task *CanvasMediaTask) error {
	var request struct {
		References []string
		Parameters CanvasVideoParameters
	}
	if err := json.Unmarshal(task.Request, &request); err != nil {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(err, false))
	}
	apiKey, err := s.loadTaskAPIKey(ctx, task)
	if err != nil {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(err, false))
	}
	selection, err := s.selectMediaAccount(ctx, apiKey, task.SelectedModel, PlatformGrok, task.PublicID)
	if errors.Is(err, errCanvasMediaAccountBusy) {
		return s.retryVideo(ctx, task, err)
	}
	if err != nil {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(err, true))
	}
	account := selection.Account
	defer releaseCanvasMediaSelection(selection)
	mappedModel, _ := account.ResolveMappedModel(task.SelectedModel)
	if mappedModel == "" {
		mappedModel = task.SelectedModel
	}
	payload := map[string]any{
		"model": mappedModel, "prompt": task.Prompt, "seconds": request.Parameters.Seconds,
		"size": request.Parameters.Size, "resolution_name": request.Parameters.Resolution,
		"generate_audio": request.Parameters.GenerateAudio, "watermark": request.Parameters.Watermark,
	}
	if len(request.References) > 0 {
		images := make([]map[string]string, 0, len(request.References))
		for _, assetID := range request.References {
			dataURL, loadErr := s.canvasImageDataURL(ctx, task.UserID, assetID)
			if loadErr != nil {
				return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(loadErr, false))
			}
			images = append(images, map[string]string{"image_url": dataURL})
		}
		payload["image"] = images[0]
		if len(images) > 1 {
			payload["images"] = images
		}
	}
	body, _ := json.Marshal(payload)
	response, err := s.gateway.ExecuteCanvasGrokMedia(ctx, account, GrokMediaEndpointVideosGenerations, "", body, "application/json")
	if err != nil {
		s.gateway.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusIndeterminate, canvasMediaTaskError(err, true))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		s.gateway.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
		upstreamErr := canvasMediaHTTPError(response.StatusCode, response.Body)
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return s.retryVideo(ctx, task, upstreamErr)
		}
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(upstreamErr, false))
	}
	requestID := strings.TrimSpace(response.Result.ResponseID)
	if requestID == "" {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusIndeterminate, CanvasMediaTaskError{
			Code: "missing_upstream_request_id", Message: "Video provider returned no task ID", Retryable: false,
		})
	}
	s.gateway.ReportOpenAIAccountScheduleResult(account.ID, true, nil)
	if err := s.tasks.MarkSubmitted(ctx, task.ID, s.workerID, requestID, account.ID, time.Now().Add(canvasMediaPollInterval)); err != nil {
		return err
	}
	_ = s.gateway.BindGrokMediaVideoRequestAccount(ctx, apiKey.GroupID, requestID, account.ID)
	response.Result.Model = task.SelectedModel
	response.Result.BillingModel = task.SelectedModel
	response.Result.UpstreamModel = mappedModel
	if err := s.recordMediaUsage(ctx, task, apiKey, account, response.Result, "/v1/videos/generations"); err != nil {
		logger.LegacyPrintf("service.canvas_media", "record video usage %s: %v", task.PublicID, err)
	} else {
		_ = s.tasks.MarkBillingRecorded(ctx, task.ID)
	}
	return nil
}

func (s *CanvasMediaService) pollVideo(ctx context.Context, task *CanvasMediaTask) error {
	if task.UpstreamAccountID == nil || *task.UpstreamAccountID <= 0 || s.gateway.accountRepo == nil {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusIndeterminate, CanvasMediaTaskError{
			Code: "video_account_missing", Message: "Video polling account is unavailable", Retryable: false,
		})
	}
	account, err := s.gateway.accountRepo.GetByID(ctx, *task.UpstreamAccountID)
	if err != nil || account == nil || account.Platform != PlatformGrok {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusIndeterminate, CanvasMediaTaskError{
			Code: "video_account_missing", Message: "Video polling account is unavailable", Retryable: false,
		})
	}
	release, acquired, err := s.acquireStoredAccount(ctx, account)
	if err != nil || !acquired {
		return s.tasks.ReschedulePoll(ctx, task.ID, s.workerID, time.Now().Add(canvasMediaPollInterval))
	}
	defer release()
	response, err := s.gateway.ExecuteCanvasGrokMedia(ctx, account, GrokMediaEndpointVideoStatus, task.UpstreamRequestID, nil, "")
	if err != nil {
		return s.tasks.ReschedulePoll(ctx, task.ID, s.workerID, time.Now().Add(canvasMediaPollInterval))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		upstreamErr := canvasMediaHTTPError(response.StatusCode, response.Body)
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return s.tasks.ReschedulePoll(ctx, task.ID, s.workerID, time.Now().Add(canvasMediaPollInterval))
		}
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(upstreamErr, false))
	}
	status := strings.ToLower(strings.TrimSpace(firstGJSON(response.Body,
		"status", "data.status", "video.status", "state")))
	resultURL := firstGJSON(response.Body,
		"video_url", "result_url", "url", "video.url", "result.url",
		"data.video_url", "data.result_url", "data.url", "content.video_url", "content.url")
	switch status {
	case "failed", "cancelled", "canceled", "rejected", "error":
		message := firstGJSON(response.Body, "error.message", "message", "detail")
		if message == "" {
			message = "Video generation failed"
		}
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, CanvasMediaTaskError{
			Code: "video_generation_failed", Message: sanitizeImageExecutionMessage(message), Retryable: false,
		})
	case "completed", "succeeded", "success", "done":
		if resultURL == "" {
			return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, CanvasMediaTaskError{
				Code: "video_result_missing", Message: "Video provider returned no result", Retryable: false,
			})
		}
	default:
		if resultURL == "" {
			return s.tasks.ReschedulePoll(ctx, task.ID, s.workerID, time.Now().Add(canvasMediaPollInterval))
		}
	}
	staged, err := downloadCanvasMediaURL(ctx, resultURL, int64(maxVideoCanvasAssetBytes))
	if err != nil {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(err, false))
	}
	defer staged.Cleanup()
	file, err := staged.Open()
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	var request struct {
		References []string
		Parameters CanvasVideoParameters
	}
	if err := json.Unmarshal(task.Request, &request); err != nil {
		return err
	}
	width, height := canvasVideoDimensions(request.Parameters.Size, request.Parameters.Resolution)
	parents, _ := json.Marshal(request.References)
	asset, err := s.projects.UploadAssetStream(ctx, ImageAssetStreamUpload{
		UserID: task.UserID, ProjectPublicID: task.ProjectPublicID,
		FileName: "generated-video" + canvasMediaExtension(staged.contentType, ".mp4"),
		MIMEType: staged.contentType, Width: width, Height: height,
		DurationMS: int64(request.Parameters.Seconds) * 1000,
		ByteSize:   staged.size, Reader: file, SourceType: "generated", ParentAssetIDs: parents,
	})
	if err != nil {
		return s.tasks.Fail(ctx, task.ID, s.workerID, ImageJobStatusFailed, canvasMediaTaskError(err, false))
	}
	return s.tasks.Complete(ctx, task.ID, s.workerID, asset.ID, task.SelectedModel)
}

func (s *CanvasMediaService) executeAudio(ctx context.Context, owner string, task *CanvasMediaTask, validated *canvasMediaValidatedRequest) error {
	var parameters CanvasAudioParameters
	if err := json.Unmarshal(task.Request, &parameters); err != nil {
		return err
	}
	selection, err := s.selectMediaAccount(ctx, validated.apiKey, task.SelectedModel, PlatformOpenAI, task.PublicID)
	if err != nil {
		return err
	}
	defer releaseCanvasMediaSelection(selection)
	account := selection.Account
	mappedModel, _ := account.ResolveMappedModel(task.SelectedModel)
	if mappedModel == "" {
		mappedModel = task.SelectedModel
	}
	payload := map[string]any{
		"model": mappedModel, "input": task.Prompt, "voice": parameters.Voice,
		"response_format": parameters.Format, "speed": parameters.Speed,
	}
	if parameters.Instructions != "" {
		payload["instructions"] = parameters.Instructions
	}
	body, _ := json.Marshal(payload)
	started := time.Now()
	response, err := s.gateway.ExecuteCanvasAudioSpeech(ctx, account, body)
	if err != nil {
		s.gateway.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		s.gateway.ReportOpenAIAccountScheduleResult(account.ID, false, nil)
		return canvasMediaHTTPError(response.StatusCode, errorBody)
	}
	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	if strings.Contains(strings.ToLower(contentType), "json") {
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("audio provider returned JSON: %s", sanitizeImageExecutionMessage(extractUpstreamErrorMessage(errorBody)))
	}
	staged, err := stageCanvasMediaReader(ctx, response.Body, response.ContentLength, canvasMediaAudioMaxBytes, contentType)
	if err != nil {
		return err
	}
	defer staged.Cleanup()
	file, err := staged.Open()
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	durationMS := estimateSpeechDurationMS(task.Prompt, parameters.Speed)
	asset, err := s.projects.UploadAssetStream(ctx, ImageAssetStreamUpload{
		UserID: task.UserID, ProjectPublicID: task.ProjectPublicID,
		FileName: "generated-audio." + parameters.Format, MIMEType: contentType,
		DurationMS: durationMS, ByteSize: staged.size, Reader: file, SourceType: "generated",
	})
	if err != nil {
		return err
	}
	s.gateway.ReportOpenAIAccountScheduleResult(account.ID, true, nil)
	result := &OpenAIForwardResult{
		RequestID: firstNonEmpty(response.Header.Get("x-request-id"), response.Header.Get("request-id")),
		Model:     task.SelectedModel, BillingModel: task.SelectedModel, UpstreamModel: mappedModel,
		Duration: time.Since(started),
		Usage: OpenAIUsage{
			InputTokens:  max(1, (utf8.RuneCountInString(task.Prompt)+3)/4),
			OutputTokens: max(1, int((durationMS*21+999)/1000)),
		},
	}
	if err := s.recordMediaUsage(ctx, task, validated.apiKey, account, result, "/v1/audio/speech"); err != nil {
		return err
	}
	_ = s.tasks.MarkBillingRecorded(ctx, task.ID)
	return s.tasks.Complete(ctx, task.ID, owner, asset.ID, task.SelectedModel)
}

func (s *CanvasMediaService) loadTaskAPIKey(ctx context.Context, task *CanvasMediaTask) (*APIKey, error) {
	apiKey, err := s.apiKeys.GetByID(ctx, task.APIKeyID)
	if err != nil || apiKey == nil || apiKey.UserID != task.UserID || apiKey.GroupID == nil ||
		apiKey.Group == nil || !apiKey.IsActive() {
		return nil, ErrImageCanvasAPIKeyNotFound
	}
	if err := s.apiKeys.CheckAPIKeyQuotaAndExpiry(apiKey); err != nil {
		return nil, ErrImageCanvasAPIKeyNotFound
	}
	return apiKey, nil
}

func (s *CanvasMediaService) selectMediaAccount(ctx context.Context, apiKey *APIKey, model, platform, seed string) (*AccountSelectionResult, error) {
	selection, _, err := s.gateway.SelectAccountWithSchedulerForCapability(
		ctx, apiKey.GroupID, "", DeriveSessionHashFromSeed(seed), model, nil,
		OpenAIUpstreamTransportHTTPSSE, "", false, platform,
	)
	if err != nil || selection == nil || selection.Account == nil {
		if err == nil {
			err = ErrCanvasMediaUpstreamUnavailable
		}
		return nil, err
	}
	if !selection.Acquired {
		return nil, errCanvasMediaAccountBusy
	}
	return selection, nil
}

func releaseCanvasMediaSelection(selection *AccountSelectionResult) {
	if selection != nil && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func (s *CanvasMediaService) acquireStoredAccount(ctx context.Context, account *Account) (func(), bool, error) {
	if s.gateway.concurrencyService == nil {
		return func() {}, true, nil
	}
	result, err := s.gateway.concurrencyService.AcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil || result == nil || !result.Acquired {
		return func() {}, false, err
	}
	return result.ReleaseFunc, true, nil
}

func (s *CanvasMediaService) canvasImageDataURL(ctx context.Context, userID int64, publicID string) (string, error) {
	asset, err := s.projects.GetAsset(ctx, userID, publicID)
	if err != nil || asset.MediaKind != "image" || asset.ByteSize > canvasMediaReferenceMaxBytes {
		return "", ErrImageAssetNotFound
	}
	object, err := s.projects.GetAssetObject(ctx, userID, publicID, false)
	if err != nil || object == nil || int64(len(object.Data)) != asset.ByteSize {
		return "", ErrImageAssetNotFound
	}
	return "data:" + asset.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(object.Data), nil
}

func (s *CanvasMediaService) retryVideo(ctx context.Context, task *CanvasMediaTask, cause error) error {
	delay := time.Duration(min(30, max(2, task.AttemptCount*2))) * time.Second
	return s.tasks.RetryQueued(ctx, task.ID, s.workerID, time.Now().Add(delay), &CanvasMediaTaskError{
		Code: "video_upstream_busy", Message: sanitizeImageExecutionMessage(cause.Error()), Retryable: true,
	})
}

func (s *CanvasMediaService) recordMediaUsage(
	ctx context.Context,
	task *CanvasMediaTask,
	apiKey *APIKey,
	account *Account,
	result *OpenAIForwardResult,
	endpoint string,
) error {
	billingType := task.BillingType
	return s.gateway.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: result, APIKey: apiKey, User: apiKey.User, Account: account,
		InboundEndpoint: endpoint, UpstreamEndpoint: endpoint,
		BillingRequestID: "canvas-media:" + task.PublicID,
		BillingType:      &billingType, BillingSubscriptionID: task.BillingSubscriptionID,
		RequestPayloadHash: task.RequestHash, APIKeyService: s.apiKeys,
		QuotaPlatform: account.Platform,
		ChannelUsageFields: ChannelUsageFields{
			OriginalModel: task.SelectedModel, ChannelMappedModel: result.UpstreamModel,
		},
	})
}

func canvasMediaHTTPError(status int, body []byte) error {
	message := sanitizeImageExecutionMessage(extractUpstreamErrorMessage(body))
	if message == "" {
		message = fmt.Sprintf("Media provider returned status %d", status)
	}
	return fmt.Errorf("%s", message)
}

func canvasMediaTaskError(err error, retryable bool) CanvasMediaTaskError {
	message := "Media generation failed"
	if err != nil {
		message = sanitizeImageExecutionMessage(err.Error())
	}
	return CanvasMediaTaskError{Code: "media_generation_failed", Message: message, Retryable: retryable}
}

func firstGJSON(body []byte, paths ...string) string {
	for _, path := range paths {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			return value
		}
	}
	return ""
}

func canvasVideoDimensions(size, resolution string) (int, int) {
	shortEdge := 720
	if strings.EqualFold(strings.TrimSpace(resolution), "480p") {
		shortEdge = 480
	}
	switch strings.TrimSpace(size) {
	case "9:16":
		return shortEdge, shortEdge * 16 / 9
	case "1:1":
		return shortEdge, shortEdge
	default:
		return shortEdge * 16 / 9, shortEdge
	}
}

func estimateSpeechDurationMS(prompt string, speed float64) int64 {
	if speed <= 0 {
		speed = 1
	}
	runes := max(1, utf8.RuneCountInString(prompt))
	duration := int64(float64(runes) / (12 * speed) * 1000)
	if duration < 1000 {
		duration = 1000
	}
	if duration > maxCanvasMediaDurationMS {
		duration = maxCanvasMediaDurationMS
	}
	return duration
}

func canvasMediaExtension(contentType, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	default:
		if extension := filepath.Ext(fallback); extension != "" {
			return extension
		}
		return fallback
	}
}
