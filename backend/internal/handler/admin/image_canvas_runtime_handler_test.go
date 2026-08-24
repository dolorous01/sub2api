package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type adminImageCanvasSettingsRepo struct {
	values map[string]string
}

func (r *adminImageCanvasSettingsRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}

func (r *adminImageCanvasSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	value, exists := r.values[key]
	if !exists {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *adminImageCanvasSettingsRepo) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func (r *adminImageCanvasSettingsRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, nil
}

func (r *adminImageCanvasSettingsRepo) SetMultiple(context.Context, map[string]string) error {
	return nil
}

func (r *adminImageCanvasSettingsRepo) GetAll(context.Context) (map[string]string, error) {
	return nil, nil
}

func (r *adminImageCanvasSettingsRepo) Delete(context.Context, string) error {
	return nil
}

func adminImageCanvasRuntimeCapability() service.ImageModelCapability {
	return service.ImageModelCapability{
		DimensionMode: service.ImageDimensionModeSize,
		Generation:    true,
		MaxOutputs:    4,
		Sizes:         []string{"1024x1024"},
		OutputFormats: []string{"png", "jpeg"},
		Backgrounds:   []string{"transparent", "opaque"},
	}
}

func validAdminImageCanvasRuntimeSettings() service.ImageJobRuntimeSettings {
	return service.ImageJobRuntimeSettings{
		WorkerConcurrency:    3,
		MaxActiveJobsPerUser: 8,
		TaskTimeoutSeconds:   900,
		MaxOutputsPerJob:     4,
		MaxInputImages:       8,
		ResultTTLSeconds:     86400,
		DefaultOverrides: []service.ImageModelDefaultOverride{{
			Model: "gpt-image-1",
			Parameters: service.ImageModelDefaults{
				Size: "1024x1024", OutputFormat: "png", Background: "transparent",
			},
		}},
		CustomPresets: []service.ImageModelPresetOverride{},
	}
}

func TestAdminImageCanvasRuntimeSettingsUpdateAndReload(t *testing.T) {
	repo := &adminImageCanvasSettingsRepo{}
	handler := &ImageCanvasHandler{
		runtime: service.NewImageJobRuntimeSettingsService(repo, nil),
		catalog: adminImageCanvasCatalog{models: map[string]service.ImageModelCapability{
			"gpt-image-1": adminImageCanvasRuntimeCapability(),
		}},
	}
	router := adminImageCanvasRuntimeTestRouter(handler)
	body, err := json.Marshal(validAdminImageCanvasRuntimeSettings())
	require.NoError(t, err)

	updated := performAdminImageCanvasJSON(
		t, router, http.MethodPut, "/api/v1/admin/image-canvas/runtime", string(body),
	)
	require.Equal(t, http.StatusOK, updated.Code)
	require.Equal(t, int64(3), gjson.Get(updated.Body.String(), "data.settings.worker_concurrency").Int())
	require.Equal(
		t,
		"1024x1024",
		gjson.Get(updated.Body.String(), "data.settings.default_overrides.0.parameters.size").String(),
	)
	require.NotEmpty(t, repo.values[service.SettingKeyImageJobRuntimeSettings])

	loaded := performAdminImageCanvasJSON(
		t, router, http.MethodGet, "/api/v1/admin/image-canvas/runtime", "",
	)
	require.Equal(t, http.StatusOK, loaded.Code)
	require.Equal(t, int64(3), gjson.Get(loaded.Body.String(), "data.settings.worker_concurrency").Int())
}

func TestAdminImageCanvasRuntimeSettingsRejectsInvalidModelParameters(t *testing.T) {
	handler := &ImageCanvasHandler{
		runtime: service.NewImageJobRuntimeSettingsService(&adminImageCanvasSettingsRepo{}, nil),
		catalog: adminImageCanvasCatalog{models: map[string]service.ImageModelCapability{
			"gpt-image-1": adminImageCanvasRuntimeCapability(),
		}},
	}
	router := adminImageCanvasRuntimeTestRouter(handler)
	settings := validAdminImageCanvasRuntimeSettings()
	settings.DefaultOverrides[0].Parameters.OutputFormat = "jpeg"
	body, err := json.Marshal(settings)
	require.NoError(t, err)

	result := performAdminImageCanvasJSON(
		t, router, http.MethodPut, "/api/v1/admin/image-canvas/runtime", string(body),
	)
	require.Equal(t, http.StatusUnprocessableEntity, result.Code)
	require.Equal(
		t,
		"invalid_image_job_runtime_settings",
		gjson.Get(result.Body.String(), "reason").String(),
	)
}

func adminImageCanvasRuntimeTestRouter(handler *ImageCanvasHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 99})
		c.Set(string(middleware2.ContextKeyUserRole), service.RoleAdmin)
		c.Next()
	})
	router.GET("/api/v1/admin/image-canvas/runtime", handler.GetRuntimeSettings)
	router.PUT("/api/v1/admin/image-canvas/runtime", handler.UpdateRuntimeSettings)
	return router
}
