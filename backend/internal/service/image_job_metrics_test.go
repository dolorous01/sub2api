package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageJobMetricsSnapshot(t *testing.T) {
	metrics := &ImageJobMetrics{}
	metrics.JobCreated("batch")
	metrics.JobClaimed(250 * time.Millisecond)
	metrics.JobFinished(ImageJobStatusCompleted, 4, 2*time.Second, 0.15)
	metrics.AccountSwitched()
	metrics.StorageError()

	snapshot := metrics.Snapshot()
	require.Equal(t, uint64(1), snapshot.CreatedBatch)
	require.Equal(t, int64(0), snapshot.Queued)
	require.Equal(t, uint64(1), snapshot.Started)
	require.Equal(t, uint64(1), snapshot.Completed)
	require.Equal(t, uint64(4), snapshot.Outputs)
	require.Equal(t, uint64(250), snapshot.QueueDurationMilliseconds)
	require.Equal(t, uint64(1), snapshot.QueueDurationSamples)
	require.Equal(t, uint64(2000), snapshot.ExecutionDurationMilliseconds)
	require.Equal(t, uint64(1), snapshot.ExecutionDurationSamples)
	require.Equal(t, int64(150000), snapshot.ReservationDeltaMicroUSD)
	require.Equal(t, uint64(1), snapshot.AccountSwitches)
	require.Equal(t, uint64(1), snapshot.StorageErrors)
}
