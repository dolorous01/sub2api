package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type imageCanvasMediaServiceStub struct {
	createVideo     func(context.Context, service.CanvasVideoTaskCreate) (*service.CanvasMediaTask, bool, error)
	generateAudio   func(context.Context, service.CanvasAudioTaskCreate) (*service.CanvasMediaTask, error)
	getOwned        func(context.Context, int64, string) (*service.CanvasMediaTask, error)
	listRecoverable func(context.Context, int64, int64) ([]service.CanvasMediaTask, error)
	cancelOwned     func(context.Context, int64, string) (*service.CanvasMediaTask, error)
}

func (s *imageCanvasMediaServiceStub) CreateVideo(ctx context.Context, input service.CanvasVideoTaskCreate) (*service.CanvasMediaTask, bool, error) {
	if s.createVideo == nil {
		return nil, false, errors.New("unexpected CreateVideo call")
	}
	return s.createVideo(ctx, input)
}

func (s *imageCanvasMediaServiceStub) GenerateAudio(ctx context.Context, input service.CanvasAudioTaskCreate) (*service.CanvasMediaTask, error) {
	if s.generateAudio == nil {
		return nil, errors.New("unexpected GenerateAudio call")
	}
	return s.generateAudio(ctx, input)
}

func (s *imageCanvasMediaServiceStub) GetOwned(ctx context.Context, userID int64, publicID string) (*service.CanvasMediaTask, error) {
	if s.getOwned == nil {
		return nil, errors.New("unexpected GetOwned call")
	}
	return s.getOwned(ctx, userID, publicID)
}

func (s *imageCanvasMediaServiceStub) ListRecoverable(ctx context.Context, userID, projectID int64) ([]service.CanvasMediaTask, error) {
	if s.listRecoverable == nil {
		return nil, errors.New("unexpected ListRecoverable call")
	}
	return s.listRecoverable(ctx, userID, projectID)
}

func (s *imageCanvasMediaServiceStub) CancelOwned(ctx context.Context, userID int64, publicID string) (*service.CanvasMediaTask, error) {
	if s.cancelOwned == nil {
		return nil, errors.New("unexpected CancelOwned call")
	}
	return s.cancelOwned(ctx, userID, publicID)
}

func TestImageCanvasCreateVideoTaskForwardsIdempotencyAndMapsResponse(t *testing.T) {
	createdAt := time.Unix(1_700_000_000, 0)
	var captured service.CanvasVideoTaskCreate
	media := &imageCanvasMediaServiceStub{
		createVideo: func(_ context.Context, input service.CanvasVideoTaskCreate) (*service.CanvasMediaTask, bool, error) {
			captured = input
			return &service.CanvasMediaTask{
				PublicID: "media-video-1", Kind: service.CanvasMediaKindVideo,
				Status: service.ImageJobStatusQueued, Phase: "preflight",
				ProjectPublicID: "project-1", ClientNodeID: "node-1",
				SelectedModel: "grok-imagine-video", CreatedAt: createdAt, UpdatedAt: createdAt,
			}, true, nil
		},
	}
	router := imageCanvasMediaTestRouter(&ImageCanvasHandler{media: media})
	body := `{"api_key_id":12,"project_id":"project-1","client_node_id":"node-1","selected_model":"grok-imagine-video","prompt":"animate this","reference_asset_ids":["asset-1"],"parameters":{"seconds":10,"size":"16:9","resolution":"720p","generate_audio":true,"watermark":false}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/image-canvas/media/video/tasks", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "idem-video-1")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, int64(99), captured.UserID)
	require.Equal(t, int64(12), captured.APIKeyID)
	require.Equal(t, "idem-video-1", captured.IdempotencyKey)
	require.Equal(t, []string{"asset-1"}, captured.ReferenceAssetIDs)
	require.Equal(t, 10, captured.Parameters.Seconds)
	require.Equal(t, "media-video-1", gjson.Get(recorder.Body.String(), "data.id").String())
	require.Equal(t, "video", gjson.Get(recorder.Body.String(), "data.kind").String())
	require.Equal(t, "[]", gjson.Get(recorder.Body.String(), "data.results").Raw)
}

func TestImageCanvasGenerateAudioForwardsIdempotencyAndReturnsAsset(t *testing.T) {
	createdAt := time.Unix(1_700_000_000, 0)
	var captured service.CanvasAudioTaskCreate
	media := &imageCanvasMediaServiceStub{
		generateAudio: func(_ context.Context, input service.CanvasAudioTaskCreate) (*service.CanvasMediaTask, error) {
			captured = input
			return &service.CanvasMediaTask{
				PublicID: "media-audio-1", Kind: service.CanvasMediaKindAudio,
				Status: service.ImageJobStatusCompleted, Phase: "completed",
				ProjectPublicID: "project-1", ClientNodeID: "node-2",
				SelectedModel: "gpt-4o-mini-tts", SuccessfulModel: "gpt-4o-mini-tts",
				ResultAssetPublicID: "asset-audio-1", ResultMIMEType: "audio/mpeg",
				CreatedAt: createdAt, UpdatedAt: createdAt,
			}, nil
		},
	}
	router := imageCanvasMediaTestRouter(&ImageCanvasHandler{media: media})
	body := `{"api_key_id":12,"project_id":"project-1","client_node_id":"node-2","selected_model":"gpt-4o-mini-tts","prompt":"hello","parameters":{"voice":"alloy","format":"mp3","speed":1}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/image-canvas/media/audio", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "idem-audio-1")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, int64(99), captured.UserID)
	require.Equal(t, "idem-audio-1", captured.IdempotencyKey)
	require.Equal(t, "asset-audio-1", gjson.Get(recorder.Body.String(), "data.results.0.asset_id").String())
	require.Equal(t, "/api/v1/image-canvas/assets/asset-audio-1", gjson.Get(recorder.Body.String(), "data.results.0.url").String())
}

func TestImageCanvasMediaRoutesUseAuthenticatedOwner(t *testing.T) {
	var getUserID, cancelUserID int64
	var getID, cancelID string
	media := &imageCanvasMediaServiceStub{
		getOwned: func(_ context.Context, userID int64, publicID string) (*service.CanvasMediaTask, error) {
			getUserID, getID = userID, publicID
			return &service.CanvasMediaTask{PublicID: publicID, Kind: service.CanvasMediaKindVideo, Status: service.ImageJobStatusRunning}, nil
		},
		cancelOwned: func(_ context.Context, userID int64, publicID string) (*service.CanvasMediaTask, error) {
			cancelUserID, cancelID = userID, publicID
			return &service.CanvasMediaTask{PublicID: publicID, Kind: service.CanvasMediaKindVideo, Status: service.ImageJobStatusCanceled}, nil
		},
	}
	router := imageCanvasMediaTestRouter(&ImageCanvasHandler{media: media})

	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/image-canvas/media/tasks/media-1", nil))
	require.Equal(t, http.StatusOK, getRecorder.Code)

	cancelRecorder := httptest.NewRecorder()
	router.ServeHTTP(cancelRecorder, httptest.NewRequest(http.MethodDelete, "/api/v1/image-canvas/media/tasks/media-1", nil))
	require.Equal(t, http.StatusOK, cancelRecorder.Code)
	require.Equal(t, int64(99), getUserID)
	require.Equal(t, int64(99), cancelUserID)
	require.Equal(t, "media-1", getID)
	require.Equal(t, "media-1", cancelID)
}

func TestWriteImageCanvasMediaErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{name: "not found", err: service.ErrCanvasMediaTaskNotFound, status: http.StatusNotFound, reason: "image_canvas_not_found"},
		{name: "library not found", err: service.ErrImageCanvasLibraryItemNotFound, status: http.StatusNotFound, reason: "image_canvas_not_found"},
		{name: "invalid", err: service.ErrCanvasMediaTaskInvalid, status: http.StatusBadRequest, reason: "invalid_image_canvas_request"},
		{name: "library invalid", err: service.ErrImageCanvasLibraryItemInvalid, status: http.StatusBadRequest, reason: "invalid_image_canvas_request"},
		{name: "conflict", err: service.ErrCanvasMediaTaskIdempotencyConflict, status: http.StatusConflict, reason: "image_canvas_job_conflict"},
		{name: "library version conflict", err: service.ErrImageCanvasLibraryVersionConflict, status: http.StatusConflict, reason: "library_item_version_conflict"},
		{name: "library client conflict", err: service.ErrImageCanvasLibraryClientConflict, status: http.StatusConflict, reason: "library_item_client_conflict"},
		{name: "capability", err: service.ErrCanvasMediaCapabilityUnavailable, status: http.StatusUnprocessableEntity, reason: "invalid_media_model_selection"},
		{name: "reservation unavailable", err: service.ErrImageJobReservationUnavailable, status: http.StatusServiceUnavailable, reason: "image_job_unavailable"},
		{name: "upstream unavailable", err: service.ErrCanvasMediaUpstreamUnavailable, status: http.StatusServiceUnavailable, reason: "image_job_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			writeImageCanvasError(context, test.err)
			require.Equal(t, test.status, recorder.Code)
			require.Equal(t, test.reason, gjson.Get(recorder.Body.String(), "reason").String())
		})
	}
}

func TestImageCanvasProjectResponseIncludesRecoverableMediaTasks(t *testing.T) {
	project := &service.ImageCanvasProject{
		PublicID: "project-1",
		MediaTasks: []service.CanvasMediaTask{{
			PublicID: "media-1", Kind: service.CanvasMediaKindVideo,
			Status: service.ImageJobStatusRunning, ProjectPublicID: "project-1",
			ClientNodeID: "source", CreatedAt: time.Unix(1_700_000_000, 0),
			UpdatedAt: time.Unix(1_700_000_001, 0),
		}},
	}

	response := toImageCanvasProjectResponse(project)
	require.Len(t, response.MediaTasks, 1)
	require.Equal(t, "media-1", response.MediaTasks[0].ID)
	require.Equal(t, "source", response.MediaTasks[0].ClientNodeID)
}

func imageCanvasMediaTestRouter(handler *ImageCanvasHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(context *gin.Context) {
		context.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 99})
		context.Next()
	})
	router.POST("/api/v1/image-canvas/media/video/tasks", handler.CreateVideoTask)
	router.POST("/api/v1/image-canvas/media/audio", handler.GenerateAudio)
	router.GET("/api/v1/image-canvas/media/tasks/:task_id", handler.GetMediaTask)
	router.DELETE("/api/v1/image-canvas/media/tasks/:task_id", handler.CancelMediaTask)
	return router
}
