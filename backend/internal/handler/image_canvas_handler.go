package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type imageCanvasProjectService interface {
	List(context.Context, int64) ([]service.ImageCanvasProject, error)
	Create(context.Context, int64, string, string, json.RawMessage) (*service.ImageCanvasProject, error)
	Get(context.Context, int64, string) (*service.ImageCanvasProject, error)
	Update(context.Context, int64, string, int64, string, json.RawMessage) (*service.ImageCanvasProject, error)
	Delete(context.Context, int64, string) error
	UploadAsset(context.Context, service.ImageAssetUpload) (*service.ImageAsset, error)
	UploadAssetStream(context.Context, service.ImageAssetStreamUpload) (*service.ImageAsset, error)
	GetAssetObject(context.Context, int64, string, bool) (*service.ImageJobObject, error)
	OpenAssetObject(context.Context, int64, string, bool) (*service.ImageAssetObjectStream, error)
}

type imageCanvasJobService interface {
	Create(context.Context, service.ImageCanvasJobCreate) (*service.ImageJob, bool, error)
	GetOwned(context.Context, int64, string) (*service.ImageJob, error)
	CancelOwned(context.Context, int64, string) (*service.ImageJob, error)
	GetResult(context.Context, int64, string, int) (*service.ImageJobObject, error)
}

type imageCanvasPolicyService interface {
	Get(context.Context) (*service.ImageModelPolicy, error)
}

type imageCanvasMediaService interface {
	CreateVideo(context.Context, service.CanvasVideoTaskCreate) (*service.CanvasMediaTask, bool, error)
	GenerateAudio(context.Context, service.CanvasAudioTaskCreate) (*service.CanvasMediaTask, error)
	GetOwned(context.Context, int64, string) (*service.CanvasMediaTask, error)
	ListRecoverable(context.Context, int64, int64) ([]service.CanvasMediaTask, error)
	CancelOwned(context.Context, int64, string) (*service.CanvasMediaTask, error)
}

type ImageCanvasHandler struct {
	projects imageCanvasProjectService
	jobs     imageCanvasJobService
	policies imageCanvasPolicyService
	catalog  service.ImageModelCatalog
	media    imageCanvasMediaService
	editors  imageEditorService
}

func NewImageCanvasHandler(
	projects *service.ImageCanvasProjectService,
	jobs *service.ImageCanvasJobService,
	policies *service.ImageModelPolicyService,
	catalog service.ImageModelCatalog,
) *ImageCanvasHandler {
	return &ImageCanvasHandler{projects: projects, jobs: jobs, policies: policies, catalog: catalog}
}

func (h *ImageCanvasHandler) SetMediaService(media *service.CanvasMediaService) *ImageCanvasHandler {
	if h != nil {
		h.media = media
	}
	return h
}

type ImageCanvasConfigResponse struct {
	Enabled          bool                           `json:"enabled"`
	APIKeys          []service.ImageCanvasAPIKey    `json:"api_keys"`
	SelectedAPIKeyID *int64                         `json:"selected_api_key_id"`
	PolicyVersion    int64                          `json:"policy_version"`
	Models           []service.ImageModelPolicyItem `json:"models"`
}

type imageCanvasProjectWriteRequest struct {
	ID       string          `json:"id"`
	Version  int64           `json:"version"`
	Name     string          `json:"name"`
	Document json.RawMessage `json:"document"`
}

const maxImageCanvasProjectWriteRequestBytes = 9 << 20

type imageCanvasProjectResponse struct {
	ID         string                         `json:"id"`
	Name       string                         `json:"name"`
	Document   json.RawMessage                `json:"document"`
	Version    int64                          `json:"version"`
	CreatedAt  time.Time                      `json:"created_at"`
	UpdatedAt  time.Time                      `json:"updated_at"`
	OpenJobs   []imageCanvasJobResponse       `json:"open_jobs,omitempty"`
	MediaTasks []imageCanvasMediaTaskResponse `json:"media_tasks,omitempty"`
}

type imageCanvasAssetResponse struct {
	ID           string `json:"id"`
	SourceType   string `json:"source_type"`
	MediaKind    string `json:"media_kind"`
	FileName     string `json:"file_name,omitempty"`
	MIMEType     string `json:"mime_type"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	ByteSize     int64  `json:"byte_size"`
	SHA256       string `json:"sha256"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
}

type imageCanvasJobResultResponse struct {
	Index    int    `json:"index"`
	Status   string `json:"status"`
	AssetID  string `json:"asset_id,omitempty"`
	URL      string `json:"url,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Size     string `json:"size,omitempty"`
}

type imageCanvasJobResponse struct {
	ID              string                         `json:"id"`
	Status          service.ImageJobStatus         `json:"status"`
	Phase           string                         `json:"phase,omitempty"`
	Operation       string                         `json:"operation"`
	ClientNodeID    string                         `json:"client_node_id,omitempty"`
	SelectedModel   string                         `json:"selected_model,omitempty"`
	SuccessfulModel string                         `json:"successful_model,omitempty"`
	PolicyVersion   int64                          `json:"policy_version,omitempty"`
	AttemptPlan     []string                       `json:"attempt_plan"`
	AttemptPosition int                            `json:"attempt_position"`
	RequestedCount  int                            `json:"requested_count"`
	CompletedCount  int                            `json:"completed_count"`
	Results         []imageCanvasJobResultResponse `json:"results"`
	Error           *service.ImageJobError         `json:"error,omitempty"`
	CreatedAt       int64                          `json:"created_at"`
	UpdatedAt       int64                          `json:"updated_at"`
}

func (h *ImageCanvasHandler) GetConfig(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if h == nil || h.catalog == nil || h.policies == nil {
		writeImageCanvasError(c, service.ErrImageJobUnavailable)
		return
	}
	keys, err := h.catalog.ListOwnedAPIKeys(c.Request.Context(), userID)
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	policy, err := h.policies.Get(c.Request.Context())
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	result := ImageCanvasConfigResponse{
		Enabled: true, APIKeys: keys, PolicyVersion: policy.Version,
		Models: make([]service.ImageModelPolicyItem, 0),
	}
	if raw := strings.TrimSpace(c.Query("api_key_id")); raw != "" {
		apiKeyID, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || apiKeyID <= 0 {
			writeImageCanvasError(c, service.ErrImageCanvasAPIKeyNotFound)
			return
		}
		allowed, catalogErr := h.catalog.ForAPIKey(c.Request.Context(), userID, apiKeyID)
		if catalogErr != nil {
			writeImageCanvasError(c, catalogErr)
			return
		}
		result.SelectedAPIKeyID = &apiKeyID
		for _, item := range policy.Items {
			capability, available := allowed[item.Model]
			if !policy.Enabled || !item.Enabled || !available || (!capability.Generation && !capability.Edit) {
				continue
			}
			item.Capability = capability
			result.Models = append(result.Models, item)
		}
		sort.SliceStable(result.Models, func(i, j int) bool { return result.Models[i].Position < result.Models[j].Position })
	}
	response.Success(c, result)
}

func (h *ImageCanvasHandler) ListProjects(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	projects, err := h.projects.List(c.Request.Context(), userID)
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	items := make([]imageCanvasProjectResponse, 0, len(projects))
	for index := range projects {
		if h.media != nil {
			tasks, listErr := h.media.ListRecoverable(c.Request.Context(), userID, projects[index].ID)
			if listErr != nil {
				writeImageCanvasError(c, listErr)
				return
			}
			projects[index].MediaTasks = tasks
		}
		items = append(items, toImageCanvasProjectResponse(&projects[index]))
	}
	response.Success(c, gin.H{"items": items})
}

func (h *ImageCanvasHandler) CreateProject(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	var request imageCanvasProjectWriteRequest
	if err := bindImageCanvasProjectWriteRequest(c, &request); err != nil {
		writeImageCanvasError(c, service.ErrImageCanvasDocumentInvalid)
		return
	}
	project, err := h.projects.Create(c.Request.Context(), userID, request.ID, request.Name, request.Document)
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Created(c, toImageCanvasProjectResponse(project))
}

func (h *ImageCanvasHandler) GetProject(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	project, err := h.projects.Get(c.Request.Context(), userID, c.Param("project_id"))
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	if h.media != nil {
		project.MediaTasks, err = h.media.ListRecoverable(c.Request.Context(), userID, project.ID)
		if err != nil {
			writeImageCanvasError(c, err)
			return
		}
	}
	response.Success(c, toImageCanvasProjectResponse(project))
}

func (h *ImageCanvasHandler) UpdateProject(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	var request imageCanvasProjectWriteRequest
	if err := bindImageCanvasProjectWriteRequest(c, &request); err != nil {
		writeImageCanvasError(c, service.ErrImageCanvasDocumentInvalid)
		return
	}
	project, err := h.projects.Update(c.Request.Context(), userID, c.Param("project_id"), request.Version, request.Name, request.Document)
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Success(c, toImageCanvasProjectResponse(project))
}

func bindImageCanvasProjectWriteRequest(c *gin.Context, request *imageCanvasProjectWriteRequest) error {
	if c == nil || c.Request == nil || c.Writer == nil || request == nil {
		return service.ErrImageCanvasDocumentInvalid
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImageCanvasProjectWriteRequestBytes)
	return c.ShouldBindJSON(request)
}

func (h *ImageCanvasHandler) DeleteProject(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if err := h.projects.Delete(c.Request.Context(), userID, c.Param("project_id")); err != nil {
		writeImageCanvasError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *ImageCanvasHandler) UploadAsset(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (512<<20)+(1<<20))
	fileHeader, err := c.FormFile("file")
	if err != nil {
		writeImageCanvasError(c, service.ErrImageAssetInvalid)
		return
	}
	if fileHeader.Size <= 0 || fileHeader.Size > 512<<20 {
		writeImageCanvasError(c, service.ErrImageAssetInvalid)
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		writeImageCanvasError(c, service.ErrImageAssetInvalid)
		return
	}
	defer func() { _ = file.Close() }()
	width, widthErr := parseOptionalCanvasMediaInt(c.PostForm("width"))
	height, heightErr := parseOptionalCanvasMediaInt(c.PostForm("height"))
	durationMS, durationErr := parseOptionalCanvasMediaInt64(c.PostForm("duration_ms"))
	if widthErr != nil || heightErr != nil || durationErr != nil {
		writeImageCanvasError(c, service.ErrImageAssetInvalid)
		return
	}
	asset, err := h.projects.UploadAssetStream(c.Request.Context(), service.ImageAssetStreamUpload{
		UserID: userID, ProjectPublicID: strings.TrimSpace(c.PostForm("project_id")),
		FileName: fileHeader.Filename, MIMEType: fileHeader.Header.Get("Content-Type"),
		Width: width, Height: height, DurationMS: durationMS, ByteSize: fileHeader.Size, Reader: file,
	})
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Created(c, toImageCanvasAssetResponse(asset))
}

func (h *ImageCanvasHandler) DownloadAsset(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	object, err := h.projects.OpenAssetObject(c.Request.Context(), userID, c.Param("asset_id"), c.Query("thumbnail") == "true")
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	defer func() { _ = object.Reader.Close() }()
	contentType := strings.TrimSpace(object.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Cache-Control", "private, max-age=300")
	c.DataFromReader(http.StatusOK, object.Size, contentType, object.Reader, nil)
}

func (h *ImageCanvasHandler) CreateJob(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	var request service.ImageCanvasJobCreate
	if err := c.ShouldBindJSON(&request); err != nil {
		writeImageCanvasError(c, service.ErrImageJobInvalidRequest)
		return
	}
	request.UserID = userID
	request.IdempotencyKey = c.GetHeader("Idempotency-Key")
	job, _, err := h.jobs.Create(c.Request.Context(), request)
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Accepted(c, toImageCanvasJobResponse(job))
}

func (h *ImageCanvasHandler) GetJob(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	job, err := h.jobs.GetOwned(c.Request.Context(), userID, c.Param("job_id"))
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Success(c, toImageCanvasJobResponse(job))
}

func (h *ImageCanvasHandler) CancelJob(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	job, err := h.jobs.CancelOwned(c.Request.Context(), userID, c.Param("job_id"))
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Success(c, toImageCanvasJobResponse(job))
}

func (h *ImageCanvasHandler) DownloadJobResult(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 {
		writeImageCanvasError(c, service.ErrImageJobInvalidRequest)
		return
	}
	object, err := h.jobs.GetResult(c.Request.Context(), userID, c.Param("job_id"), index)
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	contentType := strings.TrimSpace(object.ContentType)
	if contentType == "" {
		contentType = http.DetectContentType(object.Data)
	}
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("Content-Length", strconv.Itoa(len(object.Data)))
	c.Data(http.StatusOK, contentType, object.Data)
}

func (h *ImageCanvasHandler) StreamJobEvents(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		writeImageCanvasError(c, service.ErrImageJobUnavailable)
		return
	}
	ctx := c.Request.Context()
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	lastSnapshot := ""
	first := true
	for {
		job, err := h.jobs.GetOwned(ctx, userID, c.Param("job_id"))
		if err != nil {
			if first {
				writeImageCanvasError(c, err)
			}
			return
		}
		encoded, _ := json.Marshal(toImageCanvasJobResponse(job))
		if snapshot := string(encoded); snapshot != lastSnapshot {
			event := "job"
			if first {
				event = "snapshot"
			}
			_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, encoded)
			flusher.Flush()
			lastSnapshot = snapshot
			first = false
		}
		if job.Status.Terminal() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			_, _ = io.WriteString(c.Writer, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func imageCanvasUserID(c *gin.Context) (int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.ErrorWithDetails(c, http.StatusUnauthorized, "User authentication required", "unauthorized", nil)
		return 0, false
	}
	return subject.UserID, true
}

func toImageCanvasAssetResponse(asset *service.ImageAsset) imageCanvasAssetResponse {
	if asset == nil {
		return imageCanvasAssetResponse{}
	}
	result := imageCanvasAssetResponse{
		ID: asset.PublicID, SourceType: asset.SourceType, MediaKind: asset.MediaKind,
		FileName: asset.FileName, MIMEType: asset.MIMEType, Width: asset.Width, Height: asset.Height,
		DurationMS: asset.DurationMS, ByteSize: asset.ByteSize, SHA256: asset.SHA256,
		URL: "/api/v1/image-canvas/assets/" + asset.PublicID,
	}
	if asset.ThumbnailObjectKey != "" {
		result.ThumbnailURL = result.URL + "?thumbnail=true"
	}
	return result
}

func parseOptionalCanvasMediaInt(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, service.ErrImageAssetInvalid
	}
	return value, nil
}

func parseOptionalCanvasMediaInt64(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, service.ErrImageAssetInvalid
	}
	return value, nil
}

func toImageCanvasJobResponse(job *service.ImageJob) imageCanvasJobResponse {
	if job == nil {
		return imageCanvasJobResponse{AttemptPlan: []string{}, Results: []imageCanvasJobResultResponse{}}
	}
	result := imageCanvasJobResponse{
		ID: job.PublicID, Status: job.Status, Phase: job.ExecutionPhase, Operation: job.Operation,
		ClientNodeID: job.ClientNodeID, SelectedModel: job.SelectedModel, SuccessfulModel: job.SuccessfulModel,
		PolicyVersion: job.PolicyVersion, AttemptPlan: append([]string(nil), job.AttemptPlan...),
		RequestedCount: job.RequestedCount, CompletedCount: job.CompletedCount,
		Error: job.Error, CreatedAt: job.CreatedAt.Unix(), UpdatedAt: job.UpdatedAt.Unix(),
		Results: make([]imageCanvasJobResultResponse, 0, len(job.Results)),
	}
	if len(result.AttemptPlan) == 0 {
		result.AttemptPlan = []string{job.Request.Model}
	}
	if len(job.AttemptLog) > 0 {
		result.AttemptPosition = job.AttemptLog[len(job.AttemptLog)-1].Position
	}
	for _, item := range job.Results {
		entry := imageCanvasJobResultResponse{
			Index: item.Index, Status: item.Status, AssetID: item.AssetPublicID,
			MIMEType: item.MIMEType, Size: item.SizeTier,
		}
		if item.Status == "completed" {
			entry.URL = fmt.Sprintf("/api/v1/image-canvas/jobs/%s/results/%d", job.PublicID, item.Index)
		}
		result.Results = append(result.Results, entry)
	}
	return result
}

func toImageCanvasProjectResponse(project *service.ImageCanvasProject) imageCanvasProjectResponse {
	if project == nil {
		return imageCanvasProjectResponse{}
	}
	result := imageCanvasProjectResponse{
		ID: project.PublicID, Name: project.Name, Document: project.Document, Version: project.Version,
		CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt,
	}
	if len(project.OpenJobs) > 0 {
		result.OpenJobs = make([]imageCanvasJobResponse, 0, len(project.OpenJobs))
		for index := range project.OpenJobs {
			result.OpenJobs = append(result.OpenJobs, toImageCanvasJobResponse(&project.OpenJobs[index]))
		}
	}
	if len(project.MediaTasks) > 0 {
		result.MediaTasks = make([]imageCanvasMediaTaskResponse, 0, len(project.MediaTasks))
		for index := range project.MediaTasks {
			result.MediaTasks = append(result.MediaTasks, toImageCanvasMediaTaskResponse(&project.MediaTasks[index]))
		}
	}
	return result
}

func writeImageCanvasError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrImageCanvasProjectNotFound),
		errors.Is(err, service.ErrImageAssetNotFound),
		errors.Is(err, service.ErrImageEditorDocumentNotFound),
		errors.Is(err, service.ErrImageCanvasAPIKeyNotFound),
		errors.Is(err, service.ErrImageJobNotFound),
		errors.Is(err, service.ErrCanvasMediaTaskNotFound):
		response.ErrorWithDetails(c, http.StatusNotFound, "Image canvas resource not found", "image_canvas_not_found", nil)
	case errors.Is(err, service.ErrImageCanvasProjectVersionConflict):
		response.ErrorWithDetails(c, http.StatusConflict, "Image canvas project was updated elsewhere", "project_version_conflict", nil)
	case errors.Is(err, service.ErrImageEditorDocumentVersionConflict):
		response.ErrorWithDetails(c, http.StatusConflict, "Image editor document was updated elsewhere", "editor_version_conflict", nil)
	case errors.Is(err, service.ErrImageEditorDocumentConflict):
		response.ErrorWithDetails(c, http.StatusConflict, "Image editor node is linked to another base asset", "editor_document_conflict", nil)
	case errors.Is(err, service.ErrImageJobIdempotencyConflict), errors.Is(err, service.ErrImageJobCancelConflict),
		errors.Is(err, service.ErrCanvasMediaTaskConflict), errors.Is(err, service.ErrCanvasMediaTaskIdempotencyConflict):
		response.ErrorWithDetails(c, http.StatusConflict, err.Error(), "image_canvas_job_conflict", nil)
	case errors.Is(err, service.ErrImageCanvasDocumentInvalid), errors.Is(err, service.ErrImageAssetInvalid),
		errors.Is(err, service.ErrImageEditorDocumentInvalid),
		errors.Is(err, service.ErrImageJobInvalidRequest), errors.Is(err, service.ErrCanvasMediaTaskInvalid):
		response.ErrorWithDetails(c, http.StatusBadRequest, err.Error(), "invalid_image_canvas_request", nil)
	case errors.Is(err, service.ErrImageModelSelectionInvalid), errors.Is(err, service.ErrNoCompatibleImageModel):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, err.Error(), "invalid_image_model_selection", nil)
	case errors.Is(err, service.ErrCanvasMediaCapabilityUnavailable):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, err.Error(), "invalid_media_model_selection", nil)
	case errors.Is(err, service.ErrImageCanvasModerationBlocked):
		response.ErrorWithDetails(c, http.StatusBadRequest, "Image request was blocked by content policy", "content_policy_violation", nil)
	case errors.Is(err, service.ErrImageJobReservationInsufficient):
		response.ErrorWithDetails(c, http.StatusPaymentRequired, "Insufficient quota for image generation", "insufficient_quota", nil)
	case errors.Is(err, service.ErrImageJobQueueFull):
		response.ErrorWithDetails(c, http.StatusTooManyRequests, "Too many active image jobs for this user", "image_job_queue_full", nil)
	case errors.Is(err, service.ErrImageJobDisabled), errors.Is(err, service.ErrImageJobUnavailable),
		errors.Is(err, service.ErrImageJobReservationUnavailable),
		errors.Is(err, service.ErrCanvasMediaUpstreamUnavailable):
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "Image job service is unavailable", "image_job_unavailable", nil)
	case errors.Is(err, service.ErrImageJobExpired):
		response.ErrorWithDetails(c, http.StatusGone, "Image job result has expired", "image_job_expired", nil)
	default:
		response.ErrorFrom(c, err)
	}
}
