package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const maxImageCanvasLibraryWriteBytes = (1 << 20) + (128 << 10)

type imageCanvasLibraryItemWriteRequest struct {
	ClientID string          `json:"client_id"`
	Version  int64           `json:"version"`
	Kind     string          `json:"kind"`
	AssetID  string          `json:"asset_id"`
	Title    string          `json:"title"`
	Content  string          `json:"content"`
	Tags     []string        `json:"tags"`
	Source   string          `json:"source"`
	Note     string          `json:"note"`
	Metadata json.RawMessage `json:"metadata"`
}

type imageCanvasLibraryItemResponse struct {
	ID           string          `json:"id"`
	ClientID     string          `json:"client_id"`
	Kind         string          `json:"kind"`
	AssetID      string          `json:"asset_id,omitempty"`
	AssetURL     string          `json:"asset_url,omitempty"`
	ThumbnailURL string          `json:"thumbnail_url,omitempty"`
	Title        string          `json:"title"`
	Content      string          `json:"content,omitempty"`
	Tags         []string        `json:"tags"`
	Source       string          `json:"source,omitempty"`
	Note         string          `json:"note,omitempty"`
	Metadata     json.RawMessage `json:"metadata"`
	Version      int64           `json:"version"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

func (h *ImageCanvasHandler) ListLibraryItems(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	items, err := h.projects.ListLibraryItems(c.Request.Context(), userID)
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	result := make([]imageCanvasLibraryItemResponse, 0, len(items))
	for index := range items {
		result = append(result, toImageCanvasLibraryItemResponse(&items[index]))
	}
	response.Success(c, gin.H{"items": result})
}

func (h *ImageCanvasHandler) CreateLibraryItem(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	request, ok := bindImageCanvasLibraryItem(c)
	if !ok {
		return
	}
	item, created, err := h.projects.CreateLibraryItem(c.Request.Context(), userID, request.libraryWrite())
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	result := toImageCanvasLibraryItemResponse(item)
	if created {
		response.Created(c, result)
		return
	}
	response.Success(c, result)
}

func (h *ImageCanvasHandler) UpdateLibraryItem(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	request, ok := bindImageCanvasLibraryItem(c)
	if !ok {
		return
	}
	item, err := h.projects.UpdateLibraryItem(c.Request.Context(), userID, c.Param("item_id"), request.libraryWrite())
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Success(c, toImageCanvasLibraryItemResponse(item))
}

func (h *ImageCanvasHandler) DeleteLibraryItem(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if err := h.projects.DeleteLibraryItem(c.Request.Context(), userID, c.Param("item_id")); err != nil {
		writeImageCanvasError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func bindImageCanvasLibraryItem(c *gin.Context) (imageCanvasLibraryItemWriteRequest, bool) {
	var request imageCanvasLibraryItemWriteRequest
	if c == nil || c.Request == nil || c.Writer == nil {
		return request, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImageCanvasLibraryWriteBytes)
	if err := c.ShouldBindJSON(&request); err != nil {
		writeImageCanvasError(c, service.ErrImageCanvasLibraryItemInvalid)
		return request, false
	}
	return request, true
}

func (request imageCanvasLibraryItemWriteRequest) libraryWrite() service.ImageCanvasLibraryItemWrite {
	return service.ImageCanvasLibraryItemWrite{
		ClientID:      request.ClientID,
		Version:       request.Version,
		Kind:          request.Kind,
		AssetPublicID: request.AssetID,
		Title:         request.Title,
		Content:       request.Content,
		Tags:          request.Tags,
		Source:        request.Source,
		Note:          request.Note,
		Metadata:      request.Metadata,
	}
}

func toImageCanvasLibraryItemResponse(item *service.ImageCanvasLibraryItem) imageCanvasLibraryItemResponse {
	if item == nil {
		return imageCanvasLibraryItemResponse{Tags: []string{}, Metadata: json.RawMessage(`{}`)}
	}
	result := imageCanvasLibraryItemResponse{
		ID:        item.PublicID,
		ClientID:  item.ClientID,
		Kind:      item.Kind,
		AssetID:   item.AssetPublicID,
		Title:     item.Title,
		Content:   item.Content,
		Tags:      item.Tags,
		Source:    item.Source,
		Note:      item.Note,
		Metadata:  item.Metadata,
		Version:   item.Version,
		CreatedAt: item.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		UpdatedAt: item.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if result.Tags == nil {
		result.Tags = []string{}
	}
	if len(result.Metadata) == 0 {
		result.Metadata = json.RawMessage(`{}`)
	}
	if strings.TrimSpace(item.AssetPublicID) != "" {
		result.AssetURL = "/api/v1/image-canvas/assets/" + item.AssetPublicID
		if item.Kind == "image" {
			result.ThumbnailURL = result.AssetURL + "?thumbnail=true"
		}
	}
	return result
}
