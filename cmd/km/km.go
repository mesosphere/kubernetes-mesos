//TODO(jdef) cloned from k8s cmd/hypercube: this is a binary that can morph
//into all of the other kubernetes-mesos binaries
package main

import (
	"os"
)

func main() {
	hk := HyperKube{
		Name: "km",
		Long: "This is an all-in-one binary that can run any of the various Kubernetes-Mesos servers.",
	}

	hk.AddServer(NewKubeAPIServer())
	hk.AddServer(NewControllerManager())
	hk.AddServer(NewScheduler())
	hk.AddServer(NewKubeletExecutor())
	hk.AddServer(NewKubeProxy())

	hk.RunToExit(os.Args)
}
