package admin

import (
	"bytes"
	"context"
	"encoding/json"
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

type adminImageCanvasCatalogWithAdmin struct {
	adminImageCanvasCatalog
	entries []service.ImageModelCatalogEntry
}

func (c adminImageCanvasCatalogWithAdmin) ListAdmin(context.Context) ([]service.ImageModelCatalogEntry, error) {
	return append([]service.ImageModelCatalogEntry(nil), c.entries...), nil
}

func (c adminImageCanvasCatalogWithAdmin) CheckSchedulability(_ context.Context, model string) (service.ImageModelCatalogEntry, error) {
	for _, entry := range c.entries {
		if entry.Model == model {
			return entry, nil
		}
	}
	return service.ImageModelCatalogEntry{Model: model, SchedulabilityReason: service.ImageModelSchedulabilityReasonUnknownModel}, nil
}

type adminImageJobRepo struct {
	service.ImageJobRepository
	jobs []*service.ImageJob
}

func (r adminImageJobRepo) ListRecentAdmin(context.Context, int) ([]*service.ImageJob, error) {
	return append([]*service.ImageJob(nil), r.jobs...), nil
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

func TestAdminImageCanvasPolicyRejectsModelWithoutEligibleGroup(t *testing.T) {
	repo := &adminImageCanvasPolicyRepo{}
	capability := service.ImageModelCapability{Generation: true}
	handler := &ImageCanvasHandler{
		policies: service.NewImageModelPolicyService(repo),
		catalog: adminImageCanvasCatalogWithAdmin{
			adminImageCanvasCatalog: adminImageCanvasCatalog{models: map[string]service.ImageModelCapability{
				"model-a": capability,
			}},
			entries: []service.ImageModelCatalogEntry{{
				Model: "model-a", Capability: capability, Schedulable: false,
				SchedulabilityReason: service.ImageModelSchedulabilityReasonNoGroup,
			}},
		},
	}
	router := adminImageCanvasTestRouter(handler, service.RoleAdmin)
	body := `{"version":0,"enabled":true,"models":[{"model":"model-a","enabled":true,"position":0}]}`

	response := performAdminImageCanvasJSON(t, router, http.MethodPut, "/api/v1/admin/image-canvas/model-policy", body)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	require.Equal(t, "invalid_image_model_policy", gjson.Get(response.Body.String(), "reason").String())
	require.Zero(t, repo.policy.Version)
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

func TestAdminImageCanvasOverviewSeparatesEntryAndPolicyState(t *testing.T) {
	settingsRepo := &adminImageCanvasSettingsRepo{values: map[string]string{
		service.SettingKeyImageCanvasEnabled: "true",
	}}
	catalog := adminImageCanvasCatalogWithAdmin{
		adminImageCanvasCatalog: adminImageCanvasCatalog{models: map[string]service.ImageModelCapability{
			"model-a": {Generation: true},
		}},
		entries: []service.ImageModelCatalogEntry{{
			Model: "model-a", Provider: service.ImageProviderOpenAI, Schedulable: true,
			Coverage: service.ImageModelCoverage{AccountCount: 2, GroupCount: 1, APIKeyCount: 3},
		}},
	}
	handler := &ImageCanvasHandler{
		policies: service.NewImageModelPolicyService(&adminImageCanvasPolicyRepo{policy: service.ImageModelPolicy{
			Version: 4, Enabled: false,
			Items: []service.ImageModelPolicyItem{{Model: "model-a", Enabled: true, Position: 0}},
		}}),
		catalog:  catalog,
		settings: service.NewSettingService(settingsRepo, nil),
	}
	router := adminImageCanvasTestRouter(handler, service.RoleAdmin)

	result := performAdminImageCanvasJSON(t, router, http.MethodGet, "/api/v1/admin/image-canvas/overview", "")
	require.Equal(t, http.StatusOK, result.Code)
	require.True(t, gjson.Get(result.Body.String(), "data.entry_enabled").Bool())
	require.False(t, gjson.Get(result.Body.String(), "data.policy_enabled").Bool())
	require.Equal(t, int64(4), gjson.Get(result.Body.String(), "data.policy_version").Int())
	require.Equal(t, int64(1), gjson.Get(result.Body.String(), "data.active_model_count").Int())
}

func TestAdminImageCanvasModelCheckIncludesPolicyEligibility(t *testing.T) {
	catalog := adminImageCanvasCatalogWithAdmin{
		adminImageCanvasCatalog: adminImageCanvasCatalog{models: map[string]service.ImageModelCapability{
			"model-a": {Generation: true},
		}},
		entries: []service.ImageModelCatalogEntry{{Model: "model-a", Schedulable: true}},
	}
	handler := &ImageCanvasHandler{
		policies: service.NewImageModelPolicyService(&adminImageCanvasPolicyRepo{policy: service.ImageModelPolicy{
			Version: 1, Enabled: true,
			Items: []service.ImageModelPolicyItem{{Model: "model-a", Enabled: false, Position: 0}},
		}}),
		catalog: catalog,
	}
	router := adminImageCanvasTestRouter(handler, service.RoleAdmin)

	result := performAdminImageCanvasJSON(t, router, http.MethodPost, "/api/v1/admin/image-canvas/models/model-a/check", "")
	require.Equal(t, http.StatusOK, result.Code)
	require.True(t, gjson.Get(result.Body.String(), "data.schedulable").Bool())
	require.True(t, gjson.Get(result.Body.String(), "data.policy_listed").Bool())
	require.False(t, gjson.Get(result.Body.String(), "data.policy_item_enabled").Bool())
	require.False(t, gjson.Get(result.Body.String(), "data.eligible").Bool())
}

func TestAdminImageCanvasRecentJobsReturnsStableEmptyAttemptArrays(t *testing.T) {
	now := time.Now()
	repo := adminImageJobRepo{jobs: []*service.ImageJob{{
		PublicID: "imgjob_test", Status: service.ImageJobStatusQueued,
		Operation: "generation", RequestedModel: "model-a",
		RequestedCount: 1, CreatedAt: now, UpdatedAt: now,
	}}}
	handler := &ImageCanvasHandler{jobs: service.NewImageJobService(repo, nil)}
	router := adminImageCanvasTestRouter(handler, service.RoleAdmin)

	result := performAdminImageCanvasJSON(t, router, http.MethodGet, "/api/v1/admin/image-canvas/jobs/recent", "")
	require.Equal(t, http.StatusOK, result.Code)
	require.True(t, gjson.Get(result.Body.String(), "data.items.0.attempt_plan").IsArray())
	require.True(t, gjson.Get(result.Body.String(), "data.items.0.attempt_log").IsArray())
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
	router.GET("/api/v1/admin/image-canvas/overview", handler.GetOverview)
	router.POST("/api/v1/admin/image-canvas/models/:model/check", handler.CheckModel)
	router.GET("/api/v1/admin/image-canvas/jobs/recent", handler.ListRecentJobs)
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
