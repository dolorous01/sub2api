package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageJobCleanupExpiresOnlyAfterObjectsDeleted(t *testing.T) {
	base := &workerMemoryImageJobRepository{job: &ImageJob{ID: 7, PublicID: "imgjob_expired", Status: ImageJobStatusCompleted}}
	repo := &cleanupMemoryImageJobRepository{workerMemoryImageJobRepository: base, jobs: []*ImageJob{base.job}}
	store := &workerMemoryImageJobStore{objects: map[string][]byte{}, failDeleteIndex: -1}
	base.job.Inputs = []ImageJobInput{{ObjectKey: "image-jobs/7/input.png"}}
	base.job.Results = []ImageJobResult{{ObjectKey: "image-jobs/7/results/0.png"}, {ObjectKey: "image-jobs/7/results/1.png"}}
	for _, key := range []string{"image-jobs/7/input.png", "image-jobs/7/results/0.png", "image-jobs/7/results/1.png"} {
		store.objects[key] = []byte("image")
	}
	cleanup := NewImageJobCleanup(repo, store, &ImageJobMetrics{})

	require.NoError(t, cleanup.RunOnce(context.Background()))
	require.Empty(t, store.KeysWithPrefix("image-jobs/7/"))
	require.Equal(t, ImageJobStatusExpired, base.job.Status)
	require.Equal(t, 1, repo.markCalls)
}

func TestImageJobCleanupKeepsStateWhenDeleteFails(t *testing.T) {
	base := &workerMemoryImageJobRepository{job: &ImageJob{ID: 8, PublicID: "imgjob_failed_delete", Status: ImageJobStatusPartial}}
	repo := &cleanupMemoryImageJobRepository{workerMemoryImageJobRepository: base, jobs: []*ImageJob{base.job}}
	store := &workerMemoryImageJobStore{objects: map[string][]byte{}, failDeleteIndex: 1}
	base.job.Results = []ImageJobResult{{ObjectKey: "image-jobs/8/results/0.png"}, {ObjectKey: "image-jobs/8/results/1.png"}}
	for _, key := range []string{"image-jobs/8/results/0.png", "image-jobs/8/results/1.png"} {
		store.objects[key] = []byte("image")
	}
	cleanup := NewImageJobCleanup(repo, store, &ImageJobMetrics{})

	require.Error(t, cleanup.RunOnce(context.Background()))
	require.Equal(t, ImageJobStatusPartial, base.job.Status)
	require.Equal(t, 0, repo.markCalls)
}

func TestImageJobCleanupPreservesPromotedCanvasAssetObject(t *testing.T) {
	assetID := int64(91)
	job := &ImageJob{
		Inputs: []ImageJobInput{{ObjectKey: "image-jobs/9/input.png"}},
		Results: []ImageJobResult{
			{ObjectKey: "image-jobs/9/results/0.png", AssetID: &assetID, AssetPublicID: "asset_91"},
			{ObjectKey: "image-jobs/9/results/1.png"},
		},
	}

	require.ElementsMatch(t, []string{
		"image-jobs/9/input.png",
		"image-jobs/9/results/1.png",
	}, imageJobCleanupObjectKeys(job))
}

type cleanupMemoryImageJobRepository struct {
	*workerMemoryImageJobRepository
	jobs      []*ImageJob
	markCalls int
}

func (r *cleanupMemoryImageJobRepository) ListExpired(context.Context, time.Time, int) ([]*ImageJob, error) {
	return r.jobs, nil
}

func (r *cleanupMemoryImageJobRepository) MarkExpired(_ context.Context, jobID int64, fromStatus ImageJobStatus, _ time.Time) error {
	r.markCalls++
	if r.job.ID != jobID || r.job.Status != fromStatus {
		return ErrImageJobConflict
	}
	r.job.Status = ImageJobStatusExpired
	return nil
}

var _ ImageJobRepository = (*cleanupMemoryImageJobRepository)(nil)
