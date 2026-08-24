package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type canvasMediaTaskRepositoryStub struct {
	CanvasMediaTaskRepository
	create     func(context.Context, CanvasMediaTaskInsert) (*CanvasMediaTask, bool, error)
	renewLease func(context.Context, int64, string, time.Time) error
	fail       func(context.Context, int64, string, ImageJobStatus, CanvasMediaTaskError) error
}

func (r *canvasMediaTaskRepositoryStub) Create(ctx context.Context, input CanvasMediaTaskInsert) (*CanvasMediaTask, bool, error) {
	return r.create(ctx, input)
}

func (r *canvasMediaTaskRepositoryStub) RenewLease(ctx context.Context, id int64, owner string, leaseUntil time.Time) error {
	return r.renewLease(ctx, id, owner, leaseUntil)
}

func (r *canvasMediaTaskRepositoryStub) Fail(ctx context.Context, id int64, owner string, status ImageJobStatus, taskError CanvasMediaTaskError) error {
	return r.fail(ctx, id, owner, status, taskError)
}

func TestValidateCanvasMediaIdentity(t *testing.T) {
	if err := validateCanvasMediaIdentity(1, 2, "project", "node", "model", "prompt", "idempotency"); err != nil {
		t.Fatalf("validateCanvasMediaIdentity() error = %v", err)
	}

	invalid := []struct {
		name           string
		userID         int64
		apiKeyID       int64
		projectID      string
		nodeID         string
		model          string
		prompt         string
		idempotencyKey string
	}{
		{name: "missing user", apiKeyID: 2, projectID: "project", nodeID: "node", model: "model", prompt: "prompt", idempotencyKey: "key"},
		{name: "missing API key", userID: 1, projectID: "project", nodeID: "node", model: "model", prompt: "prompt", idempotencyKey: "key"},
		{name: "long node", userID: 1, apiKeyID: 2, projectID: "project", nodeID: strings.Repeat("n", 129), model: "model", prompt: "prompt", idempotencyKey: "key"},
		{name: "long prompt", userID: 1, apiKeyID: 2, projectID: "project", nodeID: "node", model: "model", prompt: strings.Repeat("p", (32<<10)+1), idempotencyKey: "key"},
		{name: "missing idempotency key", userID: 1, apiKeyID: 2, projectID: "project", nodeID: "node", model: "model", prompt: "prompt"},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			err := validateCanvasMediaIdentity(test.userID, test.apiKeyID, test.projectID, test.nodeID, test.model, test.prompt, test.idempotencyKey)
			if !errors.Is(err, ErrCanvasMediaTaskInvalid) {
				t.Fatalf("validateCanvasMediaIdentity() error = %v, want ErrCanvasMediaTaskInvalid", err)
			}
		})
	}
}

func TestValidateCanvasVideoParameters(t *testing.T) {
	capability := ImageModelCapability{
		VideoSeconds: []int{5, 10}, AspectRatios: []string{"4:3", "16:9"},
		Resolutions: []string{"720p", "1080p"},
		Defaults:    ImageModelDefaults{AspectRatio: " 4:3 ", Resolution: " 1080P "},
	}
	parameters := CanvasVideoParameters{}
	if err := validateCanvasVideoParameters(&parameters, capability); err != nil {
		t.Fatalf("validateCanvasVideoParameters() error = %v", err)
	}
	if parameters.Seconds != 5 || parameters.Size != "4:3" || parameters.Resolution != "1080p" {
		t.Fatalf("validateCanvasVideoParameters() defaults = %+v", parameters)
	}

	for _, test := range []CanvasVideoParameters{
		{Seconds: 7, Size: "4:3", Resolution: "720p"},
		{Seconds: 5, Size: "1:1", Resolution: "720p"},
		{Seconds: 5, Size: "4:3", Resolution: "4k"},
	} {
		if err := validateCanvasVideoParameters(&test, capability); !errors.Is(err, ErrCanvasMediaTaskInvalid) {
			t.Fatalf("validateCanvasVideoParameters(%+v) error = %v, want ErrCanvasMediaTaskInvalid", test, err)
		}
	}
}

func TestValidateCanvasAudioParameters(t *testing.T) {
	capability := ImageModelCapability{
		AudioVoices: []string{"alloy", "nova"}, AudioFormats: []string{"mp3", "wav"},
		AudioSpeedMin: 0.5, AudioSpeedMax: 2,
	}
	parameters := CanvasAudioParameters{}
	if err := validateCanvasAudioParameters(&parameters, capability); err != nil {
		t.Fatalf("validateCanvasAudioParameters() error = %v", err)
	}
	if parameters.Voice != "alloy" || parameters.Format != "mp3" || parameters.Speed != 1 {
		t.Fatalf("validateCanvasAudioParameters() defaults = %+v", parameters)
	}

	normalized := CanvasAudioParameters{Voice: " NOVA ", Format: " WAV ", Speed: 1.5, Instructions: " Speak clearly "}
	if err := validateCanvasAudioParameters(&normalized, capability); err != nil {
		t.Fatalf("validateCanvasAudioParameters() normalized error = %v", err)
	}
	if normalized.Voice != "nova" || normalized.Format != "wav" || normalized.Instructions != "Speak clearly" {
		t.Fatalf("validateCanvasAudioParameters() normalized = %+v", normalized)
	}

	for _, test := range []CanvasAudioParameters{
		{Voice: "unknown", Format: "mp3", Speed: 1},
		{Voice: "alloy", Format: "flac", Speed: 1},
		{Voice: "alloy", Format: "mp3", Speed: 0.49},
		{Voice: "alloy", Format: "mp3", Speed: 2.01},
		{Voice: "alloy", Format: "mp3", Speed: 1, Instructions: strings.Repeat("i", 4097)},
	} {
		if err := validateCanvasAudioParameters(&test, capability); !errors.Is(err, ErrCanvasMediaTaskInvalid) {
			t.Fatalf("validateCanvasAudioParameters(%+v) error = %v, want ErrCanvasMediaTaskInvalid", test, err)
		}
	}
}

func TestMaintainVideoLeaseCancelsWorkerWhenLeaseIsLost(t *testing.T) {
	renewed := make(chan struct{}, 1)
	repository := &canvasMediaTaskRepositoryStub{
		renewLease: func(context.Context, int64, string, time.Time) error {
			renewed <- struct{}{}
			return ErrCanvasMediaTaskConflict
		},
	}
	service := &CanvasMediaService{tasks: repository, workerID: "worker-1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go service.maintainVideoLeaseEvery(ctx, 42, cancel, done, time.Millisecond)

	select {
	case <-renewed:
	case <-time.After(time.Second):
		t.Fatal("lease was not renewed")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("worker context was not canceled after losing the lease")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lease heartbeat did not stop")
	}
}

func TestCanvasMediaIdempotencyHashIncludesAPIKey(t *testing.T) {
	var hashes []string
	repository := &canvasMediaTaskRepositoryStub{
		create: func(_ context.Context, input CanvasMediaTaskInsert) (*CanvasMediaTask, bool, error) {
			hashes = append(hashes, input.RequestHash)
			return &CanvasMediaTask{RequestHash: input.RequestHash}, true, nil
		},
	}
	service := &CanvasMediaService{tasks: repository}
	for _, apiKeyID := range []int64{11, 12} {
		validated := &canvasMediaValidatedRequest{
			project: &ImageCanvasProject{PublicID: "project-1"},
			apiKey:  &APIKey{ID: apiKeyID, UserID: 99},
		}
		if _, _, err := service.insertTask(
			context.Background(), validated, CanvasMediaKindVideo, "node-1", "model-1",
			"prompt", []byte(`{"seconds":5}`), "idem-1",
		); err != nil {
			t.Fatalf("insertTask() error = %v", err)
		}
	}
	if len(hashes) != 2 || hashes[0] == hashes[1] {
		t.Fatalf("insertTask() hashes = %v, want API-key-specific hashes", hashes)
	}
}

func TestProcessVideoTaskDoesNotResubmitIndeterminateRequest(t *testing.T) {
	var gotStatus ImageJobStatus
	var gotError CanvasMediaTaskError
	repository := &canvasMediaTaskRepositoryStub{
		fail: func(_ context.Context, _ int64, _ string, status ImageJobStatus, taskError CanvasMediaTaskError) error {
			gotStatus, gotError = status, taskError
			return nil
		},
	}
	service := &CanvasMediaService{tasks: repository, workerID: "worker-1"}
	task := &CanvasMediaTask{ID: 42, AttemptCount: 2, CreatedAt: time.Now()}

	if err := service.processVideoTask(context.Background(), task); err != nil {
		t.Fatalf("processVideoTask() error = %v", err)
	}
	if gotStatus != ImageJobStatusIndeterminate || gotError.Code != "submission_indeterminate" || gotError.Retryable {
		t.Fatalf("processVideoTask() failure = status %q, error %+v", gotStatus, gotError)
	}
}

func TestProcessAudioTaskDoesNotRepeatIndeterminateExecution(t *testing.T) {
	var gotStatus ImageJobStatus
	var gotError CanvasMediaTaskError
	repository := &canvasMediaTaskRepositoryStub{
		fail: func(_ context.Context, _ int64, _ string, status ImageJobStatus, taskError CanvasMediaTaskError) error {
			gotStatus, gotError = status, taskError
			return nil
		},
	}
	service := &CanvasMediaService{tasks: repository, workerID: "worker-1"}
	task := &CanvasMediaTask{ID: 42, AttemptCount: 2}

	if err := service.processAudioTask(context.Background(), task); err != nil {
		t.Fatalf("processAudioTask() error = %v", err)
	}
	if gotStatus != ImageJobStatusIndeterminate || gotError.Code != "audio_execution_indeterminate" || gotError.Retryable {
		t.Fatalf("processAudioTask() failure = status %q, error %+v", gotStatus, gotError)
	}
}
