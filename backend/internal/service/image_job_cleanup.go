package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// ImageJobCleanup removes private input/result objects after their retention
// window. A job is marked expired only after every referenced object has been
// deleted successfully, which makes deletion retryable and idempotent.
type ImageJobCleanup struct {
	repo    ImageJobRepository
	store   ImageJobObjectStore
	metrics *ImageJobMetrics
	limit   int
}

func NewImageJobCleanup(repo ImageJobRepository, store ImageJobObjectStore, metrics *ImageJobMetrics, limits ...int) *ImageJobCleanup {
	limit := 100
	if len(limits) > 0 && limits[0] > 0 {
		limit = limits[0]
	}
	return &ImageJobCleanup{repo: repo, store: store, metrics: metrics, limit: limit}
}

func (c *ImageJobCleanup) RunOnce(ctx context.Context) error {
	if c == nil || c.repo == nil || c.store == nil {
		return fmt.Errorf("image job cleanup dependencies are unavailable")
	}
	jobs, err := c.repo.ListExpired(ctx, timezone.Now(), c.limit)
	if err != nil {
		return err
	}
	var runErr error
	for _, job := range jobs {
		if job == nil {
			continue
		}
		keys := imageJobCleanupObjectKeys(job)
		var jobErr error
		for _, key := range keys {
			if err := c.store.Delete(ctx, key); err != nil {
				if c.metrics != nil {
					c.metrics.StorageError()
				}
				jobErr = errors.Join(jobErr, fmt.Errorf("delete image job object %q: %w", key, err))
			}
		}
		if jobErr != nil {
			runErr = errors.Join(runErr, jobErr)
			continue
		}
		if err := c.repo.MarkExpired(ctx, job.ID, job.Status, timezone.Now()); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("mark image job %s expired: %w", job.PublicID, err))
		}
	}
	return runErr
}

func imageJobCleanupObjectKeys(job *ImageJob) []string {
	if job == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(job.Inputs)+len(job.Results))
	keys := make([]string, 0, len(job.Inputs)+len(job.Results))
	add := func(key string) {
		if key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, input := range job.Inputs {
		add(input.ObjectKey)
	}
	for _, result := range job.Results {
		// Canvas results are promoted to permanent assets and share the same
		// object key. Expiring job metadata must not delete the asset bytes.
		if result.AssetID != nil || result.AssetPublicID != "" {
			continue
		}
		add(result.ObjectKey)
	}
	return keys
}

// ImageJobMaintenanceIntervals centralizes the process-local maintenance
// cadence so tests and lifecycle wiring can use the same defaults.
func ImageJobMaintenanceIntervals(_ *config.Config) (time.Duration, time.Duration) {
	return time.Minute, 5 * time.Minute
}
