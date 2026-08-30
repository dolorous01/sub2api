package admin

import (
	"context"
	"errors"
	"fmt"
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
	settings *service.SettingService
}

func NewImageCanvasHandler(
	policies *service.ImageModelPolicyService,
	catalog service.ImageModelCatalog,
	runtime *service.ImageJobRuntimeSettingsService,
	jobs *service.ImageJobService,
	dependencies ...any,
) *ImageCanvasHandler {
	h := &ImageCanvasHandler{policies: policies, catalog: catalog, runtime: runtime, jobs: jobs}
	for _, dependency := range dependencies {
		if settings, ok := dependency.(*service.SettingService); ok {
			h.settings = settings
		}
	}
	return h
}

// SetSettingService wires the route-level feature flag into the admin status
// view without changing the constructor shape used by older generated Wire
// files.
func (h *ImageCanvasHandler) SetSettingService(settings *service.SettingService) *ImageCanvasHandler {
	if h != nil {
		h.settings = settings
	}
	return h
}

type UpdateImageModelPolicyRequest struct {
	Version int64                          `json:"version"`
	Enabled bool                           `json:"enabled"`
	Models  []service.ImageModelPolicyItem `json:"models"`
}

type ImageModelPolicyResponse struct {
	Version         int64                            `json:"version"`
	Enabled         bool                             `json:"enabled"`
	Models          []service.ImageModelPolicyItem   `json:"models"`
	AvailableModels []service.ImageModelPolicyItem   `json:"available_models"`
	Catalog         []service.ImageModelCatalogEntry `json:"catalog,omitempty"`
}

type ImageJobRuntimeSettingsResponse struct {
	Settings        service.ImageJobRuntimeSettings `json:"settings"`
	Defaults        service.ImageJobRuntimeSettings `json:"defaults"`
	Storage         service.ImageJobStorageInfo     `json:"storage"`
	Worker          service.ImageJobWorkerSnapshot  `json:"worker"`
	AvailableModels []service.ImageModelPolicyItem  `json:"available_models"`
}

type ImageCanvasFallbackRules struct {
	SelectedModelFirst      bool     `json:"selected_model_first"`
	SameProviderOnly        bool     `json:"same_provider_only"`
	RetryableStatusCodes    []string `json:"retryable_status_codes"`
	RequiresZeroFinalOutput bool     `json:"requires_zero_final_output"`
	ContentPolicyFallsBack  bool     `json:"content_policy_falls_back"`
	AccountFailoverFirst    bool     `json:"account_failover_first"`
}

type ImageCanvasOverviewResponse struct {
	EntryEnabled     bool                             `json:"entry_enabled"`
	PolicyEnabled    bool                             `json:"policy_enabled"`
	PolicyVersion    int64                            `json:"policy_version"`
	ActiveModelCount int                              `json:"active_model_count"`
	Catalog          []service.ImageModelCatalogEntry `json:"catalog"`
	Worker           service.ImageJobWorkerSnapshot   `json:"worker"`
	Storage          service.ImageJobStorageInfo      `json:"storage"`
	FallbackRules    ImageCanvasFallbackRules         `json:"fallback_rules"`
}

type ImageModelCheckResponse struct {
	Model                string                       `json:"model"`
	Schedulable          bool                         `json:"schedulable"`
	SchedulabilityReason string                       `json:"schedulability_reason,omitempty"`
	PolicyEnabled        bool                         `json:"policy_enabled"`
	PolicyListed         bool                         `json:"policy_listed"`
	PolicyItemEnabled    bool                         `json:"policy_item_enabled"`
	Eligible             bool                         `json:"eligible"`
	Provider             string                       `json:"provider,omitempty"`
	MediaKind            string                       `json:"media_kind,omitempty"`
	Capability           service.ImageModelCapability `json:"capability,omitempty"`
	Coverage             service.ImageModelCoverage   `json:"coverage"`
}

type ImageJobAdminView struct {
	ID              string                    `json:"id"`
	UserID          int64                     `json:"user_id"`
	Status          service.ImageJobStatus    `json:"status"`
	Operation       string                    `json:"operation"`
	Mode            string                    `json:"mode,omitempty"`
	RequestedModel  string                    `json:"requested_model"`
	MappedModel     string                    `json:"mapped_model,omitempty"`
	SelectedModel   string                    `json:"selected_model,omitempty"`
	SuccessfulModel string                    `json:"successful_model,omitempty"`
	APIKeyID        int64                     `json:"api_key_id"`
	GroupID         int64                     `json:"group_id"`
	ProjectID       *int64                    `json:"project_id,omitempty"`
	PolicyVersion   int64                     `json:"policy_version,omitempty"`
	ExecutionPhase  string                    `json:"execution_phase,omitempty"`
	RequestedCount  int                       `json:"requested_count"`
	CompletedCount  int                       `json:"completed_count"`
	AttemptPlan     []string                  `json:"attempt_plan"`
	AttemptLog      []service.ImageJobAttempt `json:"attempt_log"`
	Error           *service.ImageJobError    `json:"error,omitempty"`
	CreatedAt       time.Time                 `json:"created_at"`
	StartedAt       *time.Time                `json:"started_at,omitempty"`
	FinishedAt      *time.Time                `json:"finished_at,omitempty"`
	UpdatedAt       time.Time                 `json:"updated_at"`
	DurationMS      int64                     `json:"duration_ms,omitempty"`
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

// GetOverview returns the compact operational snapshot used by the redesigned
// admin console. It deliberately keeps policy state separate from the route
// feature flag so an operator can see which gate is stopping requests.
func (h *ImageCanvasHandler) GetOverview(c *gin.Context) {
	if !requireImageCanvasAdmin(c) {
		return
	}
	if h == nil || h.catalog == nil {
		writeAdminImageCanvasError(c, fmt.Errorf("image canvas overview dependencies are unavailable"))
		return
	}
	catalog, err := h.listAdminCatalog(c.Request.Context())
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	policyEnabled := false
	var policyVersion int64
	if h.policies != nil {
		policy, policyErr := h.policies.Get(c.Request.Context())
		if policyErr != nil {
			writeAdminImageCanvasError(c, policyErr)
			return
		}
		if policy != nil {
			policyEnabled = policy.Enabled
			policyVersion = policy.Version
		}
	}
	worker := service.ImageJobWorkerSnapshot{}
	storage := service.ImageJobStorageInfo{}
	if h.runtime != nil {
		worker = h.runtimeWorkerSnapshot()
		storage = h.runtime.StorageInfo()
	}
	entryEnabled := false
	if h.settings != nil {
		entryEnabled = h.settings.IsImageCanvasEnabled(c.Request.Context())
	}
	activeModels := 0
	for _, item := range catalog {
		if item.Schedulable && strings.EqualFold(strings.TrimSpace(item.MediaKind), "image") {
			activeModels++
		}
	}
	response.Success(c, ImageCanvasOverviewResponse{
		EntryEnabled: entryEnabled, PolicyEnabled: policyEnabled, PolicyVersion: policyVersion,
		ActiveModelCount: activeModels, Catalog: catalog, Worker: worker, Storage: storage,
		FallbackRules: ImageCanvasFallbackRules{
			SelectedModelFirst:      true,
			SameProviderOnly:        true,
			RetryableStatusCodes:    []string{"0", "429", "5xx"},
			RequiresZeroFinalOutput: true,
			ContentPolicyFallsBack:  false,
			AccountFailoverFirst:    true,
		},
	})
}

// CheckModel is a non-billable preflight check. It only inspects the local
// account/catalog and policy state; no provider request is sent.
func (h *ImageCanvasHandler) CheckModel(c *gin.Context) {
	if !requireImageCanvasAdmin(c) {
		return
	}
	if h == nil || h.catalog == nil {
		writeAdminImageCanvasError(c, fmt.Errorf("image canvas catalog dependencies are unavailable"))
		return
	}
	model := strings.TrimSpace(c.Param("model"))
	entry, err := h.checkAdminModel(c.Request.Context(), model)
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	result := ImageModelCheckResponse{
		Model: entry.Model, Schedulable: entry.Schedulable,
		SchedulabilityReason: entry.SchedulabilityReason,
		Provider:             entry.Provider, MediaKind: entry.MediaKind,
		Capability: entry.Capability, Coverage: entry.Coverage,
	}
	if h.policies != nil {
		policy, policyErr := h.policies.Get(c.Request.Context())
		if policyErr != nil {
			writeAdminImageCanvasError(c, policyErr)
			return
		}
		if policy != nil {
			result.PolicyEnabled = policy.Enabled
			for _, item := range policy.Items {
				if item.Model == entry.Model || strings.EqualFold(item.Model, entry.Model) {
					result.PolicyListed = true
					result.PolicyItemEnabled = item.Enabled
					break
				}
			}
		}
	} else {
		result.PolicyEnabled = true
		result.PolicyListed = true
		result.PolicyItemEnabled = true
	}
	result.Eligible = result.Schedulable && result.PolicyEnabled && result.PolicyListed && result.PolicyItemEnabled
	response.Success(c, result)
}

func (h *ImageCanvasHandler) ListRecentJobs(c *gin.Context) {
	if !requireImageCanvasAdmin(c) {
		return
	}
	if h == nil || h.jobs == nil {
		writeAdminImageCanvasError(c, service.ErrImageJobUnavailable)
		return
	}
	limit := 30
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	jobs, err := h.jobs.ListRecentAdmin(c.Request.Context(), limit)
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	items := make([]ImageJobAdminView, 0, len(jobs))
	for _, job := range jobs {
		if job != nil {
			items = append(items, imageJobAdminView(job))
		}
	}
	response.Success(c, gin.H{"items": items})
}

func (h *ImageCanvasHandler) listAdminCatalog(ctx context.Context) ([]service.ImageModelCatalogEntry, error) {
	if adminCatalog, ok := h.catalog.(service.ImageModelCatalogAdmin); ok {
		return adminCatalog.ListAdmin(ctx)
	}
	capabilities, err := h.catalog.ListSchedulable(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(capabilities))
	for model := range capabilities {
		models = append(models, model)
	}
	sort.Strings(models)
	result := make([]service.ImageModelCatalogEntry, 0, len(models))
	for _, model := range models {
		capability := capabilities[model]
		result = append(result, service.ImageModelCatalogEntry{
			Model: model, Provider: capability.Provider, MediaKind: capability.MediaKind,
			Capability: capability, Schedulable: true,
		})
	}
	return result, nil
}

func (h *ImageCanvasHandler) listSchedulableAdminCatalog(
	ctx context.Context,
) ([]service.ImageModelCatalogEntry, map[string]service.ImageModelCapability, error) {
	catalog, err := h.listAdminCatalog(ctx)
	if err != nil {
		return nil, nil, err
	}
	capabilities := make(map[string]service.ImageModelCapability, len(catalog))
	for _, entry := range catalog {
		if entry.Schedulable {
			capabilities[entry.Model] = entry.Capability
		}
	}
	return catalog, capabilities, nil
}

func (h *ImageCanvasHandler) checkAdminModel(ctx context.Context, model string) (service.ImageModelCatalogEntry, error) {
	if adminCatalog, ok := h.catalog.(service.ImageModelCatalogAdmin); ok {
		return adminCatalog.CheckSchedulability(ctx, model)
	}
	model = strings.TrimSpace(model)
	capabilities, err := h.catalog.ListSchedulable(ctx)
	if err != nil {
		return service.ImageModelCatalogEntry{}, err
	}
	capability, ok := capabilities[model]
	if !ok {
		return service.ImageModelCatalogEntry{Model: model, SchedulabilityReason: service.ImageModelSchedulabilityReasonNoAccount}, nil
	}
	return service.ImageModelCatalogEntry{Model: model, Provider: capability.Provider, MediaKind: capability.MediaKind, Capability: capability, Schedulable: true}, nil
}

func (h *ImageCanvasHandler) runtimeWorkerSnapshot() service.ImageJobWorkerSnapshot {
	if h != nil && h.jobs != nil {
		return h.jobs.WorkerSnapshot()
	}
	return service.ImageJobWorkerSnapshot{}
}

func imageJobAdminView(job *service.ImageJob) ImageJobAdminView {
	view := ImageJobAdminView{
		ID: job.PublicID, UserID: job.UserID, Status: job.Status, Operation: job.Operation, Mode: job.Mode,
		RequestedModel: job.RequestedModel, MappedModel: job.MappedModel,
		SelectedModel: job.SelectedModel, SuccessfulModel: job.SuccessfulModel,
		APIKeyID: job.APIKeyID, GroupID: job.GroupID, ProjectID: job.ProjectID,
		PolicyVersion: job.PolicyVersion, ExecutionPhase: job.ExecutionPhase,
		RequestedCount: job.RequestedCount, CompletedCount: job.CompletedCount,
		AttemptPlan: append([]string{}, job.AttemptPlan...),
		AttemptLog:  append([]service.ImageJobAttempt{}, job.AttemptLog...),
		Error:       job.Error, CreatedAt: job.CreatedAt, StartedAt: job.StartedAt,
		FinishedAt: job.FinishedAt, UpdatedAt: job.UpdatedAt,
	}
	if job.StartedAt != nil {
		end := job.FinishedAt
		if end == nil {
			now := time.Now()
			end = &now
		}
		if end.After(*job.StartedAt) {
			view.DurationMS = end.Sub(*job.StartedAt).Milliseconds()
		}
	}
	return view
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
	catalog, capabilities, err := h.listSchedulableAdminCatalog(c.Request.Context())
	if err != nil {
		writeAdminImageCanvasError(c, err)
		return
	}
	result := buildImageModelPolicyResponse(policy, capabilities)
	result.Catalog = catalog
	response.Success(c, result)
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
	catalog, capabilities, err := h.listSchedulableAdminCatalog(c.Request.Context())
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
	result := buildImageModelPolicyResponse(updated, capabilities)
	result.Catalog = catalog
	response.Success(c, result)
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
