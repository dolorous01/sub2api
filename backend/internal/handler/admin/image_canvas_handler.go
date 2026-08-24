package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type adminImageModelPolicyService interface {
	Get(context.Context) (*service.ImageModelPolicy, error)
	Replace(context.Context, int64, int64, bool, []service.ImageModelPolicyItem) (*service.ImageModelPolicy, error)
	ListAudit(context.Context, int) ([]service.ImageModelPolicyAudit, error)
}

type ImageCanvasHandler struct {
	policies adminImageModelPolicyService
	catalog  service.ImageModelCatalog
	runtime  *service.ImageJobRuntimeSettingsService
	jobs     *service.ImageJobService
}

func NewImageCanvasHandler(
	policies *service.ImageModelPolicyService,
	catalog service.ImageModelCatalog,
	runtime *service.ImageJobRuntimeSettingsService,
	jobs *service.ImageJobService,
) *ImageCanvasHandler {
	return &ImageCanvasHandler{policies: policies, catalog: catalog, runtime: runtime, jobs: jobs}
}

type UpdateImageModelPolicyRequest struct {
	Version int64                          `json:"version"`
	Enabled bool                           `json:"enabled"`
	Models  []service.ImageModelPolicyItem `json:"models"`
}

type ImageModelPolicyResponse struct {
	Version         int64                          `json:"version"`
	Enabled         bool                           `json:"enabled"`
	Models          []service.ImageModelPolicyItem `json:"models"`
	AvailableModels []service.ImageModelPolicyItem `json:"available_models"`
}

type ImageJobRuntimeSettingsResponse struct {
	Settings        service.ImageJobRuntimeSettings `json:"settings"`
	Defaults        service.ImageJobRuntimeSettings `json:"defaults"`
	Storage         service.ImageJobStorageInfo     `json:"storage"`
	Worker          service.ImageJobWorkerSnapshot  `json:"worker"`
	AvailableModels []service.ImageModelPolicyItem  `json:"available_models"`
}

func (h *ImageCanvasHandler) GetRuntimeSettings(c *gin.Context) {
	if !requireImageCanvasAdmin(c) {
		return
	}
	if h == nil || h.runtime == nil || h.catalog == nil {
		writeAdminImageCanvasError(c, fmt.Errorf("image canvas runtime dependencies are unavailable"))
		return
	}
	settings, err := h.runtime.Get(c.Request.Context())
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	capabilities, err := h.catalog.ListSchedulable(c.Request.Context())
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	response.Success(c, h.runtimeSettingsResponse(settings, capabilities))
}

func (h *ImageCanvasHandler) UpdateRuntimeSettings(c *gin.Context) {
	if !requireImageCanvasAdmin(c) {
		return
	}
	if h == nil || h.runtime == nil || h.catalog == nil {
		writeAdminImageCanvasError(c, fmt.Errorf("image canvas runtime dependencies are unavailable"))
		return
	}
	var request service.ImageJobRuntimeSettings
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAdminImageCanvasError(c, fmt.Errorf("%w: invalid request body", service.ErrImageJobRuntimeSettingsInvalid))
		return
	}
	capabilities, err := h.catalog.ListSchedulable(c.Request.Context())
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	if err := validateImageJobRuntimeModelSettings(request, capabilities); err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	updated, err := h.runtime.Update(c.Request.Context(), request)
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	// Rebuild the catalog response after the runtime override cache changes.
	capabilities, err = h.catalog.ListSchedulable(c.Request.Context())
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	response.Success(c, h.runtimeSettingsResponse(updated, capabilities))
}

func (h *ImageCanvasHandler) runtimeSettingsResponse(
	settings service.ImageJobRuntimeSettings,
	capabilities map[string]service.ImageModelCapability,
) ImageJobRuntimeSettingsResponse {
	worker := service.ImageJobWorkerSnapshot{}
	if h != nil && h.jobs != nil {
		worker = h.jobs.WorkerSnapshot()
	}
	return ImageJobRuntimeSettingsResponse{
		Settings: settings, Defaults: h.runtime.Defaults(), Storage: h.runtime.StorageInfo(),
		Worker: worker, AvailableModels: buildImageModelPolicyResponse(nil, capabilities).AvailableModels,
	}
}

func validateImageJobRuntimeModelSettings(
	settings service.ImageJobRuntimeSettings,
	capabilities map[string]service.ImageModelCapability,
) error {
	validate := func(model string, parameters service.ImageModelDefaults) error {
		capability, exists := capabilities[strings.TrimSpace(model)]
		if !exists {
			return fmt.Errorf("%w: model %q is not schedulable", service.ErrImageJobRuntimeSettingsInvalid, model)
		}
		if err := service.ValidateImageModelDefaultsForCapability(parameters, capability); err != nil {
			return fmt.Errorf("%w: model %q: %v", service.ErrImageJobRuntimeSettingsInvalid, model, err)
		}
		return nil
	}
	for _, override := range settings.DefaultOverrides {
		if err := validate(override.Model, override.Parameters); err != nil {
			return err
		}
	}
	for _, preset := range settings.CustomPresets {
		if err := validate(preset.Model, preset.Parameters); err != nil {
			return err
		}
	}
	return nil
}

func (h *ImageCanvasHandler) GetPolicy(c *gin.Context) {
	if !requireImageCanvasAdmin(c) {
		return
	}
	if h == nil || h.policies == nil || h.catalog == nil {
		writeAdminImageCanvasError(c, fmt.Errorf("image canvas policy dependencies are unavailable"))
		return
	}
	policy, err := h.policies.Get(c.Request.Context())
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	capabilities, err := h.catalog.ListSchedulable(c.Request.Context())
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	response.Success(c, buildImageModelPolicyResponse(policy, capabilities))
}

func (h *ImageCanvasHandler) UpdatePolicy(c *gin.Context) {
	operatorID, ok := imageCanvasAdminOperator(c)
	if !ok {
		return
	}
	if h == nil || h.policies == nil || h.catalog == nil {
		writeAdminImageCanvasError(c, fmt.Errorf("image canvas policy dependencies are unavailable"))
		return
	}
	var request UpdateImageModelPolicyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAdminImageCanvasError(c, fmt.Errorf("%w: invalid request body", service.ErrImageModelPolicyInvalid))
		return
	}
	if request.Version < 0 {
		writeAdminImageCanvasError(c, fmt.Errorf("%w: version must not be negative", service.ErrImageModelPolicyInvalid))
		return
	}
	models, err := service.NormalizeImageModelPolicyItems(request.Models)
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	capabilities, err := h.catalog.ListSchedulable(c.Request.Context())
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	for _, model := range models {
		if !model.Enabled {
			continue
		}
		capability, available := capabilities[model.Model]
		if !available || (!capability.Generation && !capability.Edit) {
			writeAdminImageCanvasError(c, fmt.Errorf("%w: enabled model %q is not schedulable", service.ErrImageModelPolicyInvalid, model.Model))
			return
		}
	}
	updated, err := h.policies.Replace(c.Request.Context(), request.Version, operatorID, request.Enabled, models)
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	response.Success(c, buildImageModelPolicyResponse(updated, capabilities))
}

func (h *ImageCanvasHandler) ListAudit(c *gin.Context) {
	if !requireImageCanvasAdmin(c) {
		return
	}
	if h == nil || h.policies == nil {
		writeAdminImageCanvasError(c, fmt.Errorf("image canvas policy dependencies are unavailable"))
		return
	}
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	audits, err := h.policies.ListAudit(c.Request.Context(), limit)
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	response.Success(c, gin.H{"items": audits})
}

func buildImageModelPolicyResponse(policy *service.ImageModelPolicy, capabilities map[string]service.ImageModelCapability) ImageModelPolicyResponse {
	result := ImageModelPolicyResponse{}
	if policy != nil {
		result.Version = policy.Version
		result.Enabled = policy.Enabled
		result.Models = append([]service.ImageModelPolicyItem(nil), policy.Items...)
		for index := range result.Models {
			result.Models[index].Capability = capabilities[result.Models[index].Model]
		}
	}
	names := make([]string, 0, len(capabilities))
	for model := range capabilities {
		names = append(names, model)
	}
	sort.Strings(names)
	result.AvailableModels = make([]service.ImageModelPolicyItem, 0, len(names))
	for position, model := range names {
		result.AvailableModels = append(result.AvailableModels, service.ImageModelPolicyItem{
			Model: model, Enabled: true, Position: position, Capability: capabilities[model],
		})
	}
	return result
}

func imageCanvasAdminOperator(c *gin.Context) (int64, bool) {
	if !requireImageCanvasAdmin(c) {
		return 0, false
	}
	subject, _ := middleware2.GetAuthSubjectFromContext(c)
	return subject.UserID, true
}

func requireImageCanvasAdmin(c *gin.Context) bool {
	subject, hasSubject := middleware2.GetAuthSubjectFromContext(c)
	role, hasRole := middleware2.GetUserRoleFromContext(c)
	if !hasSubject || subject.UserID <= 0 || !hasRole {
		response.ErrorWithDetails(c, http.StatusUnauthorized, "Admin authentication required", "unauthorized", nil)
		return false
	}
	if role != service.RoleAdmin {
		response.ErrorWithDetails(c, http.StatusForbidden, "Admin access required", "forbidden", nil)
		return false
	}
	return true
}

func writeAdminImageCanvasError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrImageModelPolicyVersionConflict):
		response.ErrorWithDetails(c, http.StatusConflict, "Image model policy was updated by another administrator", "policy_version_conflict", nil)
	case errors.Is(err, service.ErrImageModelPolicyInvalid),
		errors.Is(err, service.ErrImageModelSelectionInvalid),
		errors.Is(err, service.ErrNoCompatibleImageModel):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, err.Error(), "invalid_image_model_policy", nil)
	case errors.Is(err, service.ErrImageJobRuntimeSettingsInvalid):
		response.ErrorWithDetails(c, http.StatusUnprocessableEntity, err.Error(), "invalid_image_job_runtime_settings", nil)
	default:
		response.ErrorFrom(c, err)
	}
}
