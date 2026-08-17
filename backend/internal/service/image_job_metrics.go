package service

import (
	"math"
	"sync/atomic"
	"time"
)

type ImageJobMetrics struct {
	createdBatch        atomic.Uint64
	createdSequence     atomic.Uint64
	queued              atomic.Int64
	started             atomic.Uint64
	completed           atomic.Uint64
	partial             atomic.Uint64
	failed              atomic.Uint64
	indeterminate       atomic.Uint64
	canceled            atomic.Uint64
	outputs             atomic.Uint64
	queueDurationMS     atomic.Uint64
	queueSamples        atomic.Uint64
	executionDurationMS atomic.Uint64
	executionSamples    atomic.Uint64
	storageErrors       atomic.Uint64
	accountSwitches     atomic.Uint64
	reservationDelta    atomic.Int64
}

type ImageJobMetricsSnapshot struct {
	CreatedBatch                  uint64 `json:"created_batch"`
	CreatedSequence               uint64 `json:"created_sequence"`
	Queued                        int64  `json:"queued"`
	Started                       uint64 `json:"started"`
	Completed                     uint64 `json:"completed"`
	Partial                       uint64 `json:"partial"`
	Failed                        uint64 `json:"failed"`
	Indeterminate                 uint64 `json:"indeterminate"`
	Canceled                      uint64 `json:"canceled"`
	Outputs                       uint64 `json:"outputs"`
	QueueDurationMilliseconds     uint64 `json:"queue_duration_milliseconds"`
	QueueDurationSamples          uint64 `json:"queue_duration_samples"`
	ExecutionDurationMilliseconds uint64 `json:"execution_duration_milliseconds"`
	ExecutionDurationSamples      uint64 `json:"execution_duration_samples"`
	StorageErrors                 uint64 `json:"storage_errors"`
	AccountSwitches               uint64 `json:"account_switches"`
	ReservationDeltaMicroUSD      int64  `json:"reservation_delta_micro_usd"`
}

func (m *ImageJobMetrics) JobCreated(mode string) {
	if m == nil {
		return
	}
	if mode == "sequence" {
		m.createdSequence.Add(1)
	} else {
		m.createdBatch.Add(1)
	}
	m.queued.Add(1)
}

func (m *ImageJobMetrics) JobClaimed(queueDuration time.Duration) {
	if m == nil {
		return
	}
	m.queued.Add(-1)
	m.started.Add(1)
	m.queueDurationMS.Add(durationMilliseconds(queueDuration))
	m.queueSamples.Add(1)
}

func (m *ImageJobMetrics) JobFinished(status ImageJobStatus, outputs int, duration time.Duration, reservationDeltaUSD float64) {
	if m == nil {
		return
	}
	switch status {
	case ImageJobStatusCompleted:
		m.completed.Add(1)
	case ImageJobStatusPartial:
		m.partial.Add(1)
	case ImageJobStatusFailed:
		m.failed.Add(1)
	case ImageJobStatusIndeterminate:
		m.indeterminate.Add(1)
	case ImageJobStatusCanceled:
		m.canceled.Add(1)
	}
	if outputs > 0 {
		m.outputs.Add(uint64(outputs))
	}
	m.executionDurationMS.Add(durationMilliseconds(duration))
	m.executionSamples.Add(1)
	if !math.IsNaN(reservationDeltaUSD) && !math.IsInf(reservationDeltaUSD, 0) {
		m.reservationDelta.Add(int64(math.Round(reservationDeltaUSD * 1_000_000)))
	}
}

func (m *ImageJobMetrics) AccountSwitched() {
	if m != nil {
		m.accountSwitches.Add(1)
	}
}

func (m *ImageJobMetrics) StorageError() {
	if m != nil {
		m.storageErrors.Add(1)
	}
}

func (m *ImageJobMetrics) Snapshot() ImageJobMetricsSnapshot {
	if m == nil {
		return ImageJobMetricsSnapshot{}
	}
	return ImageJobMetricsSnapshot{
		CreatedBatch:                  m.createdBatch.Load(),
		CreatedSequence:               m.createdSequence.Load(),
		Queued:                        m.queued.Load(),
		Started:                       m.started.Load(),
		Completed:                     m.completed.Load(),
		Partial:                       m.partial.Load(),
		Failed:                        m.failed.Load(),
		Indeterminate:                 m.indeterminate.Load(),
		Canceled:                      m.canceled.Load(),
		Outputs:                       m.outputs.Load(),
		QueueDurationMilliseconds:     m.queueDurationMS.Load(),
		QueueDurationSamples:          m.queueSamples.Load(),
		ExecutionDurationMilliseconds: m.executionDurationMS.Load(),
		ExecutionDurationSamples:      m.executionSamples.Load(),
		StorageErrors:                 m.storageErrors.Load(),
		AccountSwitches:               m.accountSwitches.Load(),
		ReservationDeltaMicroUSD:      m.reservationDelta.Load(),
	}
}

func durationMilliseconds(duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}
	return uint64(duration / time.Millisecond)
}
