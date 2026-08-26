package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type imageEditorServiceStub struct {
	createDocument *service.ImageEditorDocument
	createCreated  bool
	createUserID   int64
	updateInput    service.ImageEditorDocumentUpdate
	updateError    error
	uploadInput    service.ImageEditorDerivedAssetUpload
	uploadData     []byte
}

func (s *imageEditorServiceStub) CreateOrGet(
	_ context.Context,
	userID int64,
	_, _, _ string,
	_ json.RawMessage,
	_ []service.ImageEditorAssetReference,
) (*service.ImageEditorDocument, bool, error) {
	s.createUserID = userID
	return s.createDocument, s.createCreated, nil
}

func (s *imageEditorServiceStub) Get(context.Context, int64, string) (*service.ImageEditorDocument, error) {
	return s.createDocument, nil
}

func (s *imageEditorServiceStub) Update(
	_ context.Context,
	input service.ImageEditorDocumentUpdate,
) (*service.ImageEditorDocument, error) {
	s.updateInput = input
	return s.createDocument, s.updateError
}

func (s *imageEditorServiceStub) UploadDerivedAssetStream(
	_ context.Context,
	input service.ImageEditorDerivedAssetUpload,
) (*service.ImageAsset, error) {
	s.uploadInput = input
	s.uploadData, _ = io.ReadAll(input.Reader)
	return &service.ImageAsset{
		PublicID: "asset_derived_123", SourceType: "derived", MediaKind: "image",
		MIMEType: "image/png", Width: 32, Height: 32, ByteSize: int64(len(s.uploadData)),
	}, nil
}

func TestImageEditorHandlerCreatesOwnedDocument(t *testing.T) {
	editor := &imageEditorServiceStub{
		createCreated: true,
		createDocument: &service.ImageEditorDocument{
			PublicID: "ied_public_123", ProjectPublicID: "icp_public_123", NodeID: "node-a",
			Document: json.RawMessage(`{"schema_version":1}`), Version: 1,
			BaseAsset:    &service.ImageAsset{PublicID: "asset_base_123", MediaKind: "image"},
			CurrentAsset: &service.ImageAsset{PublicID: "asset_base_123", MediaKind: "image"},
		},
	}
	router := imageEditorTestRouter(&ImageCanvasHandler{editors: editor})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/image-canvas/editor-documents", strings.NewReader(`{
		"project_id":"icp_public_123",
		"node_id":"node-a",
		"base_asset_id":"asset_base_123",
		"document":{"schema_version":1}
	}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if editor.createUserID != 99 || !strings.Contains(recorder.Body.String(), "ied_public_123") {
		t.Fatalf("unexpected create result: user=%d body=%s", editor.createUserID, recorder.Body.String())
	}
	for _, forbidden := range []string{"object_key", "owner_user_id"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestImageEditorHandlerMapsVersionConflict(t *testing.T) {
	editor := &imageEditorServiceStub{updateError: service.ErrImageEditorDocumentVersionConflict}
	router := imageEditorTestRouter(&ImageCanvasHandler{editors: editor})
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/image-canvas/editor-documents/ied_public_123", strings.NewReader(`{
		"version":1,
		"document":{"schema_version":1}
	}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict ||
		!strings.Contains(recorder.Body.String(), "editor_version_conflict") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if editor.updateInput.UserID != 99 || editor.updateInput.PublicID != "ied_public_123" {
		t.Fatalf("unexpected update input: %+v", editor.updateInput)
	}
}

func TestImageEditorHandlerUploadsDerivedAsset(t *testing.T) {
	editor := &imageEditorServiceStub{}
	router := imageEditorTestRouter(&ImageCanvasHandler{editors: editor})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("parent_asset_id", "asset_parent_123"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "crop.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("image-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/image-canvas/editor-documents/ied_public_123/assets",
		&body,
	)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if editor.uploadInput.UserID != 99 ||
		editor.uploadInput.DocumentPublicID != "ied_public_123" ||
		editor.uploadInput.ParentAssetPublicID != "asset_parent_123" ||
		string(editor.uploadData) != "image-bytes" {
		t.Fatalf("unexpected upload: input=%+v data=%q", editor.uploadInput, editor.uploadData)
	}
}

func imageEditorTestRouter(handler *ImageCanvasHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(context *gin.Context) {
		context.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 99})
		context.Next()
	})
	router.POST("/api/v1/image-canvas/editor-documents", handler.CreateEditorDocument)
	router.GET("/api/v1/image-canvas/editor-documents/:document_id", handler.GetEditorDocument)
	router.PATCH("/api/v1/image-canvas/editor-documents/:document_id", handler.UpdateEditorDocument)
	router.POST("/api/v1/image-canvas/editor-documents/:document_id/assets", handler.UploadEditorDerivedAsset)
	return router
}
