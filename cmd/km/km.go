//TODO(jdef) cloned from k8s cmd/hypercube: this is a binary that can morph
//into all of the other kubernetes-mesos binaries
package main

import (
	"os"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/hyperkube"
	apiserver "github.com/GoogleCloudPlatform/kubernetes/pkg/master/server"
	proxy "github.com/GoogleCloudPlatform/kubernetes/pkg/proxy/server"

	"github.com/mesosphere/kubernetes-mesos/pkg/controllermanager"
	kexec "github.com/mesosphere/kubernetes-mesos/pkg/executor/service"
	sched "github.com/mesosphere/kubernetes-mesos/pkg/scheduler/service"
)

func main() {
	hk := hyperkube.HyperKube{
		Name: "km",
		Long: "This is an all-in-one binary that can run any of the various Kubernetes-Mesos servers.",
	}

	hk.AddServer(apiserver.NewHyperkubeServer())
	hk.AddServer(controllermanager.NewHyperkubeServer())
	hk.AddServer(sched.NewHyperkubeServer())
	hk.AddServer(kexec.NewHyperkubeServer())
	hk.AddServer(proxy.NewHyperkubeServer())

	hk.RunToExit(os.Args)
}
