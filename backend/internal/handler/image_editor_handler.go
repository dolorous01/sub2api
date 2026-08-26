package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	maxImageEditorWriteRequestBytes = 128 << 10
	maxImageEditorDerivedAssetBytes = 64 << 20
)

type imageEditorService interface {
	CreateOrGet(context.Context, int64, string, string, string, json.RawMessage, []service.ImageEditorAssetReference) (*service.ImageEditorDocument, bool, error)
	Get(context.Context, int64, string) (*service.ImageEditorDocument, error)
	Update(context.Context, service.ImageEditorDocumentUpdate) (*service.ImageEditorDocument, error)
	UploadDerivedAssetStream(context.Context, service.ImageEditorDerivedAssetUpload) (*service.ImageAsset, error)
}

type imageEditorDocumentCreateRequest struct {
	ProjectID       string                              `json:"project_id"`
	NodeID          string                              `json:"node_id"`
	BaseAssetID     string                              `json:"base_asset_id"`
	Document        json.RawMessage                     `json:"document"`
	AssetReferences []service.ImageEditorAssetReference `json:"asset_references,omitempty"`
}

type imageEditorDocumentUpdateRequest struct {
	Version         int64                               `json:"version"`
	Document        json.RawMessage                     `json:"document"`
	CurrentAssetID  string                              `json:"current_asset_id,omitempty"`
	Operation       string                              `json:"operation,omitempty"`
	Parameters      json.RawMessage                     `json:"parameters,omitempty"`
	AssetReferences []service.ImageEditorAssetReference `json:"asset_references,omitempty"`
}

type imageEditorRevisionResponse struct {
	ID         string                   `json:"id"`
	Version    int64                    `json:"version"`
	Asset      imageCanvasAssetResponse `json:"asset"`
	Operation  string                   `json:"operation"`
	Parameters json.RawMessage          `json:"parameters"`
	CreatedAt  time.Time                `json:"created_at"`
}

type imageEditorDocumentResponse struct {
	ID              string                              `json:"id"`
	ProjectID       string                              `json:"project_id"`
	NodeID          string                              `json:"node_id"`
	BaseAsset       imageCanvasAssetResponse            `json:"base_asset"`
	CurrentAsset    imageCanvasAssetResponse            `json:"current_asset"`
	Document        json.RawMessage                     `json:"document"`
	Version         int64                               `json:"version"`
	AssetReferences []service.ImageEditorAssetReference `json:"asset_references"`
	Revisions       []imageEditorRevisionResponse       `json:"revisions"`
	CreatedAt       time.Time                           `json:"created_at"`
	UpdatedAt       time.Time                           `json:"updated_at"`
}

func (h *ImageCanvasHandler) SetEditorService(editor *service.ImageEditorService) *ImageCanvasHandler {
	if h != nil {
		h.editors = editor
	}
	return h
}

func (h *ImageCanvasHandler) CreateEditorDocument(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if h == nil || h.editors == nil {
		writeImageCanvasError(c, service.ErrImageJobUnavailable)
		return
	}
	var request imageEditorDocumentCreateRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImageEditorWriteRequestBytes)
	if err := c.ShouldBindJSON(&request); err != nil {
		writeImageCanvasError(c, service.ErrImageEditorDocumentInvalid)
		return
	}
	document, created, err := h.editors.CreateOrGet(
		c.Request.Context(), userID, request.ProjectID, request.NodeID,
		request.BaseAssetID, request.Document, request.AssetReferences,
	)
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	result := toImageEditorDocumentResponse(document)
	if created {
		response.Created(c, result)
		return
	}
	response.Success(c, result)
}

func (h *ImageCanvasHandler) GetEditorDocument(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if h == nil || h.editors == nil {
		writeImageCanvasError(c, service.ErrImageJobUnavailable)
		return
	}
	document, err := h.editors.Get(c.Request.Context(), userID, c.Param("document_id"))
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Success(c, toImageEditorDocumentResponse(document))
}

func (h *ImageCanvasHandler) UpdateEditorDocument(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if h == nil || h.editors == nil {
		writeImageCanvasError(c, service.ErrImageJobUnavailable)
		return
	}
	var request imageEditorDocumentUpdateRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImageEditorWriteRequestBytes)
	if err := c.ShouldBindJSON(&request); err != nil {
		writeImageCanvasError(c, service.ErrImageEditorDocumentInvalid)
		return
	}
	document, err := h.editors.Update(c.Request.Context(), service.ImageEditorDocumentUpdate{
		UserID: userID, PublicID: c.Param("document_id"), Version: request.Version,
		Document: request.Document, CurrentAssetPublicID: request.CurrentAssetID,
		Operation: request.Operation, Parameters: request.Parameters,
		AssetReferences: request.AssetReferences,
	})
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Success(c, toImageEditorDocumentResponse(document))
}

func (h *ImageCanvasHandler) UploadEditorDerivedAsset(c *gin.Context) {
	userID, ok := imageCanvasUserID(c)
	if !ok {
		return
	}
	if h == nil || h.editors == nil {
		writeImageCanvasError(c, service.ErrImageJobUnavailable)
		return
	}
	c.Request.Body = http.MaxBytesReader(
		c.Writer, c.Request.Body, maxImageEditorDerivedAssetBytes+(1<<20),
	)
	fileHeader, err := c.FormFile("file")
	if err != nil || fileHeader.Size <= 0 || fileHeader.Size > maxImageEditorDerivedAssetBytes {
		writeImageCanvasError(c, service.ErrImageAssetInvalid)
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		writeImageCanvasError(c, service.ErrImageAssetInvalid)
		return
	}
	defer func() { _ = file.Close() }()

	asset, err := h.editors.UploadDerivedAssetStream(
		c.Request.Context(),
		service.ImageEditorDerivedAssetUpload{
			UserID: userID, DocumentPublicID: c.Param("document_id"),
			ParentAssetPublicID: strings.TrimSpace(c.PostForm("parent_asset_id")),
			FileName:            fileHeader.Filename, MIMEType: fileHeader.Header.Get("Content-Type"),
			ByteSize: fileHeader.Size, Reader: file,
		},
	)
	if err != nil {
		writeImageCanvasError(c, err)
		return
	}
	response.Created(c, toImageCanvasAssetResponse(asset))
}

func toImageEditorDocumentResponse(document *service.ImageEditorDocument) imageEditorDocumentResponse {
	if document == nil {
		return imageEditorDocumentResponse{
			AssetReferences: []service.ImageEditorAssetReference{},
			Revisions:       []imageEditorRevisionResponse{},
		}
	}
	result := imageEditorDocumentResponse{
		ID: document.PublicID, ProjectID: document.ProjectPublicID, NodeID: document.NodeID,
		BaseAsset:    toImageCanvasAssetResponse(document.BaseAsset),
		CurrentAsset: toImageCanvasAssetResponse(document.CurrentAsset),
		Document:     document.Document, Version: document.Version,
		CreatedAt: document.CreatedAt, UpdatedAt: document.UpdatedAt,
		AssetReferences: append([]service.ImageEditorAssetReference(nil), document.AssetReferences...),
		Revisions:       make([]imageEditorRevisionResponse, 0, len(document.Revisions)),
	}
	if result.AssetReferences == nil {
		result.AssetReferences = []service.ImageEditorAssetReference{}
	}
	for index := range document.Revisions {
		revision := &document.Revisions[index]
		result.Revisions = append(result.Revisions, imageEditorRevisionResponse{
			ID: revision.PublicID, Version: revision.Version,
			Asset: toImageCanvasAssetResponse(revision.Asset), Operation: revision.Operation,
			Parameters: revision.Parameters, CreatedAt: revision.CreatedAt,
		})
	}
	return result
}
