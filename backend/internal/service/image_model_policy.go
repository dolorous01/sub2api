package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type ImageModelPolicyRepository interface {
	Get(ctx context.Context) (*ImageModelPolicy, error)
	Replace(ctx context.Context, expectedVersion, operatorUserID int64, enabled bool, items []ImageModelPolicyItem) (*ImageModelPolicy, error)
	ListAudit(ctx context.Context, limit int) ([]ImageModelPolicyAudit, error)
}

type ImageModelPolicyService struct {
	repository ImageModelPolicyRepository
}

func NewImageModelPolicyService(repository ImageModelPolicyRepository) *ImageModelPolicyService {
	return &ImageModelPolicyService{repository: repository}
}

func (s *ImageModelPolicyService) Get(ctx context.Context) (*ImageModelPolicy, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image model policy repository is required")
	}
	return s.repository.Get(ctx)
}

func (s *ImageModelPolicyService) Replace(
	ctx context.Context,
	expectedVersion, operatorUserID int64,
	enabled bool,
	items []ImageModelPolicyItem,
) (*ImageModelPolicy, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image model policy repository is required")
	}
	normalized, err := NormalizeImageModelPolicyItems(items)
	if err != nil {
		return nil, err
	}
	return s.repository.Replace(ctx, expectedVersion, operatorUserID, enabled, normalized)
}

func (s *ImageModelPolicyService) ListAudit(ctx context.Context, limit int) ([]ImageModelPolicyAudit, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image model policy repository is required")
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	return s.repository.ListAudit(ctx, limit)
}

func NormalizeImageModelPolicyItems(items []ImageModelPolicyItem) ([]ImageModelPolicyItem, error) {
	normalized := make([]ImageModelPolicyItem, len(items))
	copy(normalized, items)
	for index := range normalized {
		normalized[index].Model = strings.TrimSpace(normalized[index].Model)
		normalized[index].Capability = ImageModelCapability{}
	}
	if err := ValidateImageModelPolicyItems(normalized); err != nil {
		return nil, err
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].Position < normalized[right].Position
	})
	return normalized, nil
}

func ValidateImageModelPolicyItems(items []ImageModelPolicyItem) error {
	if len(items) == 0 {
		return fmt.Errorf("%w: at least one model is required", ErrImageModelPolicyInvalid)
	}
	seenModels := make(map[string]struct{}, len(items))
	seenPositions := make(map[int]struct{}, len(items))
	enabledCount := 0
	for _, item := range items {
		model := strings.TrimSpace(item.Model)
		if model == "" {
			return fmt.Errorf("%w: model must not be empty", ErrImageModelPolicyInvalid)
		}
		if _, exists := seenModels[model]; exists {
			return fmt.Errorf("%w: duplicate model %q", ErrImageModelPolicyInvalid, model)
		}
		seenModels[model] = struct{}{}
		if item.Position < 0 || item.Position >= len(items) {
			return fmt.Errorf("%w: position %d is outside the policy", ErrImageModelPolicyInvalid, item.Position)
		}
		if _, exists := seenPositions[item.Position]; exists {
			return fmt.Errorf("%w: duplicate position %d", ErrImageModelPolicyInvalid, item.Position)
		}
		seenPositions[item.Position] = struct{}{}
		if item.Enabled {
			enabledCount++
		}
	}
	if enabledCount == 0 {
		return fmt.Errorf("%w: at least one model must be enabled", ErrImageModelPolicyInvalid)
	}
	return nil
}

func BuildImageAttemptPlan(
	policy ImageModelPolicy,
	selectedModel string,
	operation ImageOperation,
	allowed map[string]ImageModelCapability,
) ([]string, error) {
	if !policy.Enabled {
		return nil, ErrNoCompatibleImageModel
	}
	items, err := NormalizeImageModelPolicyItems(policy.Items)
	if err != nil {
		return nil, err
	}
	selectedModel = strings.TrimSpace(selectedModel)
	selectedEnabled := false
	for _, item := range items {
		if item.Model == selectedModel && item.Enabled {
			selectedEnabled = true
			break
		}
	}
	selectedCapability, selectedAllowed := allowed[selectedModel]
	if !selectedEnabled || !selectedAllowed || !isImageCapability(selectedCapability) ||
		!selectedCapability.Supports(operation) {
		return nil, ErrImageModelSelectionInvalid
	}

	plan := make([]string, 0, len(items))
	plan = append(plan, selectedModel)
	for _, item := range items {
		if !item.Enabled || item.Model == selectedModel {
			continue
		}
		capability, ok := allowed[item.Model]
		if ok && isImageCapability(capability) && capability.Supports(operation) {
			plan = append(plan, item.Model)
		}
	}
	if len(plan) == 0 {
		return nil, ErrNoCompatibleImageModel
	}
	return plan, nil
}

func isImageCapability(capability ImageModelCapability) bool {
	mediaKind := strings.ToLower(strings.TrimSpace(capability.MediaKind))
	return mediaKind == "" || mediaKind == "image"
}

func (c ImageModelCapability) Supports(operation ImageOperation) bool {
	switch operation {
	case ImageOperationGeneration:
		return c.Generation
	case ImageOperationEdit:
		return c.Edit
	default:
		return false
	}
}
