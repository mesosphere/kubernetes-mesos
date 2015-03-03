package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	offerSubsystem = "offers"
)

var (
	OffersReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: offerSubsystem,
			Name:      "received",
			Help:      "Counter of offers received from Mesos broken out by slave host.",
		},
		[]string{"hostname"},
	)

	OffersDeclined = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: offerSubsystem,
			Name:      "declined",
			Help:      "Counter of offers declined by the framework broken out by slave host.",
		},
		[]string{"hostname"},
	)

	OffersAcquired = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: offerSubsystem,
			Name:      "acquired",
			Help:      "Counter of offers acquired for task launch broken out by slave host.",
		},
		[]string{"hostname"},
	)

	OffersReleased = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: offerSubsystem,
			Name:      "released",
			Help:      "Counter of previously-acquired offers later released, broken out by slave host.",
		},
		[]string{"hostname"},
	)
)

var registerMetrics sync.Once

func Register() {
	registerMetrics.Do(func() {
		prometheus.MustRegister(OffersReceived)
		prometheus.MustRegister(OffersDeclined)
		prometheus.MustRegister(OffersAcquired)
		prometheus.MustRegister(OffersReleased)
	})
}

func InMicroseconds(d time.Duration) float64 {
	return float64(d.Nanoseconds() / time.Microsecond.Nanoseconds())
}
