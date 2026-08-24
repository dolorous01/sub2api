package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type imageCanvasVideoTaskRequest struct {
	APIKeyID          int64                          `json:"api_key_id"`
	ProjectID         string                         `json:"project_id"`
	ClientNodeID      string                         `json:"client_node_id"`
	SelectedModel     string                         `json:"selected_model"`
	Prompt            string                         `json:"prompt"`
	ReferenceAssetIDs []string                       `json:"reference_asset_ids"`
	Parameters        imageCanvasVideoTaskParameters `json:"parameters"`
}

type imageCanvasVideoTaskParameters struct {
	Seconds       int    `json:"seconds"`
	Size          string `json:"size"`
	Resolution    string `json:"resolution"`
	GenerateAudio bool   `json:"generate_audio"`
	Watermark     bool   `json:"watermark"`
}

type imageCanvasAudioTaskRequest struct {
	APIKeyID      int64                          `json:"api_key_id"`
	ProjectID     string                         `json:"project_id"`
	ClientNodeID  string                         `json:"client_node_id"`
	SelectedModel string                         `json:"selected_model"`
	Prompt        string                         `json:"prompt"`
	Parameters    imageCanvasAudioTaskParameters `json:"parameters"`
}

type imageCanvasAudioTaskParameters struct {
	Voice        string  `json:"voice"`
	Format       string  `json:"format"`
	Speed        float64 `json:"speed"`
	Instructions string  `json:"instructions"`
}

type imageCanvasMediaTaskResultResponse struct {
	Index    int    `json:"index"`
	AssetID  string `json:"asset_id,omitempty"`
	URL      string `json:"url,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

type imageCanvasMediaTaskResponse struct {
	ID              string                               `json:"id"`
	Kind            service.CanvasMediaKind              `json:"kind"`
	Status          service.ImageJobStatus               `json:"status"`
	Phase           string                               `json:"phase"`
	ProjectID       string                               `json:"project_id"`
	ClientNodeID    string                               `json:"client_node_id"`
	SelectedModel   string                               `json:"selected_model"`
	SuccessfulModel string                               `json:"successful_model,omitempty"`
	Results         []imageCanvasMediaTaskResultResponse `json:"results"`
	Error           *service.CanvasMediaTaskError        `json:"error,omitempty"`
	CreatedAt       int64                                `json:"created_at"`
	UpdatedAt       int64                                `json:"updated_at"`
}

func (h *ImageCanvasHandler) CreateVideoTask(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if h == nil || h.media == nil {
		writeImageCanvasError(c, service.ErrCanvasMediaUpstreamUnavailable)
		return
	}
	var request imageCanvasVideoTaskRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeImageCanvasError(c, service.ErrCanvasMediaTaskInvalid)
		return
	}
	task, _, err := h.media.CreateVideo(c.Request.Context(), service.CanvasVideoTaskCreate{
		UserID: userID, APIKeyID: request.APIKeyID,
		ProjectPublicID: request.ProjectID, ClientNodeID: request.ClientNodeID,
		SelectedModel: request.SelectedModel, Prompt: request.Prompt,
		ReferenceAssetIDs: request.ReferenceAssetIDs,
		Parameters: service.CanvasVideoParameters{
			Seconds: request.Parameters.Seconds, Size: request.Parameters.Size,
			Resolution:    request.Parameters.Resolution,
			GenerateAudio: request.Parameters.GenerateAudio, Watermark: request.Parameters.Watermark,
		},
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Accepted(c, toImageCanvasMediaTaskResponse(task))
}

func (h *ImageCanvasHandler) GenerateAudio(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if h == nil || h.media == nil {
		writeImageCanvasError(c, service.ErrCanvasMediaUpstreamUnavailable)
		return
	}
	var request imageCanvasAudioTaskRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeImageCanvasError(c, service.ErrCanvasMediaTaskInvalid)
		return
	}
	task, err := h.media.GenerateAudio(c.Request.Context(), service.CanvasAudioTaskCreate{
		UserID: userID, APIKeyID: request.APIKeyID,
		ProjectPublicID: request.ProjectID, ClientNodeID: request.ClientNodeID,
		SelectedModel: request.SelectedModel, Prompt: request.Prompt,
		Parameters: service.CanvasAudioParameters{
			Voice: request.Parameters.Voice, Format: request.Parameters.Format,
			Speed: request.Parameters.Speed, Instructions: request.Parameters.Instructions,
		},
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Accepted(c, toImageCanvasMediaTaskResponse(task))
}

func (h *ImageCanvasHandler) GetMediaTask(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if h == nil || h.media == nil {
		writeImageCanvasError(c, service.ErrCanvasMediaUpstreamUnavailable)
		return
	}
	task, err := h.media.GetOwned(c.Request.Context(), userID, c.Param("task_id"))
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Success(c, toImageCanvasMediaTaskResponse(task))
}

func (h *ImageCanvasHandler) CancelMediaTask(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if h == nil || h.media == nil {
		writeImageCanvasError(c, service.ErrCanvasMediaUpstreamUnavailable)
		return
	}
	task, err := h.media.CancelOwned(c.Request.Context(), userID, c.Param("task_id"))
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Success(c, toImageCanvasMediaTaskResponse(task))
}

func toImageCanvasMediaTaskResponse(task *service.CanvasMediaTask) imageCanvasMediaTaskResponse {
	if task == nil {
		return imageCanvasMediaTaskResponse{Results: []imageCanvasMediaTaskResultResponse{}}
	}
	result := imageCanvasMediaTaskResponse{
		ID: task.PublicID, Kind: task.Kind, Status: task.Status, Phase: task.Phase,
		ProjectID: task.ProjectPublicID, ClientNodeID: task.ClientNodeID,
		SelectedModel: task.SelectedModel, SuccessfulModel: task.SuccessfulModel,
		Results: []imageCanvasMediaTaskResultResponse{}, Error: task.Error,
		CreatedAt: task.CreatedAt.Unix(), UpdatedAt: task.UpdatedAt.Unix(),
	}
	if task.ResultAssetPublicID != "" {
		result.Results = append(result.Results, imageCanvasMediaTaskResultResponse{
			Index: 0, AssetID: task.ResultAssetPublicID,
			URL:      "/api/v1/image-canvas/assets/" + task.ResultAssetPublicID,
			MIMEType: strings.TrimSpace(task.ResultMIMEType),
		})
	}
	return result
}
