package runtime

import (
	"sync"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	runtimeSubsystem = "runtime"
)

var (
	panicCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Subsystem: runtimeSubsystem,
			Name:      "panics",
			Help:      "Counter of panics handled by the internal crash handler.",
		},
	)
)

var registerMetrics sync.Once

func Register() {
	registerMetrics.Do(func() {
		prometheus.MustRegister(panicCounter)
		util.PanicHandlers = append(util.PanicHandlers, func(interface{}) { panicCounter.Inc() })
	})
}
