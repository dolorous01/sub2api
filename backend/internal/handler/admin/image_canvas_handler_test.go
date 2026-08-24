package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type adminImageCanvasPolicyRepo struct {
	policy service.ImageModelPolicy
	audits []service.ImageModelPolicyAudit
}

func (r *adminImageCanvasPolicyRepo) Get(context.Context) (*service.ImageModelPolicy, error) {
	copy := r.policy
	copy.Items = append([]service.ImageModelPolicyItem(nil), r.policy.Items...)
	return &copy, nil
}

func (r *adminImageCanvasPolicyRepo) Replace(_ context.Context, expectedVersion, operatorID int64, enabled bool, items []service.ImageModelPolicyItem) (*service.ImageModelPolicy, error) {
	if expectedVersion != r.policy.Version {
		return nil, service.ErrImageModelPolicyVersionConflict
	}
	oldVersion := r.policy.Version
	r.policy = service.ImageModelPolicy{Version: oldVersion + 1, Enabled: enabled, Items: append([]service.ImageModelPolicyItem(nil), items...)}
	r.audits = append(r.audits, service.ImageModelPolicyAudit{OperatorUserID: operatorID, OldVersion: oldVersion, NewVersion: r.policy.Version})
	return r.Get(context.Background())
}

func (r *adminImageCanvasPolicyRepo) ListAudit(context.Context, int) ([]service.ImageModelPolicyAudit, error) {
	return append([]service.ImageModelPolicyAudit(nil), r.audits...), nil
}

type adminImageCanvasCatalog struct {
	models map[string]service.ImageModelCapability
}

func (c adminImageCanvasCatalog) ListOwnedAPIKeys(context.Context, int64) ([]service.ImageCanvasAPIKey, error) {
	return nil, nil
}

func (c adminImageCanvasCatalog) ForAPIKey(context.Context, int64, int64) (map[string]service.ImageModelCapability, error) {
	return c.models, nil
}

func (c adminImageCanvasCatalog) ListSchedulable(context.Context) (map[string]service.ImageModelCapability, error) {
	return c.models, nil
}

func TestAdminImageCanvasPolicyUpdateUsesCASAndServerCapabilities(t *testing.T) {
	repo := &adminImageCanvasPolicyRepo{}
	policy := service.NewImageModelPolicyService(repo)
	handler := &ImageCanvasHandler{
		policies: policy,
		catalog: adminImageCanvasCatalog{models: map[string]service.ImageModelCapability{
			"model-a": {Generation: true, Edit: true},
		}},
	}
	router := adminImageCanvasTestRouter(handler, service.RoleAdmin)
	body := `{"version":0,"enabled":true,"models":[{"model":"model-a","enabled":true,"position":0,"capability":{"generation":false}}]}`

	first := performAdminImageCanvasJSON(t, router, http.MethodPut, "/api/v1/admin/image-canvas/model-policy", body)
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, int64(1), gjson.Get(first.Body.String(), "data.version").Int())
	require.True(t, gjson.Get(first.Body.String(), "data.models.0.capability.generation").Bool())
	require.Len(t, repo.audits, 1)
	require.Equal(t, int64(99), repo.audits[0].OperatorUserID)

	conflict := performAdminImageCanvasJSON(t, router, http.MethodPut, "/api/v1/admin/image-canvas/model-policy", body)
	require.Equal(t, http.StatusConflict, conflict.Code)
	require.Equal(t, "policy_version_conflict", gjson.Get(conflict.Body.String(), "reason").String())
}

func TestAdminImageCanvasPolicyRejectsUnschedulableEnabledModel(t *testing.T) {
	repo := &adminImageCanvasPolicyRepo{}
	handler := &ImageCanvasHandler{
		policies: service.NewImageModelPolicyService(repo),
		catalog:  adminImageCanvasCatalog{models: map[string]service.ImageModelCapability{}},
	}
	router := adminImageCanvasTestRouter(handler, service.RoleAdmin)
	body := `{"version":0,"enabled":true,"models":[{"model":"missing","enabled":true,"position":0}]}`

	response := performAdminImageCanvasJSON(t, router, http.MethodPut, "/api/v1/admin/image-canvas/model-policy", body)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	require.Equal(t, "invalid_image_model_policy", gjson.Get(response.Body.String(), "reason").String())
}

func TestAdminImageCanvasPolicyRejectsNonAdmin(t *testing.T) {
	handler := &ImageCanvasHandler{
		policies: service.NewImageModelPolicyService(&adminImageCanvasPolicyRepo{}),
		catalog:  adminImageCanvasCatalog{},
	}
	router := adminImageCanvasTestRouter(handler, service.RoleUser)

	response := performAdminImageCanvasJSON(t, router, http.MethodGet, "/api/v1/admin/image-canvas/model-policy", "")
	require.Equal(t, http.StatusForbidden, response.Code)
}

func adminImageCanvasTestRouter(handler *ImageCanvasHandler, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 99})
		c.Set(string(middleware2.ContextKeyUserRole), role)
		c.Next()
	})
	router.GET("/api/v1/admin/image-canvas/model-policy", handler.GetPolicy)
	router.PUT("/api/v1/admin/image-canvas/model-policy", handler.UpdatePolicy)
	router.GET("/api/v1/admin/image-canvas/model-policy/audit", handler.ListAudit)
	return router
}

func performAdminImageCanvasJSON(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Body.Len() > 0 {
		var value any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &value))
	}
	return recorder
}
