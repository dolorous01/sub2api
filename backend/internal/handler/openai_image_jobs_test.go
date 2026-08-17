package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImagesRespondAsyncReturnsImmediately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobs := &fakeImageJobHandlerService{job: &service.ImageJob{PublicID: "imgjob_test", Status: service.ImageJobStatusQueued}}
	h := newImageJobHandlerTestHandler(t, jobs)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader([]byte(`{"model":"gpt-image-2","prompt":"city","n":4}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "wait=10, respond-async")
	req.Header.Set("Idempotency-Key", "request-key")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	setImageJobHandlerAuth(c)

	h.Images(c)

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "respond-async", w.Header().Get("Preference-Applied"))
	require.Equal(t, "/v1/images/jobs/imgjob_test", w.Header().Get("Location"))
	require.Equal(t, 1, jobs.createCalls)
	require.Equal(t, 4, jobs.lastInput.Parsed.N)
	require.Equal(t, "batch", jobs.lastInput.Mode)
	require.Equal(t, "request-key", jobs.lastInput.IdempotencyKey)
}

func TestOpenAIImagesWithoutPreferKeepsSynchronousPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobs := &fakeImageJobHandlerService{job: &service.ImageJob{PublicID: "imgjob_test", Status: service.ImageJobStatusQueued}}
	h := newImageJobHandlerTestHandler(t, jobs)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader([]byte(`{"model":"gpt-image-2","prompt":"city"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	setImageJobHandlerAuth(c)

	h.Images(c)

	require.Zero(t, jobs.createCalls)
	require.NotEqual(t, http.StatusAccepted, w.Code)
}

func TestParseImageSequenceRequestJSON(t *testing.T) {
	parsed, scenes, err := parseImageSequenceRequest([]byte(`{"model":"gpt-image-2","prompt":"story","scenes":["arrival","storm","home"]}`), "application/json")
	require.NoError(t, err)
	require.Equal(t, []string{"arrival", "storm", "home"}, scenes)
	require.Equal(t, "/v1/images/generations", parsed.Endpoint)
	require.Equal(t, 1, parsed.N)
	require.False(t, parsed.Stream)
}

func TestParseImageSequenceRequestMultipartScenesAndImages(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "story"))
	require.NoError(t, writer.WriteField("scene", "one"))
	require.NoError(t, writer.WriteField("scene", "two"))
	part, err := writer.CreateFormFile("image[]", "reference.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("png"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	parsed, scenes, err := parseImageSequenceRequest(body.Bytes(), writer.FormDataContentType())
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two"}, scenes)
	require.Equal(t, "/v1/images/edits", parsed.Endpoint)
	require.Len(t, parsed.Uploads, 1)
}

func TestParseImageSequenceRequestRejectsInvalidSceneCount(t *testing.T) {
	_, _, err := parseImageSequenceRequest([]byte(`{"model":"gpt-image-2","scenes":["only-one"]}`), "application/json")
	require.Error(t, err)
}

func TestImageSequenceReservesOneInputSlotForPreviousFrame(t *testing.T) {
	for _, tc := range []struct {
		name       string
		images     string
		wantStatus int
		wantCreate int
	}{
		{
			name:       "three references fit with previous frame",
			images:     `[{"image_url":"https://example.com/1.png"},{"image_url":"https://example.com/2.png"},{"image_url":"https://example.com/3.png"}]`,
			wantStatus: http.StatusAccepted,
			wantCreate: 1,
		},
		{
			name:       "four references leave no previous frame slot",
			images:     `[{"image_url":"https://example.com/1.png"},{"image_url":"https://example.com/2.png"},{"image_url":"https://example.com/3.png"},{"image_url":"https://example.com/4.png"}]`,
			wantStatus: http.StatusBadRequest,
			wantCreate: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs := &fakeImageJobHandlerService{job: &service.ImageJob{PublicID: "imgjob_test", Status: service.ImageJobStatusQueued}}
			h := newImageJobHandlerTestHandler(t, jobs)
			body := fmt.Sprintf(`{"model":"gpt-image-2","prompt":"story","scenes":["one","two"],"images":%s}`, tc.images)
			req := httptest.NewRequest(http.MethodPost, "/v1/images/sequences", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req
			setImageJobHandlerAuth(c)

			h.ImageSequence(c)

			require.Equal(t, tc.wantStatus, w.Code)
			require.Equal(t, tc.wantCreate, jobs.createCalls)
		})
	}
}

func TestImageJobHandlersEnforceOwnershipAndServeResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobs := &fakeImageJobHandlerService{
		job:    &service.ImageJob{PublicID: "imgjob_test", Status: service.ImageJobStatusCompleted},
		result: &service.ImageJobObject{Data: []byte("png"), ContentType: "image/png", Size: 3},
	}
	h := newImageJobHandlerTestHandler(t, jobs)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/jobs/imgjob_test", nil)
	c.Params = gin.Params{{Key: "job_id", Value: "imgjob_test"}}
	setImageJobHandlerAuth(c)
	h.GetImageJob(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(20), jobs.lastAPIKeyID)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/jobs/imgjob_test/results/0", nil)
	c.Params = gin.Params{{Key: "job_id", Value: "imgjob_test"}, {Key: "index", Value: "0"}}
	setImageJobHandlerAuth(c)
	h.GetImageJobResult(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "image/png", w.Header().Get("Content-Type"))
	require.Equal(t, "private, max-age=300", w.Header().Get("Cache-Control"))
	require.Equal(t, "inline", w.Header().Get("Content-Disposition"))
	require.Equal(t, "png", w.Body.String())
	require.Equal(t, int64(20), jobs.lastAPIKeyID)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v1/images/jobs/imgjob_test", nil)
	c.Params = gin.Params{{Key: "job_id", Value: "imgjob_test"}}
	setImageJobHandlerAuth(c)
	h.CancelImageJob(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(20), jobs.lastAPIKeyID)
}

func TestImageJobResultMapsMissingAndExpiredErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "missing", err: service.ErrImageJobNotFound, status: http.StatusNotFound},
		{name: "expired", err: service.ErrImageJobExpired, status: http.StatusGone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs := &fakeImageJobHandlerService{resultErr: tc.err}
			h := newImageJobHandlerTestHandler(t, jobs)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/jobs/imgjob_test/results/0", nil)
			c.Params = gin.Params{{Key: "job_id", Value: "imgjob_test"}, {Key: "index", Value: "0"}}
			setImageJobHandlerAuth(c)

			h.GetImageJobResult(c)

			require.Equal(t, tc.status, w.Code)
		})
	}
}

type fakeImageJobHandlerService struct {
	createCalls  int
	lastInput    service.CreateImageJobInput
	job          *service.ImageJob
	createErr    error
	result       *service.ImageJobObject
	resultErr    error
	lastAPIKeyID int64
}

func (f *fakeImageJobHandlerService) Create(_ context.Context, input service.CreateImageJobInput) (*service.ImageJob, bool, error) {
	f.createCalls++
	f.lastInput = input
	return f.job, false, f.createErr
}

func (f *fakeImageJobHandlerService) GetOwned(_ context.Context, _ string, apiKeyID int64) (*service.ImageJob, error) {
	f.lastAPIKeyID = apiKeyID
	return f.job, nil
}

func (f *fakeImageJobHandlerService) GetAdmin(context.Context, string) (*service.ImageJob, error) {
	return f.job, nil
}

func (f *fakeImageJobHandlerService) CancelOwned(_ context.Context, _ string, apiKeyID int64) (*service.ImageJob, error) {
	f.lastAPIKeyID = apiKeyID
	return f.job, nil
}

func (f *fakeImageJobHandlerService) CancelAdmin(context.Context, string) (*service.ImageJob, error) {
	return f.job, nil
}

func (f *fakeImageJobHandlerService) GetOwnedResult(_ context.Context, _ string, apiKeyID int64, _ int) (*service.ImageJobObject, error) {
	f.lastAPIKeyID = apiKeyID
	return f.result, f.resultErr
}

func (f *fakeImageJobHandlerService) GetAdminResult(context.Context, string, int) (*service.ImageJobObject, error) {
	return f.result, nil
}

func newImageJobHandlerTestHandler(t *testing.T, jobs imageJobService) *OpenAIGatewayHandler {
	t.Helper()
	cfg := &config.Config{}
	cfg.RunMode = config.RunModeSimple
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	return &OpenAIGatewayHandler{
		gatewayService:      &service.OpenAIGatewayService{},
		billingCacheService: billing,
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   &ConcurrencyHelper{concurrencyService: service.NewConcurrencyService(nil)},
		imageJobs:           jobs,
		cfg:                 cfg,
	}
}

func setImageJobHandlerAuth(c *gin.Context) {
	groupID := int64(30)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID: 20, UserID: 10, GroupID: &groupID,
		User:  &service.User{ID: 10},
		Group: &service.Group{ID: groupID, AllowImageGeneration: true},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 10, Concurrency: 1})
}

func TestImageJobResponseJSONContract(t *testing.T) {
	response := service.ImageJobResponse{ID: "imgjob_test", Object: "image.job", Status: service.ImageJobStatusQueued}
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"object":"image.job"`)
}
