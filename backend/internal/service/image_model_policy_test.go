package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageModelPolicyBuildAttemptPlan(t *testing.T) {
	policy := ImageModelPolicy{Version: 7, Enabled: true, Items: []ImageModelPolicyItem{
		{Model: "model-a", Enabled: true, Position: 0},
		{Model: "model-b", Enabled: true, Position: 1},
		{Model: "model-c", Enabled: true, Position: 2},
	}}
	allowed := map[string]ImageModelCapability{
		"model-a": {Generation: true, Edit: false},
		"model-b": {Generation: true, Edit: true},
		"model-c": {Generation: true, Edit: true},
	}

	plan, err := BuildImageAttemptPlan(policy, "model-b", ImageOperationEdit, allowed)

	require.NoError(t, err)
	require.Equal(t, []string{"model-b", "model-c"}, plan)
}

func TestImageModelPolicyBuildAttemptPlanKeepsAdminOrderAfterSelected(t *testing.T) {
	policy := ImageModelPolicy{Enabled: true, Items: []ImageModelPolicyItem{
		{Model: "model-a", Enabled: true, Position: 0},
		{Model: "model-b", Enabled: true, Position: 1},
		{Model: "model-c", Enabled: true, Position: 2},
	}}
	allowed := map[string]ImageModelCapability{
		"model-a": {Generation: true},
		"model-b": {Generation: true},
		"model-c": {Generation: true},
	}

	plan, err := BuildImageAttemptPlan(policy, "model-c", ImageOperationGeneration, allowed)

	require.NoError(t, err)
	require.Equal(t, []string{"model-c", "model-a", "model-b"}, plan)
}

func TestImageModelPolicyRejectsDuplicateModels(t *testing.T) {
	err := ValidateImageModelPolicyItems([]ImageModelPolicyItem{
		{Model: "model-a", Enabled: true, Position: 0},
		{Model: "model-a", Enabled: true, Position: 1},
	})

	require.ErrorIs(t, err, ErrImageModelPolicyInvalid)
}

func TestImageModelPolicyRejectsPositionGap(t *testing.T) {
	err := ValidateImageModelPolicyItems([]ImageModelPolicyItem{
		{Model: "model-a", Enabled: true, Position: 0},
		{Model: "model-b", Enabled: true, Position: 2},
	})

	require.ErrorIs(t, err, ErrImageModelPolicyInvalid)
}

func TestImageModelPolicyRejectsUnavailableSelection(t *testing.T) {
	policy := ImageModelPolicy{Enabled: true, Items: []ImageModelPolicyItem{
		{Model: "model-a", Enabled: true, Position: 0},
	}}
	_, err := BuildImageAttemptPlan(policy, "model-a", ImageOperationGeneration, map[string]ImageModelCapability{})

	require.True(t, errors.Is(err, ErrImageModelSelectionInvalid))
}
