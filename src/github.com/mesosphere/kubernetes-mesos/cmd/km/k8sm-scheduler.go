package main

import (
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/service"
)

// NewScheduler creates a new hyperkube Server object that includes the
// description and flags.
func NewScheduler() *Server {
	s := service.NewSchedulerServer()

	hks := Server{
		SimpleUsage: "scheduler",
		Long: `Implements the Kubernetes-Mesos scheduler. This will launch Mesos tasks which
results in pods assigned to kubelets based on capacity and constraints.`,
		Run: func(hks *Server, args []string) error {
			return s.Run(hks, args)
		},
	}
	s.AddHyperkubeFlags(hks.Flags())
	return &hks
}
