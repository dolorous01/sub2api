package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type imageModelCatalogAPIKeyRepo struct {
	APIKeyRepository
	keys []APIKey
}

func (r imageModelCatalogAPIKeyRepo) GetByID(_ context.Context, id int64) (*APIKey, error) {
	for index := range r.keys {
		if r.keys[index].ID == id {
			key := r.keys[index]
			return &key, nil
		}
	}
	return nil, ErrAPIKeyNotFound
}

func (r imageModelCatalogAPIKeyRepo) ListByUserID(_ context.Context, userID int64, _ pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	keys := make([]APIKey, 0)
	for _, key := range r.keys {
		if key.UserID != userID || (filters.Status != "" && key.Status != filters.Status) {
			continue
		}
		keys = append(keys, key)
	}
	return keys, &pagination.PaginationResult{}, nil
}

type imageModelCatalogGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

func (r imageModelCatalogGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	group, ok := r.groups[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	copy := *group
	return &copy, nil
}

type imageModelCatalogAccountRepo struct {
	AccountRepository
	byGroup map[int64][]Account
}

func (r imageModelCatalogAccountRepo) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]Account, error) {
	return append([]Account(nil), r.byGroup[groupID]...), nil
}

func (r imageModelCatalogAccountRepo) ListSchedulable(_ context.Context) ([]Account, error) {
	var accounts []Account
	for _, grouped := range r.byGroup {
		accounts = append(accounts, grouped...)
	}
	return accounts, nil
}

func TestImageModelCatalogFiltersPolicyByOwnedAPIKeyGroup(t *testing.T) {
	groupID := int64(3)
	group := &Group{
		ID: groupID, Name: "images", Status: StatusActive, AllowImageGeneration: true,
		ModelsListConfig: GroupModelsListConfig{Enabled: true, Models: []string{"model-a", "model-c"}},
	}
	key := APIKey{ID: 42, UserID: 9, Name: "canvas", GroupID: &groupID, Group: group, Status: StatusAPIKeyActive}
	account := Account{
		ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"model-a": "gpt-image-2", "model-b": "gpt-image-2", "model-c": "gpt-image-2"},
			"image_model_capabilities": map[string]any{
				"model-a": map[string]any{"generation": true},
				"model-b": map[string]any{"generation": true},
				"model-c": map[string]any{"generation": true, "edit": true},
			},
		},
	}
	catalog := NewImageModelCatalog(
		imageModelCatalogAPIKeyRepo{keys: []APIKey{key}},
		imageModelCatalogGroupRepo{groups: map[int64]*Group{groupID: group}},
		imageModelCatalogAccountRepo{byGroup: map[int64][]Account{groupID: {account}}},
		&config.Config{},
	)

	models, err := catalog.ForAPIKey(context.Background(), 9, 42)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"model-a", "model-c"}, imageModelCatalogNames(models))
	require.True(t, models["model-c"].Edit)

	_, err = catalog.ForAPIKey(context.Background(), 10, 42)
	require.ErrorIs(t, err, ErrImageCanvasAPIKeyNotFound)
}

func TestImageModelCatalogListsRedactedAvailableKeys(t *testing.T) {
	groupID := int64(3)
	group := &Group{ID: groupID, Name: "images", Status: StatusActive, AllowImageGeneration: true}
	keys := []APIKey{
		{ID: 42, UserID: 9, Key: "sk-secret", Name: "zeta", GroupID: &groupID, Group: group, Status: StatusAPIKeyActive},
		{ID: 43, UserID: 9, Key: "sk-disabled", Name: "disabled", GroupID: &groupID, Group: group, Status: StatusAPIKeyDisabled},
	}
	catalog := NewImageModelCatalog(
		imageModelCatalogAPIKeyRepo{keys: keys},
		imageModelCatalogGroupRepo{groups: map[int64]*Group{groupID: group}},
		imageModelCatalogAccountRepo{},
		nil,
	)

	got, err := catalog.ListOwnedAPIKeys(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, []ImageCanvasAPIKey{{ID: 42, Name: "zeta", GroupID: 3, GroupName: "images"}}, got)
}

func TestAccountImageCanvasModelCatalogRequiresNativeImageAccount(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	require.Empty(t, oauth.ImageCanvasModelCatalog(4, 4))

	apiKey := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	models := apiKey.ImageCanvasModelCatalog(4, 4)
	require.Contains(t, models, "gpt-image-2")
	require.True(t, models["gpt-image-2"].Generation)
	require.True(t, models["gpt-image-2"].Edit)
}

func TestAccountImageCanvasModelCatalogFiltersUnsupportedMediaProtocols(t *testing.T) {
	openAI := &Account{
		Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"mapped-video": "grok-imagine-video",
			"mapped-audio": "gpt-4o-mini-tts",
		}},
	}
	openAIModels := openAI.ImageCanvasModelCatalog(4, 4)
	require.NotContains(t, openAIModels, "mapped-video")
	require.Equal(t, "audio", openAIModels["mapped-audio"].MediaKind)

	grok := &Account{
		Platform: PlatformGrok, Type: AccountTypeOAuth,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"mapped-video": "grok-imagine-video",
			"mapped-audio": "gpt-4o-mini-tts",
		}},
	}
	grokModels := grok.ImageCanvasModelCatalog(4, 4)
	require.Equal(t, "video", grokModels["mapped-video"].MediaKind)
	require.NotContains(t, grokModels, "mapped-audio")
}

func imageModelCatalogNames(models map[string]ImageModelCapability) []string {
	names := make([]string, 0, len(models))
	for model := range models {
		names = append(names, model)
	}
	return names
}
