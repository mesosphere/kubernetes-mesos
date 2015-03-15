package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	schedulerSubsystem = "scheduler"
)

var (
	QueueWaitTime = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Subsystem: schedulerSubsystem,
			Name:      "queue_wait_time_microseconds",
			Help:      "Launch queue wait time in microseconds",
		},
	)
	BindLatency = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Subsystem: schedulerSubsystem,
			Name:      "bind_latency_microseconds",
			Help:      "Latency in microseconds between pod-task launch and pod binding.",
		},
	)
	StatusUpdates = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: schedulerSubsystem,
			Name:      "status_updates",
			Help:      "Counter of TaskStatus updates, broken out by source, reason, state.",
		},
		[]string{"source", "reason", "state"},
	)
	ReconciliationLatency = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Subsystem: schedulerSubsystem,
			Name:      "reconciliation_latency_microseconds",
			Help:      "Latency in microseconds to execute explicit task reconciliation.",
		},
	)
	ReconciliationRequested = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: schedulerSubsystem,
			Name:      "reconciliation_requested",
			Help:      "Counter of requested task reconciliations, broken out by kind.",
		},
		[]string{"kind"},
	)
	ReconciliationExecuted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: schedulerSubsystem,
			Name:      "reconciliation_executed",
			Help:      "Counter of executed task reconciliations requests, broken out by kind.",
		},
		[]string{"kind"},
	)
	ReconciliationCancelled = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: schedulerSubsystem,
			Name:      "reconciliation_cancelled",
			Help:      "Counter of cancelled task reconciliations requests, broken out by kind.",
		},
		[]string{"kind"},
	)
)

var registerMetrics sync.Once

func Register() {
	registerMetrics.Do(func() {
		prometheus.MustRegister(QueueWaitTime)
		prometheus.MustRegister(BindLatency)
		prometheus.MustRegister(StatusUpdates)
		prometheus.MustRegister(ReconciliationLatency)
		prometheus.MustRegister(ReconciliationRequested)
		prometheus.MustRegister(ReconciliationExecuted)
		prometheus.MustRegister(ReconciliationCancelled)
	})
}

func InMicroseconds(d time.Duration) float64 {
	return float64(d.Nanoseconds() / time.Microsecond.Nanoseconds())
}
