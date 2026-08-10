package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestImageJobServiceCreateReplayAndOwnership(t *testing.T) {
	svc, deps := newImageJobServiceFixture()

	first, replayed, err := svc.Create(context.Background(), deps.createInput("same-key"))
	if err != nil || replayed || first == nil {
		t.Fatalf("first Create() = %#v, %t, %v", first, replayed, err)
	}
	second, replayed, err := svc.Create(context.Background(), deps.createInput("same-key"))
	if err != nil || !replayed || second == nil {
		t.Fatalf("replay Create() = %#v, %t, %v", second, replayed, err)
	}
	if second.PublicID != first.PublicID {
		t.Fatalf("replay public ID = %q, want %q", second.PublicID, first.PublicID)
	}
	if _, err := svc.GetOwned(context.Background(), first.PublicID, first.APIKeyID+1); !errors.Is(err, ErrImageJobNotFound) {
		t.Fatalf("GetOwned() error = %v, want not found", err)
	}
}

func TestImageJobServiceCreateCleansUploadedInputsOnReplay(t *testing.T) {
	svc, deps := newImageJobServiceFixture()
	input := deps.createInput("same-key")
	input.Parsed.Uploads = []OpenAIImagesUpload{{ContentType: "image/png", Data: []byte("image")}}

	if _, replayed, err := svc.Create(context.Background(), input); err != nil || replayed {
		t.Fatalf("first Create() replayed = %t, error = %v", replayed, err)
	}
	deletedBefore := len(deps.store.deleted)
	if _, replayed, err := svc.Create(context.Background(), input); err != nil || !replayed {
		t.Fatalf("replay Create() replayed = %t, error = %v", replayed, err)
	}
	if len(deps.store.deleted) != deletedBefore+1 {
		t.Fatalf("deleted objects = %d, want %d", len(deps.store.deleted), deletedBefore+1)
	}
}

func TestImageJobServiceCreateCleansUploadedInputsOnRepositoryFailure(t *testing.T) {
	svc, deps := newImageJobServiceFixture()
	deps.repo.createErr = errors.New("database unavailable")
	input := deps.createInput("")
	input.Parsed.Uploads = []OpenAIImagesUpload{{ContentType: "image/png", Data: []byte("image")}}

	if _, _, err := svc.Create(context.Background(), input); err == nil {
		t.Fatal("Create() error = nil, want repository failure")
	}
	if len(deps.store.deleted) != 1 {
		t.Fatalf("deleted objects = %d, want 1", len(deps.store.deleted))
	}
}

func TestImageJobServiceEstimatesBeforeUploadingInputs(t *testing.T) {
	svc, deps := newImageJobServiceFixture()
	deps.billingGateway.estimateErr = errors.New("price unavailable")
	input := deps.createInput("")
	input.Parsed.Uploads = []OpenAIImagesUpload{{ContentType: "image/png", Data: []byte("image")}}

	if _, _, err := svc.Create(context.Background(), input); !errors.Is(err, ErrImageJobReservationUnavailable) {
		t.Fatalf("Create() error = %v, want reservation unavailable", err)
	}
	if deps.store.putCalls != 0 {
		t.Fatalf("store Put calls = %d, want 0 when reservation estimate fails", deps.store.putCalls)
	}
}

func TestImageJobServiceCreateValidatesAvailabilityPermissionAndIdempotencyKey(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		svc, deps := newImageJobServiceFixture()
		deps.cfg.Gateway.ImageJobs.Enabled = false
		if _, _, err := svc.Create(context.Background(), deps.createInput("")); !errors.Is(err, ErrImageJobDisabled) {
			t.Fatalf("Create() error = %v, want disabled", err)
		}
	})

	t.Run("storage health", func(t *testing.T) {
		svc, deps := newImageJobServiceFixture()
		deps.store.healthErr = errors.New("storage unavailable")
		if _, _, err := svc.Create(context.Background(), deps.createInput("")); !errors.Is(err, ErrImageJobUnavailable) {
			t.Fatalf("Create() error = %v, want unavailable", err)
		}
	})

	t.Run("group permission", func(t *testing.T) {
		svc, deps := newImageJobServiceFixture()
		input := deps.createInput("")
		input.APIKey.Group.AllowImageGeneration = false
		if _, _, err := svc.Create(context.Background(), input); !errors.Is(err, ErrImageJobPermissionDenied) {
			t.Fatalf("Create() error = %v, want permission denied", err)
		}
	})

	t.Run("missing group snapshot", func(t *testing.T) {
		svc, deps := newImageJobServiceFixture()
		input := deps.createInput("")
		input.APIKey.Group = nil
		if _, _, err := svc.Create(context.Background(), input); !errors.Is(err, ErrImageJobInvalidRequest) {
			t.Fatalf("Create() error = %v, want invalid request", err)
		}
	})

	t.Run("invalid mode", func(t *testing.T) {
		svc, deps := newImageJobServiceFixture()
		input := deps.createInput("")
		input.Mode = "unknown"
		if _, _, err := svc.Create(context.Background(), input); !errors.Is(err, ErrImageJobInvalidRequest) {
			t.Fatalf("Create() error = %v, want invalid request", err)
		}
	})

	t.Run("idempotency key too long", func(t *testing.T) {
		svc, deps := newImageJobServiceFixture()
		key := make([]byte, 256)
		for index := range key {
			key[index] = 'x'
		}
		if _, _, err := svc.Create(context.Background(), deps.createInput(string(key))); !errors.Is(err, ErrImageJobInvalidRequest) {
			t.Fatalf("Create() error = %v, want invalid request", err)
		}
	})
}

func TestImageJobServiceCreateStoresHashedIdempotencyKeyAndNoSecret(t *testing.T) {
	svc, deps := newImageJobServiceFixture()
	input := deps.createInput("  client-secret-key  ")

	job, _, err := svc.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if deps.repo.lastCreate == nil || deps.repo.lastCreate.IdempotencyKeyHash == nil {
		t.Fatal("CreateReserved() idempotency hash = nil")
	}
	if *deps.repo.lastCreate.IdempotencyKeyHash == input.IdempotencyKey || *deps.repo.lastCreate.IdempotencyKeyHash == "client-secret-key" {
		t.Fatalf("persisted idempotency hash leaked plaintext: %q", *deps.repo.lastCreate.IdempotencyKeyHash)
	}
	if job.IdempotencyKeyHash == nil || *job.IdempotencyKeyHash != *deps.repo.lastCreate.IdempotencyKeyHash {
		t.Fatalf("job idempotency hash = %v, want repository hash", job.IdempotencyKeyHash)
	}
}

func TestImageJobServiceCancelOwnedAndAdminDelegateScope(t *testing.T) {
	svc, deps := newImageJobServiceFixture()
	job, _, err := svc.Create(context.Background(), deps.createInput(""))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := svc.CancelOwned(context.Background(), job.PublicID, job.APIKeyID+1); !errors.Is(err, ErrImageJobNotFound) {
		t.Fatalf("CancelOwned() error = %v, want not found", err)
	}
	canceled, err := svc.CancelAdmin(context.Background(), job.PublicID)
	if err != nil || canceled.Status != ImageJobStatusCanceled {
		t.Fatalf("CancelAdmin() = %#v, %v", canceled, err)
	}
}

func TestImageJobResponseUsesStableResultURLs(t *testing.T) {
	started := time.Unix(1_700_000_001, 0).UTC()
	job := &ImageJob{
		PublicID: "imgjob_test", Status: ImageJobStatusPartial, Operation: "generation", Mode: "batch",
		RequestedModel: "gpt-image-2", RequestedCount: 2, CompletedCount: 1,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(), StartedAt: &started, ExpiresAt: time.Unix(1_700_086_400, 0).UTC(),
		Results: []ImageJobResult{{Index: 1, Status: "completed", MIMEType: "image/png", SizeTier: "1K", RevisedPrompt: "revised"}},
	}

	response := job.ToResponse()
	if response.ID != job.PublicID || response.Object != "image.job" || response.StartedAt == nil || *response.StartedAt != started.Unix() {
		t.Fatalf("ToResponse() metadata = %#v", response)
	}
	if len(response.Data) != 1 || response.Data[0].URL != "/v1/images/jobs/imgjob_test/results/1" {
		t.Fatalf("ToResponse() data = %#v", response.Data)
	}
}

type imageJobServiceFixture struct {
	repo           *fakeImageJobServiceRepository
	store          *fakeImageJobServiceStore
	billingGateway *fakeImageJobServiceBillingGateway
	cfg            *config.Config
}

func newImageJobServiceFixture() (*ImageJobService, *imageJobServiceFixture) {
	repo := &fakeImageJobServiceRepository{jobs: make(map[string]*ImageJob), idempotency: make(map[string]*ImageJob)}
	store := &fakeImageJobServiceStore{objects: make(map[string][]byte)}
	cfg := &config.Config{}
	cfg.Gateway.ImageJobs = config.DefaultImageJobsConfig()
	cfg.Gateway.ImageJobs.Enabled = true
	billingGateway := &fakeImageJobServiceBillingGateway{}
	billing := NewImageJobBilling(billingGateway, cfg.Gateway.ImageJobs.MaxReservationUSD)
	fixture := &imageJobServiceFixture{repo: repo, store: store, billingGateway: billingGateway, cfg: cfg}
	return NewImageJobService(repo, store, billing, cfg), fixture
}

func (f *imageJobServiceFixture) createInput(idempotencyKey string) CreateImageJobInput {
	groupID := int64(30)
	user := &User{ID: 10}
	group := &Group{ID: groupID, AllowImageGeneration: true}
	apiKey := &APIKey{ID: 20, UserID: user.ID, User: user, GroupID: &groupID, Group: group}
	return CreateImageJobInput{
		APIKey: apiKey, Parsed: &OpenAIImagesRequest{
			Endpoint: openAIImagesGenerationsEndpoint, Model: "gpt-image-2", Prompt: "city", N: 2, Size: "1K",
		},
		Mode: "batch", IdempotencyKey: idempotencyKey, MappedModel: "gpt-image-2",
	}
}

type fakeImageJobServiceBillingGateway struct {
	estimateErr error
}

func (f *fakeImageJobServiceBillingGateway) EstimateImageJobCost(context.Context, *APIKey, ImageJobRequest, int) (*CostBreakdown, bool, error) {
	if f.estimateErr != nil {
		return nil, false, f.estimateErr
	}
	return &CostBreakdown{ActualCost: 0.25}, false, nil
}

func (*fakeImageJobServiceBillingGateway) RecordUsageWithCost(context.Context, *OpenAIRecordUsageInput) (*CostBreakdown, error) {
	return nil, errors.New("unexpected settlement")
}

type fakeImageJobServiceStore struct {
	objects   map[string][]byte
	deleted   []string
	putCalls  int
	healthErr error
}

func (s *fakeImageJobServiceStore) Put(_ context.Context, key string, data []byte, _ string) error {
	s.putCalls++
	s.objects[key] = append([]byte(nil), data...)
	return nil
}

func (s *fakeImageJobServiceStore) Get(_ context.Context, key string) (*ImageJobObject, error) {
	data, ok := s.objects[key]
	if !ok {
		return nil, ErrImageJobNotFound
	}
	return &ImageJobObject{Data: append([]byte(nil), data...), Size: int64(len(data))}, nil
}

func (s *fakeImageJobServiceStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *fakeImageJobServiceStore) Health(context.Context) error { return s.healthErr }

type fakeImageJobServiceRepository struct {
	jobs        map[string]*ImageJob
	idempotency map[string]*ImageJob
	lastCreate  *ImageJobCreate
	createErr   error
	nextID      int64
}

func (r *fakeImageJobServiceRepository) CreateReserved(_ context.Context, create *ImageJobCreate) (bool, *ImageJob, error) {
	r.lastCreate = create
	if r.createErr != nil {
		return false, nil, r.createErr
	}
	if create.IdempotencyKeyHash != nil {
		if existing := r.idempotency[*create.IdempotencyKeyHash]; existing != nil {
			if existing.RequestDigest != create.RequestDigest {
				return false, nil, ErrImageJobIdempotencyConflict
			}
			return false, existing, nil
		}
	}
	r.nextID++
	job := &ImageJob{
		ID: r.nextID, PublicID: create.PublicID, UserID: create.UserID, APIKeyID: create.APIKeyID, GroupID: create.GroupID,
		Endpoint: create.Endpoint, Operation: create.Operation, Mode: create.Mode, RequestedModel: create.RequestedModel,
		MappedModel: create.MappedModel, Status: ImageJobStatusQueued, RequestedCount: create.RequestedCount,
		Request: create.Request, RequestDigest: create.RequestDigest, IdempotencyKeyHash: create.IdempotencyKeyHash,
		ReservedUSD: create.ReservedUSD, ReservationBillingType: create.ReservationBillingType,
		ReservationSubscriptionID: create.ReservationSubscriptionID, ReservationStatus: "held", SettlementStatus: "pending",
		ExpiresAt: create.ExpiresAt, CreatedAt: time.Now().UTC(), Inputs: append([]ImageJobInput(nil), create.Inputs...),
	}
	r.jobs[job.PublicID] = job
	if create.IdempotencyKeyHash != nil {
		r.idempotency[*create.IdempotencyKeyHash] = job
	}
	return true, job, nil
}

func (r *fakeImageJobServiceRepository) GetOwned(_ context.Context, publicID string, apiKeyID int64) (*ImageJob, error) {
	job := r.jobs[publicID]
	if job == nil || job.APIKeyID != apiKeyID {
		return nil, ErrImageJobNotFound
	}
	return job, nil
}

func (r *fakeImageJobServiceRepository) GetAdmin(_ context.Context, publicID string) (*ImageJob, error) {
	job := r.jobs[publicID]
	if job == nil {
		return nil, ErrImageJobNotFound
	}
	return job, nil
}

func (r *fakeImageJobServiceRepository) CancelOwned(_ context.Context, publicID string, apiKeyID int64, at time.Time) (*ImageJob, error) {
	job, err := r.GetOwned(context.Background(), publicID, apiKeyID)
	if err != nil {
		return nil, err
	}
	return fakeCancelImageJob(job, at)
}

func (r *fakeImageJobServiceRepository) CancelAdmin(_ context.Context, publicID string, at time.Time) (*ImageJob, error) {
	job, err := r.GetAdmin(context.Background(), publicID)
	if err != nil {
		return nil, err
	}
	return fakeCancelImageJob(job, at)
}

func fakeCancelImageJob(job *ImageJob, at time.Time) (*ImageJob, error) {
	if job.Status.Terminal() && job.Status != ImageJobStatusCanceled {
		return nil, ErrImageJobCancelConflict
	}
	job.Status = ImageJobStatusCanceled
	job.CanceledAt = &at
	return job, nil
}

func (*fakeImageJobServiceRepository) ClaimNext(context.Context, string, string) (*ImageJobClaim, error) {
	return nil, nil
}
func (*fakeImageJobServiceRepository) MarkUpstreamStarted(context.Context, int64, string, string) error {
	return nil
}
func (*fakeImageJobServiceRepository) Heartbeat(context.Context, int64, string, time.Time) error {
	return nil
}
func (*fakeImageJobServiceRepository) UpsertResult(context.Context, int64, string, *ImageJobResult) (bool, error) {
	return false, nil
}
func (*fakeImageJobServiceRepository) SettleReservation(context.Context, int64) error  { return nil }
func (*fakeImageJobServiceRepository) ReleaseReservation(context.Context, int64) error { return nil }
func (*fakeImageJobServiceRepository) MarkTerminal(context.Context, int64, string, ImageJobTerminalUpdate) error {
	return nil
}
func (*fakeImageJobServiceRepository) IsCancelRequested(context.Context, int64, string) (bool, error) {
	return false, nil
}
func (*fakeImageJobServiceRepository) RecoverStale(context.Context, time.Time) (int64, int64, error) {
	return 0, 0, nil
}
func (*fakeImageJobServiceRepository) ListExpired(context.Context, time.Time, int) ([]*ImageJob, error) {
	return nil, nil
}
func (*fakeImageJobServiceRepository) MarkExpired(context.Context, int64, ImageJobStatus, time.Time) error {
	return nil
}
