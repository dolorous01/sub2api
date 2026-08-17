package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestImageJobWorkerBatchN4UsesOneExecution(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 4)
	require.NoError(t, worker.runOnce(context.Background(), "worker-1"))
	require.Equal(t, 1, deps.executor.CallCount())
	require.Equal(t, 4, deps.executor.Inputs()[0].Parsed.N)
	require.Equal(t, ImageJobStatusCompleted, deps.repo.job.Status)
	require.Equal(t, 4, deps.repo.job.CompletedCount)
	require.Len(t, deps.store.KeysWithPrefix("image-jobs/20/imgjob_worker/results/"), 4)
	require.Equal(t, 1, deps.billingGateway.calls)
}

func TestJobImageResultSinkDoesNotPersistMetadataWhenObjectWriteFails(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 2)
	deps.store.failPutIndex = 1
	err := worker.runOnce(context.Background(), "worker-1")
	require.NoError(t, err)
	require.Equal(t, ImageJobStatusPartial, deps.repo.job.Status)
	require.Equal(t, 1, deps.repo.job.CompletedCount)
	require.Len(t, deps.repo.job.Results, 1)
}

func TestJobImageResultSinkIgnoresDuplicateFinalProgress(t *testing.T) {
	_, deps := newImageJobWorkerFixture(t, 1)
	deps.repo.job.Status = ImageJobStatusRunning
	deps.repo.job.AttemptID = stringPointer("attempt")
	sink := NewJobImageResultSink(deps.repo.job, "attempt", deps.repo, deps.store, nil)
	artifact := ImageArtifact{Index: 0, Data: []byte("png"), MIMEType: "image/png"}
	require.NoError(t, sink.Final(context.Background(), artifact))
	require.NoError(t, sink.Final(context.Background(), artifact))
	require.Equal(t, 1, sink.PersistedCount())
	require.Equal(t, 1, deps.repo.job.CompletedCount)
}

func TestJobImageResultSinkDoesNotDeleteObjectWhenMetadataWriteFails(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 1)
	deps.repo.failUpsert = true
	sink := NewJobImageResultSink(deps.repo.job, "attempt", deps.repo, deps.store, nil)
	artifact := ImageArtifact{Index: 0, Data: []byte("png"), MIMEType: "image/png"}

	require.Error(t, sink.Final(context.Background(), artifact))
	object, err := deps.store.Get(context.Background(), "image-jobs/20/imgjob_worker/results/0.png")
	require.NoError(t, err)
	require.Equal(t, []byte("png"), object.Data)
	require.Equal(t, 0, deps.store.deleteCalls)
}

func TestJobImageResultSinkPreservesPreviouslyCommittedResult(t *testing.T) {
	_, deps := newImageJobWorkerFixture(t, 1)
	key := "image-jobs/20/imgjob_worker/results/0.png"
	deps.store.objects[key] = []byte("old-png")
	deps.repo.job.Results = []ImageJobResult{{Index: 0, Status: "completed", ObjectKey: key}}
	sink := NewJobImageResultSink(deps.repo.job, "attempt", deps.repo, deps.store, nil)

	require.NoError(t, sink.Final(context.Background(), ImageArtifact{Index: 0, Data: []byte("new-png"), MIMEType: "image/png"}))
	object, err := deps.store.Get(context.Background(), key)
	require.NoError(t, err)
	require.Equal(t, []byte("old-png"), object.Data)
	require.Equal(t, 0, deps.repo.upsertCalls)
}

func TestImageJobWorkerBuildFailureStaysBeforeUpstreamPhase(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 1)
	deps.repo.job.Endpoint = openAIImagesEditsEndpoint
	deps.repo.job.Request.Endpoint = openAIImagesEditsEndpoint
	deps.repo.job.Request.Inputs = []ImageJobInputRef{{
		Kind: "image", Index: 0, ObjectKey: "missing/input.png", MIMEType: "image/png",
		ByteSize: 3, SHA256: strings.Repeat("0", 64), FieldName: "image",
	}}

	require.NoError(t, worker.runOnce(context.Background(), "worker-build-failure"))
	require.Equal(t, ImageJobStatusFailed, deps.repo.job.Status)
	require.Equal(t, 0, deps.repo.upstreamCalls)
	require.Equal(t, 0, deps.executor.CallCount())
}

func TestImageJobWorkerUpstreamStartFailureRemainsRecoverablePreflight(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 1)
	startErr := errors.New("database unavailable")
	deps.repo.failUpstreamStart = startErr

	err := worker.runOnce(context.Background(), "worker-start-failure")
	require.ErrorIs(t, err, startErr)
	require.Equal(t, ImageJobStatusRunning, deps.repo.job.Status)
	require.Equal(t, "preflight", deps.repo.job.ExecutionPhase)
	require.Equal(t, 0, deps.repo.upstreamCalls)
	require.Equal(t, 0, deps.executor.CallCount())
	require.Equal(t, "held", deps.repo.job.ReservationStatus)
}

func TestImageJobWorkerContextCancellationAfterUpstreamIsIndeterminate(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	worker.executor = imageExecutorFunc(func(executeCtx context.Context, _ ImageExecutionInput, _ ImageResultSink) (*ImageExecutionResult, error) {
		cancel()
		<-executeCtx.Done()
		return nil, executeCtx.Err()
	})

	require.NoError(t, worker.runOnce(ctx, "worker-canceled-upstream"))
	require.Equal(t, ImageJobStatusIndeterminate, deps.repo.job.Status)
	require.Equal(t, "upstream", deps.repo.job.ExecutionPhase)
	require.Equal(t, 1, deps.repo.releaseCalls)
}

func TestImageJobWorkerLateCancelDoesNotReportSafelyCanceled(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 1)
	deps.executor.failCall = 0
	deps.repo.cancelOnCheck = 3

	require.NoError(t, worker.runOnce(context.Background(), "worker-late-cancel"))
	require.Equal(t, ImageJobStatusFailed, deps.repo.job.Status)
	require.Equal(t, "upstream", deps.repo.job.ExecutionPhase)
	require.NotNil(t, deps.repo.job.Error)
}

func TestImageJobWorkerEditsPreserveImagesMaskAndDefaultFidelity(t *testing.T) {
	for _, test := range []struct {
		name       string
		localCount int
		urlCount   int
		hasMask    bool
	}{
		{name: "single edit", localCount: 1},
		{name: "masked inpaint", localCount: 1, hasMask: true},
		{name: "three image compose", localCount: 3},
		{name: "URL images and mask", urlCount: 2, hasMask: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, deps := newImageJobWorkerFixture(t, 1)
			job := deps.repo.job
			job.Endpoint = openAIImagesEditsEndpoint
			job.Operation = "edit"
			job.Request.Endpoint = openAIImagesEditsEndpoint
			job.Request.Prompt = "replace background"
			for index := 0; index < test.localCount; index++ {
				key := fmt.Sprintf("image-jobs/input-%d.png", index)
				data := []byte(fmt.Sprintf("input-%d", index))
				deps.store.objects[key] = data
				job.Request.Inputs = append(job.Request.Inputs, workerImageInputRef(key, data, index, "image[]"))
			}
			for index := 0; index < test.urlCount; index++ {
				job.Request.InputURLs = append(job.Request.InputURLs, fmt.Sprintf("https://example.test/%d.png", index))
			}
			if test.hasMask {
				if test.localCount > 0 {
					key := "image-jobs/mask.png"
					data := []byte("mask")
					deps.store.objects[key] = data
					ref := workerImageInputRef(key, data, 0, "mask")
					ref.Kind = "mask"
					job.Request.Mask = &ref
				} else {
					job.Request.MaskURL = "https://example.test/mask.png"
				}
			}

			require.NoError(t, worker.runOnce(context.Background(), "worker-edits"))
			require.Equal(t, 1, deps.executor.CallCount())
			parsed := deps.executor.Inputs()[0].Parsed
			require.True(t, parsed.IsEdits())
			require.Equal(t, "replace background", parsed.Prompt)
			require.Equal(t, "high", parsed.InputFidelity)
			require.Len(t, parsed.Uploads, test.localCount)
			require.Len(t, parsed.InputImageURLs, test.urlCount)
			if test.localCount > 0 {
				for _, upload := range parsed.Uploads {
					require.Equal(t, "image[]", upload.FieldName)
				}
			}
			require.Equal(t, test.hasMask, parsed.MaskUpload != nil || parsed.MaskImageURL != "")
		})
	}
}

func workerImageInputRef(key string, data []byte, index int, fieldName string) ImageJobInputRef {
	sum := sha256.Sum256(data)
	return ImageJobInputRef{
		Kind: "image", Index: index, ObjectKey: key, MIMEType: "image/png",
		ByteSize: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), FieldName: fieldName,
	}
}

func TestImageJobWorkerSequenceSettlesEachFrameAndCallsUpstreamOncePerFrame(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 4)
	deps.repo.job.Mode = "sequence"
	deps.repo.job.Request.Endpoint = openAIImagesGenerationsEndpoint
	deps.repo.job.Request.Scenes = []string{"one", "two", "three", "four"}
	deps.executor.failCall = -1

	require.NoError(t, worker.runOnce(context.Background(), "worker-sequence"))
	require.Equal(t, ImageJobStatusCompleted, deps.repo.job.Status)
	require.Equal(t, 4, deps.repo.job.CompletedCount)
	require.Equal(t, 4, deps.executor.CallCount())
	require.Equal(t, []string{"imgjob_worker:0", "imgjob_worker:1", "imgjob_worker:2", "imgjob_worker:3"}, deps.billingGateway.requestIDs)
	require.Equal(t, 1, deps.repo.settleCalls)
	require.Equal(t, "settled", deps.repo.job.ReservationStatus)
	require.Len(t, deps.store.KeysWithPrefix("image-jobs/20/imgjob_worker/results/"), 4)
	operations := make([]string, 0, 4)
	for _, input := range deps.executor.Inputs() {
		if input.Parsed.IsEdits() {
			operations = append(operations, "edit")
		} else {
			operations = append(operations, "generation")
		}
	}
	require.Equal(t, []string{"generation", "edit", "edit", "edit"}, operations)
}

func TestImageJobWorkerSequenceFailureAfterFirstFrameIsPartial(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 4)
	deps.repo.job.Mode = "sequence"
	deps.repo.job.Request.Endpoint = openAIImagesGenerationsEndpoint
	deps.repo.job.Request.Scenes = []string{"one", "two", "three"}
	deps.repo.job.RequestedCount = 3
	deps.executor.outputCount = 1
	deps.executor.failCall = 1

	require.NoError(t, worker.runOnce(context.Background(), "worker-sequence"))
	require.Equal(t, ImageJobStatusPartial, deps.repo.job.Status)
	require.Equal(t, 1, deps.repo.job.CompletedCount)
	require.Equal(t, 2, deps.executor.CallCount())
	require.Equal(t, []string{"imgjob_worker:0"}, deps.billingGateway.requestIDs)
	require.Equal(t, 1, deps.repo.settleCalls)
}

func TestImageJobWorkerSequenceSettlementFailureReleasesReservation(t *testing.T) {
	worker, deps := newImageJobWorkerFixture(t, 3)
	deps.repo.job.Mode = "sequence"
	deps.repo.job.Request.Endpoint = openAIImagesGenerationsEndpoint
	deps.repo.job.Request.Scenes = []string{"one", "two", "three"}
	deps.repo.job.RequestedCount = 3
	deps.billingGateway.failCall = 1

	require.NoError(t, worker.runOnce(context.Background(), "worker-sequence-billing"))
	require.Equal(t, ImageJobStatusPartial, deps.repo.job.Status)
	require.Equal(t, 2, deps.repo.job.CompletedCount)
	require.Equal(t, 0, deps.repo.settleCalls)
	require.Equal(t, 1, deps.repo.releaseCalls)
	require.Equal(t, "released", deps.repo.job.ReservationStatus)
	require.Equal(t, "settlement_failed", deps.repo.job.Error.Code)
}

type imageJobWorkerFixture struct {
	repo           *workerMemoryImageJobRepository
	store          *workerMemoryImageJobStore
	executor       *workerFakeImageExecutor
	billingGateway *workerFakeImageBillingGateway
}

func newImageJobWorkerFixture(t *testing.T, n int) (*ImageJobWorker, *imageJobWorkerFixture) {
	t.Helper()
	groupID := int64(30)
	job := &ImageJob{
		ID: 1, PublicID: "imgjob_worker", UserID: 10, APIKeyID: 20, GroupID: groupID,
		Endpoint: openAIImagesGenerationsEndpoint, Operation: "generation", Mode: "batch",
		RequestedModel: "gpt-image-2", MappedModel: "gpt-image-2", Status: ImageJobStatusQueued,
		RequestedCount: n, RequestDigest: "digest", ReservedUSD: 0.4,
		ReservationBillingType: BillingTypeBalance, ReservationStatus: "held", SettlementStatus: "pending",
		Request:   ImageJobRequest{Endpoint: openAIImagesGenerationsEndpoint, Model: "gpt-image-2", Prompt: "city", N: n, OutputFormat: "png"},
		CreatedAt: time.Now().Add(-time.Second), ExpiresAt: time.Now().Add(time.Hour),
	}
	repo := &workerMemoryImageJobRepository{job: job, results: make(map[int]*ImageJobResult)}
	store := &workerMemoryImageJobStore{objects: make(map[string][]byte), failPutIndex: -1, failDeleteIndex: -1}
	executor := &workerFakeImageExecutor{outputCount: n, failCall: -1}
	provider := &workerFakeImageAPIKeyProvider{apiKey: &APIKey{
		ID: 20, UserID: 10, GroupID: &groupID, Status: StatusActive,
		User: &User{ID: 10, Status: StatusActive}, Group: &Group{ID: groupID, AllowImageGeneration: true},
	}}
	billingGateway := &workerFakeImageBillingGateway{}
	billingGateway.failCall = -1
	billing := NewImageJobBilling(billingGateway, 1, repo)
	cfg := &config.Config{}
	cfg.Gateway.ImageJobs = config.DefaultImageJobsConfig()
	cfg.Gateway.ImageJobs.Enabled = true
	worker := NewImageJobWorker(repo, store, executor, provider, nil, billing, cfg, &ImageJobMetrics{})
	return worker, &imageJobWorkerFixture{repo: repo, store: store, executor: executor, billingGateway: billingGateway}
}

type workerFakeImageExecutor struct {
	mu          sync.Mutex
	inputs      []ImageExecutionInput
	outputCount int
	failCall    int
}

type imageExecutorFunc func(context.Context, ImageExecutionInput, ImageResultSink) (*ImageExecutionResult, error)

func (f imageExecutorFunc) Execute(ctx context.Context, input ImageExecutionInput, sink ImageResultSink) (*ImageExecutionResult, error) {
	return f(ctx, input, sink)
}

func (e *workerFakeImageExecutor) Execute(ctx context.Context, input ImageExecutionInput, sink ImageResultSink) (*ImageExecutionResult, error) {
	e.mu.Lock()
	callIndex := len(e.inputs)
	e.inputs = append(e.inputs, input)
	e.mu.Unlock()
	if callIndex == e.failCall {
		return nil, errors.New("upstream frame failed")
	}
	outputCount := e.outputCount
	if input.Parsed != nil && input.Parsed.N > 0 && input.Parsed.N < outputCount {
		outputCount = input.Parsed.N
	}
	for index := 0; index < outputCount; index++ {
		if err := sink.Final(ctx, ImageArtifact{Index: index, Data: []byte(fmt.Sprintf("png-%d", index)), MIMEType: "image/png", SizeTier: input.Parsed.SizeTier}); err != nil {
			return nil, err
		}
	}
	forward := &OpenAIForwardResult{Model: input.Parsed.Model, UpstreamModel: input.Parsed.Model, ImageCount: outputCount}
	if err := sink.Complete(ctx, ImageExecutionSummary{CreatedAt: time.Now().Unix(), ForwardResult: forward}); err != nil {
		return nil, err
	}
	return &ImageExecutionResult{Forward: forward, Account: &Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, ChannelMapping: input.ChannelMapping}, nil
}

func (e *workerFakeImageExecutor) CallCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.inputs)
}

func (e *workerFakeImageExecutor) Inputs() []ImageExecutionInput {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ImageExecutionInput(nil), e.inputs...)
}

type workerFakeImageAPIKeyProvider struct {
	apiKey *APIKey
}

func (p *workerFakeImageAPIKeyProvider) GetByID(context.Context, int64) (*APIKey, error) {
	return p.apiKey, nil
}

func (*workerFakeImageAPIKeyProvider) CheckAPIKeyQuotaAndExpiry(*APIKey) error { return nil }

type workerFakeImageBillingGateway struct {
	calls      int
	requestIDs []string
	failCall   int
}

func (*workerFakeImageBillingGateway) EstimateImageJobCost(context.Context, *APIKey, ImageJobRequest, int) (*CostBreakdown, bool, error) {
	return &CostBreakdown{ActualCost: 0.4}, false, nil
}

func (g *workerFakeImageBillingGateway) RecordUsageWithCost(_ context.Context, input *OpenAIRecordUsageInput) (*CostBreakdown, error) {
	call := g.calls
	g.calls++
	if input != nil {
		g.requestIDs = append(g.requestIDs, input.BillingRequestID)
	}
	if call == g.failCall {
		return nil, errors.New("usage store unavailable")
	}
	return &CostBreakdown{ActualCost: 0.4}, nil
}

type workerMemoryImageJobStore struct {
	mu              sync.Mutex
	objects         map[string][]byte
	putCalls        int
	failPutIndex    int
	deleteCalls     int
	failDeleteIndex int
}

func (s *workerMemoryImageJobStore) Put(_ context.Context, key string, data []byte, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := s.putCalls
	s.putCalls++
	if call == s.failPutIndex {
		return errors.New("store unavailable")
	}
	s.objects[key] = append([]byte(nil), data...)
	return nil
}

func (s *workerMemoryImageJobStore) Get(_ context.Context, key string) (*ImageJobObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, ErrImageJobNotFound
	}
	return &ImageJobObject{Data: append([]byte(nil), data...), ContentType: "image/png", Size: int64(len(data))}, nil
}

func (s *workerMemoryImageJobStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	call := s.deleteCalls
	s.deleteCalls++
	if call == s.failDeleteIndex {
		s.mu.Unlock()
		return errors.New("delete unavailable")
	}
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}

func (*workerMemoryImageJobStore) Health(context.Context) error { return nil }

func (s *workerMemoryImageJobStore) KeysWithPrefix(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

type workerMemoryImageJobRepository struct {
	mu                sync.Mutex
	job               *ImageJob
	results           map[int]*ImageJobResult
	claimed           bool
	settleCalls       int
	releaseCalls      int
	upstreamCalls     int
	upsertCalls       int
	failUpsert        bool
	failUpstreamStart error
	cancelChecks      int
	cancelOnCheck     int
}

func (*workerMemoryImageJobRepository) CreateReserved(context.Context, *ImageJobCreate) (bool, *ImageJob, error) {
	return false, nil, errors.New("not implemented")
}

func (r *workerMemoryImageJobRepository) GetOwned(_ context.Context, publicID string, apiKeyID int64) (*ImageJob, error) {
	if r.job.PublicID != publicID || r.job.APIKeyID != apiKeyID {
		return nil, ErrImageJobNotFound
	}
	return r.job, nil
}

func (r *workerMemoryImageJobRepository) GetAdmin(_ context.Context, publicID string) (*ImageJob, error) {
	if r.job.PublicID != publicID {
		return nil, ErrImageJobNotFound
	}
	return r.job, nil
}

func (r *workerMemoryImageJobRepository) ClaimNext(_ context.Context, workerID, attemptID string) (*ImageJobClaim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed || r.job.Status != ImageJobStatusQueued {
		return nil, nil
	}
	r.claimed = true
	r.job.Status = ImageJobStatusRunning
	r.job.WorkerID = stringPointer(workerID)
	r.job.AttemptID = stringPointer(attemptID)
	r.job.ExecutionPhase = "preflight"
	return &ImageJobClaim{Job: r.job, WorkerID: workerID, AttemptID: attemptID}, nil
}

func (r *workerMemoryImageJobRepository) MarkUpstreamStarted(_ context.Context, _ int64, attemptID, mappedModel string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failUpstreamStart != nil {
		return r.failUpstreamStart
	}
	if r.job.AttemptID == nil || *r.job.AttemptID != attemptID {
		return ErrImageJobAttemptMismatch
	}
	r.job.ExecutionPhase = "upstream"
	r.job.MappedModel = mappedModel
	r.upstreamCalls++
	return nil
}

func (*workerMemoryImageJobRepository) Heartbeat(context.Context, int64, string, time.Time) error {
	return nil
}

func (r *workerMemoryImageJobRepository) UpsertResult(_ context.Context, _ int64, attemptID string, result *ImageJobResult) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upsertCalls++
	if r.failUpsert {
		return false, errors.New("metadata store unavailable")
	}
	if r.job.AttemptID == nil || *r.job.AttemptID != attemptID {
		return false, ErrImageJobAttemptMismatch
	}
	_, exists := r.results[result.Index]
	copyResult := *result
	r.results[result.Index] = &copyResult
	if !exists {
		r.job.CompletedCount++
	}
	r.job.Results = r.job.Results[:0]
	indices := make([]int, 0, len(r.results))
	for index := range r.results {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		r.job.Results = append(r.job.Results, *r.results[index])
	}
	return !exists, nil
}

func (r *workerMemoryImageJobRepository) SettleReservation(context.Context, int64) error {
	r.settleCalls++
	r.job.ReservationStatus, r.job.SettlementStatus = "settled", "settled"
	return nil
}

func (r *workerMemoryImageJobRepository) ReleaseReservation(context.Context, int64) error {
	r.releaseCalls++
	r.job.ReservationStatus, r.job.SettlementStatus = "released", "released"
	return nil
}

func (r *workerMemoryImageJobRepository) MarkTerminal(_ context.Context, _ int64, attemptID string, update ImageJobTerminalUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.job.AttemptID == nil || *r.job.AttemptID != attemptID {
		return ErrImageJobAttemptMismatch
	}
	r.job.Status = update.Status
	r.job.CompletedCount = update.CompletedCount
	r.job.Error = update.Error
	r.job.Usage = update.Usage
	return nil
}

func (*workerMemoryImageJobRepository) CancelOwned(context.Context, string, int64, time.Time) (*ImageJob, error) {
	return nil, errors.New("not implemented")
}

func (*workerMemoryImageJobRepository) CancelAdmin(context.Context, string, time.Time) (*ImageJob, error) {
	return nil, errors.New("not implemented")
}

func (r *workerMemoryImageJobRepository) IsCancelRequested(context.Context, int64, string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelChecks++
	return r.cancelOnCheck > 0 && r.cancelChecks >= r.cancelOnCheck, nil
}

func (*workerMemoryImageJobRepository) RecoverStale(context.Context, time.Time) (int64, int64, error) {
	return 0, 0, nil
}

func (*workerMemoryImageJobRepository) ListExpired(context.Context, time.Time, int) ([]*ImageJob, error) {
	return nil, nil
}

func (*workerMemoryImageJobRepository) MarkExpired(context.Context, int64, ImageJobStatus, time.Time) error {
	return nil
}

func stringPointer(value string) *string { return &value }

var _ ImageJobRepository = (*workerMemoryImageJobRepository)(nil)
