package main

import (
	"github.com/mesosphere/kubernetes-mesos/pkg/controllermanager"
)

// NewHyperkubeServer creates a new hyperkube Server object that includes the
// description and flags.
func NewControllerManager() *Server {
	s := controllermanager.NewCMServer()

	hks := Server{
		SimpleUsage: "controller-manager",
		Long:        "A server that runs a set of active components. This includes replication controllers, service endpoints and nodes.",
		Run: func(_ *Server, args []string) error {
			return s.Run(args)
		},
	}
	s.AddFlags(hks.Flags())
	return &hks
}
