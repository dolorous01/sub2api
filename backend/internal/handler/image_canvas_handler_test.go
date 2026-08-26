package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageCanvasConfigCatalogStub struct {
	keys []service.ImageCanvasAPIKey
}

func (s imageCanvasConfigCatalogStub) ListOwnedAPIKeys(context.Context, int64) ([]service.ImageCanvasAPIKey, error) {
	return s.keys, nil
}

func (imageCanvasConfigCatalogStub) ForAPIKey(context.Context, int64, int64) (map[string]service.ImageModelCapability, error) {
	return map[string]service.ImageModelCapability{}, nil
}

func (imageCanvasConfigCatalogStub) ListSchedulable(context.Context) (map[string]service.ImageModelCapability, error) {
	return map[string]service.ImageModelCapability{}, nil
}

type imageCanvasConfigPolicyStub struct {
	policy *service.ImageModelPolicy
}

func (s imageCanvasConfigPolicyStub) Get(context.Context) (*service.ImageModelPolicy, error) {
	return s.policy, nil
}

func TestImageCanvasGetConfigReflectsPolicyEnabledState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/image-canvas/config", nil)
	ginContext.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 99})
	handler := &ImageCanvasHandler{
		catalog:  imageCanvasConfigCatalogStub{keys: []service.ImageCanvasAPIKey{{ID: 7, Name: "canvas", Available: true}}},
		policies: imageCanvasConfigPolicyStub{policy: &service.ImageModelPolicy{Enabled: false, Version: 12}},
	}

	handler.GetConfig(ginContext)

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Data ImageCanvasConfigResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.False(t, envelope.Data.Enabled)
	require.Equal(t, int64(12), envelope.Data.PolicyVersion)
	require.Equal(t, int64(7), envelope.Data.APIKeys[0].ID)
}

func TestBindImageCanvasProjectWriteRequestRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	body := io.MultiReader(
		strings.NewReader(`{"name":"large","document":"`),
		io.LimitReader(repeatedCanvasByte('a'), maxImageCanvasProjectWriteRequestBytes),
		strings.NewReader(`"}`),
	)
	context.Request = httptest.NewRequest(http.MethodPost, "/", body)
	context.Request.Header.Set("Content-Type", "application/json")

	var request imageCanvasProjectWriteRequest
	if err := bindImageCanvasProjectWriteRequest(context, &request); err == nil {
		t.Fatal("bindImageCanvasProjectWriteRequest() succeeded for an oversized body")
	}
}

type repeatedCanvasByte byte

func (value repeatedCanvasByte) Read(target []byte) (int, error) {
	for index := range target {
		target[index] = byte(value)
	}
	return len(target), nil
}
