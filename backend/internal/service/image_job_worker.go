package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/google/uuid"
)

type ImageJobAPIKeyProvider interface {
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	CheckAPIKeyQuotaAndExpiry(apiKey *APIKey) error
}

type ImageJobSubscriptionProvider interface {
	GetByID(ctx context.Context, id int64) (*UserSubscription, error)
}

type ImageJobWorker struct {
	repo          ImageJobRepository
	store         ImageJobObjectStore
	executor      ImageExecutor
	apiKeys       ImageJobAPIKeyProvider
	subscriptions ImageJobSubscriptionProvider
	billing       *ImageJobBilling
	cfg           *config.Config
	metrics       *ImageJobMetrics
	cleanup       *ImageJobCleanup

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewImageJobWorker(
	repo ImageJobRepository,
	store ImageJobObjectStore,
	executor ImageExecutor,
	apiKeys ImageJobAPIKeyProvider,
	subscriptions ImageJobSubscriptionProvider,
	billing *ImageJobBilling,
	cfg *config.Config,
	metrics *ImageJobMetrics,
) *ImageJobWorker {
	if metrics == nil {
		metrics = &ImageJobMetrics{}
	}
	return &ImageJobWorker{
		repo: repo, store: store, executor: executor, apiKeys: apiKeys,
		subscriptions: subscriptions, billing: billing, cfg: cfg, metrics: metrics,
		cleanup: NewImageJobCleanup(repo, store, metrics),
	}
}

func (w *ImageJobWorker) Start() {
	if w == nil || w.cfg == nil || !w.cfg.Gateway.ImageJobs.Enabled {
		return
	}
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.started = true
	w.mu.Unlock()

	settings := w.cfg.Gateway.ImageJobs
	if w.repo != nil {
		cutoff := timezone.Now().Add(-time.Duration(settings.TaskTimeoutSeconds) * time.Second)
		if _, _, err := w.repo.RecoverStale(ctx, cutoff); err != nil {
			logger.LegacyPrintf("service.image_job_worker", "recover stale image jobs: %v", err)
		}
	}
	for index := 0; index < settings.WorkerConcurrency; index++ {
		workerID := fmt.Sprintf("image-worker-%d-%s", index+1, uuid.NewString())
		w.wg.Add(1)
		go w.runLoop(ctx, workerID)
	}
	maintenanceInterval, cleanupInterval := ImageJobMaintenanceIntervals(w.cfg)
	w.wg.Add(1)
	go w.runMaintenance(ctx, maintenanceInterval, cleanupInterval)
}

func (w *ImageJobWorker) runMaintenance(ctx context.Context, staleInterval, cleanupInterval time.Duration) {
	defer w.wg.Done()
	if staleInterval <= 0 {
		staleInterval = time.Minute
	}
	if cleanupInterval <= 0 {
		cleanupInterval = 5 * time.Minute
	}
	staleTicker := time.NewTicker(staleInterval)
	defer staleTicker.Stop()
	cleanupTicker := time.NewTicker(cleanupInterval)
	defer cleanupTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-staleTicker.C:
			if w.repo != nil {
				cutoff := timezone.Now().Add(-time.Duration(w.cfg.Gateway.ImageJobs.TaskTimeoutSeconds) * time.Second)
				if _, _, err := w.repo.RecoverStale(ctx, cutoff); err != nil {
					logger.LegacyPrintf("service.image_job_worker", "recover stale image jobs: %v", err)
				}
			}
		case <-cleanupTicker.C:
			if w.cleanup != nil {
				if err := w.cleanup.RunOnce(ctx); err != nil {
					logger.LegacyPrintf("service.image_job_worker", "cleanup expired image jobs: %v", err)
				}
			}
		}
	}
}

func (w *ImageJobWorker) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	cancel := w.cancel
	w.cancel = nil
	w.started = false
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
}

func (w *ImageJobWorker) runLoop(ctx context.Context, workerID string) {
	defer w.wg.Done()
	poll := time.Duration(w.cfg.Gateway.ImageJobs.PollIntervalMilliseconds) * time.Millisecond
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if err := w.runOnce(ctx, workerID); err != nil && !errors.Is(err, context.Canceled) {
			logger.LegacyPrintf("service.image_job_worker", "worker %s: %v", workerID, err)
		}
		timer.Reset(poll)
	}
}

func (w *ImageJobWorker) runOnce(ctx context.Context, workerID string) error {
	if w == nil || w.repo == nil {
		return fmt.Errorf("image job worker repository is unavailable")
	}
	claim, err := w.repo.ClaimNext(ctx, workerID, uuid.NewString())
	if err != nil || claim == nil || claim.Job == nil {
		return err
	}
	job := claim.Job
	w.metrics.JobClaimed(time.Since(job.CreatedAt))
	timeout := time.Duration(w.cfg.Gateway.ImageJobs.TaskTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stopHeartbeat := w.startHeartbeat(jobCtx, claim)
	defer stopHeartbeat()
	return w.executeClaim(jobCtx, claim)
}

func (w *ImageJobWorker) startHeartbeat(ctx context.Context, claim *ImageJobClaim) func() {
	interval := time.Duration(w.cfg.Gateway.ImageJobs.HeartbeatIntervalSeconds) * time.Second
	if interval <= 0 || claim == nil || claim.Job == nil {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case at := <-ticker.C:
				if err := w.repo.Heartbeat(context.WithoutCancel(ctx), claim.Job.ID, claim.AttemptID, at); err != nil {
					logger.LegacyPrintf("service.image_job_worker", "heartbeat job %s: %v", claim.Job.PublicID, err)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}

func (w *ImageJobWorker) executeClaim(ctx context.Context, claim *ImageJobClaim) error {
	job := claim.Job
	startedAt := time.Now()
	if w.executor == nil || w.store == nil || w.billing == nil {
		return w.markTerminal(ctx, claim, ImageJobStatusFailed, 0, nil, &ImageJobError{
			Type: "api_error", Code: "worker_unavailable", Message: "Image job worker dependencies are unavailable", Retryable: true,
		}, startedAt, 0)
	}
	apiKey, subscription, err := w.preflight(ctx, job)
	if err != nil {
		if ctx.Err() != nil {
			// Keep a canceled preflight claim recoverable. No upstream request has
			// started, so stale recovery can safely put it back on the queue.
			return ctx.Err()
		}
		return w.markTerminal(ctx, claim, ImageJobStatusFailed, 0, nil, imageJobErrorFromExecution(err), startedAt, 0)
	}
	if canceled, err := w.repo.IsCancelRequested(ctx, job.ID, claim.AttemptID); err != nil {
		return err
	} else if canceled {
		return w.markTerminal(ctx, claim, ImageJobStatusCanceled, 0, nil, nil, startedAt, 0)
	}
	mapping := ChannelMappingResult{MappedModel: job.MappedModel}
	if openAIExecutor, ok := w.executor.(*OpenAIImageExecutor); ok && openAIExecutor.gateway != nil {
		if current, _ := openAIExecutor.gateway.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, job.Request.Model); strings.TrimSpace(current.MappedModel) != "" {
			mapping = current
		}
	}
	mappedModel := strings.TrimSpace(firstNonEmptyString(mapping.MappedModel, job.MappedModel, job.Request.Model))
	if job.Mode == "sequence" {
		return w.executeSequenceClaim(ctx, claim, apiKey, subscription, mapping, mappedModel, startedAt)
	}
	return w.executeBatchClaim(ctx, claim, apiKey, subscription, mapping, mappedModel, startedAt)
}

func (w *ImageJobWorker) executeBatchClaim(
	ctx context.Context,
	claim *ImageJobClaim,
	apiKey *APIKey,
	subscription *UserSubscription,
	mapping ChannelMappingResult,
	mappedModel string,
	startedAt time.Time,
) error {
	job := claim.Job
	body, contentType, parsed, err := BuildOpenAIImagesRequest(ctx, w.store, job.Request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return w.markTerminal(ctx, claim, ImageJobStatusFailed, 0, nil, imageJobErrorFromExecution(err), startedAt, 0)
	}
	if canceled, cancelErr := w.repo.IsCancelRequested(ctx, job.ID, claim.AttemptID); cancelErr != nil {
		return cancelErr
	} else if canceled {
		return w.markTerminal(ctx, claim, ImageJobStatusCanceled, 0, nil, nil, startedAt, 0)
	}
	if err := w.repo.MarkUpstreamStarted(ctx, job.ID, claim.AttemptID, mappedModel); err != nil {
		return err
	}
	job.ExecutionPhase = "upstream"
	sink := NewJobImageResultSink(job, claim.AttemptID, w.repo, w.store, w.metrics)
	execution, executeErr := w.executor.Execute(ctx, ImageExecutionInput{
		APIKey: apiKey, User: apiKey.User, Subscription: subscription,
		Body: body, Parsed: parsed, ChannelMapping: mapping,
		SessionHash:     job.RequestDigest,
		InboundEndpoint: job.Endpoint, RequestPayloadHash: job.RequestDigest,
		RequestHeaders: http.Header{"Content-Type": []string{contentType}},
		Observer:       w.imageExecutionObserver(),
	}, sink)
	completed := sink.PersistedCount()
	status := ImageJobStatusCompleted
	var terminalError *ImageJobError
	if executeErr != nil {
		terminalError = imageJobErrorFromExecution(executeErr)
		if completed > 0 {
			status = ImageJobStatusPartial
		} else if imageExecutionIndeterminate(ctx, executeErr) {
			status = ImageJobStatusIndeterminate
		} else {
			status = ImageJobStatusFailed
		}
	} else if completed < job.RequestedCount {
		status = ImageJobStatusPartial
		terminalError = &ImageJobError{Type: "upstream_error", Code: "incomplete_output", Message: "Upstream returned fewer images than requested", Retryable: true}
	}
	if canceled, cancelErr := w.repo.IsCancelRequested(context.WithoutCancel(ctx), job.ID, claim.AttemptID); cancelErr == nil && canceled {
		if completed == 0 {
			if job.ExecutionPhase != "upstream" {
				status = ImageJobStatusCanceled
				terminalError = nil
			}
		} else {
			status = ImageJobStatusPartial
			terminalError = &ImageJobError{Type: "canceled", Code: "cancel_requested", Message: "Image job was canceled after partial completion", Retryable: false}
		}
	}

	settlementDelta := 0.0
	settlement, settleErr := w.billing.Settle(context.WithoutCancel(ctx), ImageJobSettlementInput{
		Job: job, Execution: execution, APIKey: apiKey, User: apiKey.User,
		Subscription: subscription, PersistedResultCount: completed,
		InboundEndpoint: job.Endpoint, UpstreamEndpoint: job.Endpoint,
		QuotaPlatform:      PlatformOpenAI,
		ChannelUsageFields: mapping.ToUsageFields(job.RequestedModel, mappedModel),
		APIKeyService:      imageJobAPIKeyQuotaUpdater(w.apiKeys),
	})
	if settleErr != nil {
		// A failed usage write must not leave a terminal job holding a charge
		// reservation. Release it explicitly; if that transition also fails,
		// leave the claim running so stale recovery can handle it safely.
		if releaseErr := w.repo.ReleaseReservation(context.WithoutCancel(ctx), job.ID); releaseErr != nil {
			return releaseErr
		}
		job.ReservationStatus = "released"
		job.SettlementStatus = "released"
		if terminalError == nil {
			terminalError = &ImageJobError{Type: "billing_error", Code: "settlement_failed", Message: sanitizeImageExecutionMessage(settleErr.Error()), Retryable: true}
		}
		if completed == 0 {
			status = ImageJobStatusFailed
		} else {
			status = ImageJobStatusPartial
		}
	} else {
		if settlement != nil {
			settlementDelta = job.ReservedUSD - settlement.ActualCost
		}
		if completed > 0 {
			job.ReservationStatus = "settled"
			job.SettlementStatus = "settled"
		} else {
			job.ReservationStatus = "released"
			job.SettlementStatus = "released"
		}
	}
	return w.markTerminal(context.WithoutCancel(ctx), claim, status, completed, execution, terminalError, startedAt, settlementDelta)
}

func (w *ImageJobWorker) executeSequenceClaim(
	ctx context.Context,
	claim *ImageJobClaim,
	apiKey *APIKey,
	subscription *UserSubscription,
	mapping ChannelMappingResult,
	mappedModel string,
	startedAt time.Time,
) error {
	job := claim.Job
	actualCost := 0.0
	sequence := &ImageSequenceExecutor{
		job: job, executor: w.executor, store: w.store,
		maxInputs: w.cfg.Gateway.ImageJobs.MaxInputImages,
		input: ImageExecutionInput{
			APIKey: apiKey, User: apiKey.User, Subscription: subscription,
			ChannelMapping: mapping, SessionHash: job.RequestDigest,
			InboundEndpoint: job.Endpoint, RequestPayloadHash: job.RequestDigest,
			Observer: w.imageExecutionObserver(),
		},
		sinkFactory: func(int) ImageResultSink {
			return NewJobImageResultSink(job, claim.AttemptID, w.repo, w.store, w.metrics)
		},
		canceled: func(checkCtx context.Context) (bool, error) {
			return w.repo.IsCancelRequested(checkCtx, job.ID, claim.AttemptID)
		},
		beforeExecute: func(executeCtx context.Context, frameIndex int) error {
			if frameIndex != 0 {
				return nil
			}
			if err := w.repo.MarkUpstreamStarted(executeCtx, job.ID, claim.AttemptID, mappedModel); err != nil {
				return &imageSequenceStartError{err: err}
			}
			job.ExecutionPhase = "upstream"
			return nil
		},
		settleFrame: func(settleCtx context.Context, frameIndex int, execution *ImageExecutionResult) error {
			settlement, err := w.billing.Settle(settleCtx, ImageJobSettlementInput{
				Job: job, FrameIndex: frameIndex, Execution: execution,
				APIKey: apiKey, User: apiKey.User, Subscription: subscription,
				PersistedResultCount: 1, InboundEndpoint: job.Endpoint,
				UpstreamEndpoint:   job.Endpoint,
				QuotaPlatform:      PlatformOpenAI,
				ChannelUsageFields: mapping.ToUsageFields(job.RequestedModel, mappedModel),
				APIKeyService:      imageJobAPIKeyQuotaUpdater(w.apiKeys),
			})
			if err != nil {
				return newImageExecutionError(http.StatusInternalServerError, "billing_error", "settlement_failed", err.Error(), true, false, err)
			}
			if settlement != nil {
				actualCost += settlement.ActualCost
			}
			return nil
		},
	}
	executeErr := sequence.Execute(ctx)
	var startErr *imageSequenceStartError
	if errors.As(executeErr, &startErr) {
		// MarkUpstreamStarted is a compare-and-swap boundary. Leaving the claim
		// in preflight lets stale recovery retry it without duplicating work.
		return startErr.err
	}
	if job.ExecutionPhase != "upstream" && ctx.Err() != nil {
		return ctx.Err()
	}
	completed := len(sequence.Artifacts())
	execution := aggregateImageSequenceExecutions(sequence.Executions(), completed)
	status := ImageJobStatusCompleted
	var terminalError *ImageJobError
	if executeErr != nil {
		terminalError = imageJobErrorFromExecution(executeErr)
		if completed > 0 {
			status = ImageJobStatusPartial
		} else if imageExecutionIndeterminate(ctx, executeErr) {
			status = ImageJobStatusIndeterminate
		} else {
			status = ImageJobStatusFailed
		}
	} else if completed < job.RequestedCount {
		status = ImageJobStatusPartial
		terminalError = &ImageJobError{Type: "upstream_error", Code: "incomplete_output", Message: "Upstream returned fewer sequence frames than requested", Retryable: true}
	}
	if canceled, cancelErr := w.repo.IsCancelRequested(context.WithoutCancel(ctx), job.ID, claim.AttemptID); cancelErr == nil && canceled {
		if completed == 0 {
			if job.ExecutionPhase != "upstream" {
				status = ImageJobStatusCanceled
				terminalError = nil
			}
		} else {
			status = ImageJobStatusPartial
			terminalError = &ImageJobError{Type: "canceled", Code: "cancel_requested", Message: "Image sequence was canceled after partial completion", Retryable: false}
		}
	}

	// A frame is persisted before its usage record is written so storage
	// failures never produce billable metadata. If usage settlement fails,
	// release the remaining hold instead of marking the whole reservation
	// settled; already recorded frame usage remains idempotent by frame ID.
	settlementFailed := imageSequenceSettlementFailed(executeErr)
	reservationErr := error(nil)
	if completed > 0 && !settlementFailed {
		reservationErr = w.repo.SettleReservation(context.WithoutCancel(ctx), job.ID)
		if reservationErr == nil {
			job.ReservationStatus = "settled"
			job.SettlementStatus = "settled"
		}
	} else {
		reservationErr = w.repo.ReleaseReservation(context.WithoutCancel(ctx), job.ID)
		if reservationErr == nil {
			job.ReservationStatus = "released"
			job.SettlementStatus = "released"
		}
	}
	if reservationErr != nil {
		return reservationErr
	}
	settlementDelta := 0.0
	if reservationErr == nil {
		settlementDelta = job.ReservedUSD - actualCost
	}
	return w.markTerminal(context.WithoutCancel(ctx), claim, status, completed, execution, terminalError, startedAt, settlementDelta)
}

func imageSequenceSettlementFailed(err error) bool {
	var executionErr *ImageExecutionError
	return errors.As(err, &executionErr) && executionErr.Type == "billing_error" && executionErr.Code == "settlement_failed"
}

type imageSequenceStartError struct{ err error }

func (e *imageSequenceStartError) Error() string {
	if e == nil || e.err == nil {
		return "mark image sequence upstream start"
	}
	return e.err.Error()
}

func (e *imageSequenceStartError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (w *ImageJobWorker) imageExecutionObserver() ImageExecutionObserver {
	return func(attempt ImageExecutionAttempt) {
		if attempt.Switching && w != nil && w.metrics != nil {
			w.metrics.AccountSwitched()
		}
	}
}

func aggregateImageSequenceExecutions(executions []*ImageExecutionResult, completed int) *ImageExecutionResult {
	if len(executions) == 0 {
		return nil
	}
	last := executions[len(executions)-1]
	if last == nil {
		return nil
	}
	aggregated := *last
	if last.Forward == nil {
		return &aggregated
	}
	forward := *last.Forward
	forward.Usage = OpenAIUsage{}
	forward.Duration = 0
	forward.ImageCount = completed
	forward.ImageOutputSizes = nil
	for _, execution := range executions {
		if execution == nil || execution.Forward == nil {
			continue
		}
		usage := execution.Forward.Usage
		forward.Usage.InputTokens += usage.InputTokens
		forward.Usage.ImageInputTokens += usage.ImageInputTokens
		forward.Usage.OutputTokens += usage.OutputTokens
		forward.Usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		forward.Usage.CacheReadInputTokens += usage.CacheReadInputTokens
		forward.Usage.ImageOutputTokens += usage.ImageOutputTokens
		forward.Duration += execution.Forward.Duration
		forward.ImageOutputSizes = append(forward.ImageOutputSizes, execution.Forward.ImageOutputSizes...)
	}
	aggregated.Forward = &forward
	return &aggregated
}

func (w *ImageJobWorker) preflight(ctx context.Context, job *ImageJob) (*APIKey, *UserSubscription, error) {
	if w.apiKeys == nil {
		return nil, nil, fmt.Errorf("image job API key provider is unavailable")
	}
	apiKey, err := w.apiKeys.GetByID(ctx, job.APIKeyID)
	if err != nil {
		return nil, nil, fmt.Errorf("reload image job API key: %w", err)
	}
	if apiKey == nil {
		return nil, nil, fmt.Errorf("reload image job API key: empty result")
	}
	if apiKey.ID != job.APIKeyID || apiKey.UserID != job.UserID || apiKey.GroupID == nil || *apiKey.GroupID != job.GroupID {
		return nil, nil, fmt.Errorf("image job API key ownership changed")
	}
	if !apiKey.IsActive() || apiKey.User == nil || !apiKey.User.IsActive() {
		return nil, nil, fmt.Errorf("image job API key or user is inactive")
	}
	if apiKey.Group == nil || !GroupAllowsImageGeneration(apiKey.Group) {
		return nil, nil, ErrImageJobPermissionDenied
	}
	if err := w.apiKeys.CheckAPIKeyQuotaAndExpiry(apiKey); err != nil {
		return nil, nil, err
	}
	if job.ReservationBillingType != BillingTypeSubscription {
		return apiKey, nil, nil
	}
	if job.ReservationSubscriptionID == nil || w.subscriptions == nil {
		return nil, nil, fmt.Errorf("image job subscription is unavailable")
	}
	subscription, err := w.subscriptions.GetByID(ctx, *job.ReservationSubscriptionID)
	if err != nil {
		return nil, nil, fmt.Errorf("reload image job subscription: %w", err)
	}
	if subscription == nil {
		return nil, nil, fmt.Errorf("reload image job subscription: empty result")
	}
	if subscription.ID != *job.ReservationSubscriptionID || subscription.UserID != job.UserID || subscription.GroupID != job.GroupID || !subscription.IsActive() {
		return nil, nil, fmt.Errorf("image job subscription is no longer active")
	}
	return apiKey, subscription, nil
}

func (w *ImageJobWorker) markTerminal(
	ctx context.Context,
	claim *ImageJobClaim,
	status ImageJobStatus,
	completed int,
	execution *ImageExecutionResult,
	jobError *ImageJobError,
	startedAt time.Time,
	settlementDelta float64,
) error {
	var usage json.RawMessage
	if execution != nil && execution.Forward != nil {
		usage, _ = json.Marshal(execution.Forward.Usage)
	}
	now := timezone.Now()
	var canceledAt *time.Time
	if status == ImageJobStatusCanceled {
		canceledAt = &now
	}
	reservationStatus := ""
	settlementStatus := ""
	if claim != nil && (claim.Job.ReservationStatus == "settled" || claim.Job.ReservationStatus == "released") {
		reservationStatus = claim.Job.ReservationStatus
		settlementStatus = claim.Job.SettlementStatus
	}
	err := w.repo.MarkTerminal(ctx, claim.Job.ID, claim.AttemptID, ImageJobTerminalUpdate{
		Status: status, CompletedCount: completed, Error: jobError,
		Usage: usage, FinishedAt: now, CanceledAt: canceledAt,
		ReservationStatus: reservationStatus, SettlementStatus: settlementStatus,
	})
	if err == nil {
		w.metrics.JobFinished(status, completed, time.Since(startedAt), settlementDelta)
	}
	return err
}

func imageJobErrorFromExecution(err error) *ImageJobError {
	if err == nil {
		return nil
	}
	var executionError *ImageExecutionError
	if errors.As(err, &executionError) {
		return &ImageJobError{
			Type: executionError.Type, Code: executionError.Code,
			Message: sanitizeImageExecutionMessage(executionError.Error()), Retryable: executionError.Retryable,
		}
	}
	return &ImageJobError{Type: "upstream_error", Code: "image_execution_failed", Message: sanitizeImageExecutionMessage(err.Error()), Retryable: true}
}

func imageExecutionIndeterminate(ctx context.Context, err error) bool {
	var executionError *ImageExecutionError
	if errors.As(err, &executionError) && executionError.Indeterminate {
		return true
	}
	return ctx != nil && ctx.Err() != nil
}

func imageJobAPIKeyQuotaUpdater(provider ImageJobAPIKeyProvider) APIKeyQuotaUpdater {
	updater, _ := provider.(APIKeyQuotaUpdater)
	return updater
}

type JobImageResultSink struct {
	mu        sync.Mutex
	job       *ImageJob
	attemptID string
	repo      ImageJobRepository
	store     ImageJobObjectStore
	metrics   *ImageJobMetrics
	seen      map[int]struct{}
	persisted int
	summary   ImageExecutionSummary
	completed bool
}

func NewJobImageResultSink(job *ImageJob, attemptID string, repo ImageJobRepository, store ImageJobObjectStore, metrics *ImageJobMetrics) *JobImageResultSink {
	seen := make(map[int]struct{})
	if job != nil {
		for _, result := range job.Results {
			if result.Status == "completed" && strings.TrimSpace(result.ObjectKey) != "" {
				seen[result.Index] = struct{}{}
			}
		}
	}
	return &JobImageResultSink{job: job, attemptID: attemptID, repo: repo, store: store, metrics: metrics, seen: seen}
}

func (s *JobImageResultSink) Partial(context.Context, int, []byte, string) error {
	return nil
}

func (s *JobImageResultSink) Final(ctx context.Context, artifact ImageArtifact) error {
	if s == nil || s.job == nil || s.repo == nil || s.store == nil {
		return fmt.Errorf("image job result sink is unavailable")
	}
	if artifact.Index < 0 || artifact.Index >= s.job.RequestedCount {
		return fmt.Errorf("image result index %d is outside requested range", artifact.Index)
	}
	if len(artifact.Data) == 0 {
		return fmt.Errorf("image result %d is empty", artifact.Index)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = make(map[int]struct{})
	}
	if _, exists := s.seen[artifact.Index]; exists {
		// A replay must not overwrite a previously committed result object.
		return nil
	}
	mimeType, extension, err := normalizeImageJobMIMEType(imageArtifactContentType(artifact))
	if err != nil {
		return err
	}
	objectKey := fmt.Sprintf("image-jobs/%d/%s/results/%d.%s", s.job.APIKeyID, s.job.PublicID, artifact.Index, extension)
	if err := s.store.Put(ctx, objectKey, artifact.Data, mimeType); err != nil {
		if s.metrics != nil {
			s.metrics.StorageError()
		}
		return fmt.Errorf("store image result %d: %w", artifact.Index, err)
	}
	inserted, err := s.repo.UpsertResult(ctx, s.job.ID, s.attemptID, &ImageJobResult{
		JobID: s.job.ID, Index: artifact.Index, Status: "completed", ObjectKey: objectKey,
		MIMEType: mimeType, ByteSize: int64(len(artifact.Data)), Width: artifact.Width, Height: artifact.Height,
		SizeTier: artifact.SizeTier, RevisedPrompt: artifact.RevisedPrompt, UpstreamOutputID: artifact.UpstreamOutputID,
	})
	if err != nil {
		// Do not delete the deterministic key here. A concurrent/replayed
		// result may already be referenced by a valid metadata row, and an
		// unconditional delete would remove a downloadable image. TTL cleanup
		// handles objects referenced by the job; failed metadata writes are
		// retained rather than risking data loss.
		return err
	}
	s.seen[artifact.Index] = struct{}{}
	if inserted {
		s.persisted++
	}
	return nil
}

func (s *JobImageResultSink) Complete(_ context.Context, summary ImageExecutionSummary) error {
	if s == nil {
		return fmt.Errorf("image job result sink is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return fmt.Errorf("image job result sink is already complete")
	}
	s.summary = cloneImageExecutionSummary(summary)
	s.completed = true
	return nil
}

func (s *JobImageResultSink) PersistedCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persisted
}

func (s *JobImageResultSink) Summary() ImageExecutionSummary {
	if s == nil {
		return ImageExecutionSummary{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneImageExecutionSummary(s.summary)
}

var _ ImageResultSink = (*JobImageResultSink)(nil)
