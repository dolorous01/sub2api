//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageModelPolicyRepositoryReplaceUsesCASAndAudit(t *testing.T) {
	repo, operatorID := newImageModelPolicyIntegrationRepo(t)

	first, err := repo.Replace(context.Background(), 0, operatorID, true, []service.ImageModelPolicyItem{
		{Model: "model-a", Enabled: true, Position: 0},
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Version)

	_, err = repo.Replace(context.Background(), 0, operatorID, true, []service.ImageModelPolicyItem{
		{Model: "model-b", Enabled: true, Position: 0},
	})
	require.ErrorIs(t, err, service.ErrImageModelPolicyVersionConflict)

	audits, err := repo.ListAudit(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.Equal(t, int64(0), audits[0].OldVersion)
	require.Equal(t, int64(1), audits[0].NewVersion)
}

func newImageModelPolicyIntegrationRepo(t *testing.T) (service.ImageModelPolicyRepository, int64) {
	t.Helper()
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `
		DELETE FROM image_model_policy_audits;
		DELETE FROM image_model_policy_items;
		UPDATE image_model_policies SET version = 0, enabled = FALSE, updated_at = NOW() WHERE id = 1;`)
	require.NoError(t, err)

	stamp := time.Now().UnixNano()
	operator := mustCreateUser(t, integrationEntClient, &service.User{
		Email: fmt.Sprintf("image-policy-%d@example.test", stamp),
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `
			DELETE FROM image_model_policy_audits;
			DELETE FROM image_model_policy_items;
			UPDATE image_model_policies SET version = 0, enabled = FALSE, updated_at = NOW() WHERE id = 1;`)
	})
	return NewImageModelPolicyRepository(integrationDB), operator.ID
}
