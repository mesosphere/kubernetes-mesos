package main

import (
	"github.com/mesosphere/kubernetes-mesos/pkg/executor/service"
)

// NewHyperkubeServer creates a new hyperkube Server object that includes the
// description and flags.
func NewKubeletExecutor() *Server {
	s := service.NewHyperKubeletExecutorServer()
	hks := Server{
		SimpleUsage: "executor",
		Long: `The kubelet-executor binary is responsible for maintaining a set of containers
on a particular node. It syncs data from a specialized Mesos source that tracks
task launches and kills. It then queries Docker to see what is currently
running.  It synchronizes the configuration data, with the running set of
containers by starting or stopping Docker containers.`,
		Run: func(hks *Server, args []string) error {
			return s.Run(hks, args)
		},
	}
	s.AddHyperkubeFlags(hks.Flags())
	return &hks
}
