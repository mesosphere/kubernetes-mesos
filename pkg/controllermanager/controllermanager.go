/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// The controller manager is responsible for monitoring replication
// controllers, and creating corresponding pods to achieve the desired
// state.  It uses the API to listen for new controllers and to create/delete
// pods.
package controllermanager

// HACK(jdef): copy/pasted from k8s /cmd/controller-manager package, hacked to use
// a modified endpoint-controller

import (
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/cmd/kube-controller-manager/app"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/resource"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
	nodeControllerPkg "github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider/controller"
	replicationControllerPkg "github.com/GoogleCloudPlatform/kubernetes/pkg/controller"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/namespace"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/resourcequota"
	kendpoint "github.com/GoogleCloudPlatform/kubernetes/pkg/service"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/golang/glog"

	"github.com/mesosphere/kubernetes-mesos/pkg/cloud/mesos"
	"github.com/mesosphere/kubernetes-mesos/pkg/profile"
	kmendpoint "github.com/mesosphere/kubernetes-mesos/pkg/service"
	"github.com/spf13/pflag"
)

// CMServer is the mail context object for the controller manager.
type CMServer struct {
	*app.CMServer
	UseHostPortEndpoints bool
}

// NewCMServer creates a new CMServer with a default config.
func NewCMServer() *CMServer {
	s := &CMServer{
		CMServer: app.NewCMServer(),
	}
	s.CloudProvider = mesos.PluginName
	s.UseHostPortEndpoints = true
	s.MinionRegexp = "^.*$"
	// if the kubelet isn't running on a slave we don't want the resources to show up on the node
	s.NodeMilliCPU = 0
	s.NodeMemory = *resource.NewQuantity(0, resource.BinarySI)
	return s
}

// AddFlags adds flags for a specific CMServer to the specified FlagSet
func (s *CMServer) AddFlags(fs *pflag.FlagSet) {
	s.CMServer.AddFlags(fs)
	fs.BoolVar(&s.UseHostPortEndpoints, "host_port_endpoints", s.UseHostPortEndpoints, "Map service endpoints to hostIP:hostPort instead of podIP:containerPort. Default true.")
}

func (s *CMServer) verifyMinionFlags() {
	if !s.SyncNodeList && s.MinionRegexp != "" {
		glog.Info("--minion_regexp is ignored by --sync_nodes=false")
	}
	if s.CloudProvider == "" || s.MinionRegexp == "" {
		if len(s.MachineList) == 0 {
			glog.Info("No machines specified!")
		}
		return
	}
	if len(s.MachineList) != 0 {
		glog.Info("--machines is overwritten by --minion_regexp")
	}
}

func (s *CMServer) Run(_ []string) error {
	s.verifyMinionFlags()
	if len(s.ClientConfig.Host) == 0 {
		glog.Fatal("usage: controller-manager --master <master>")
	}

	kubeClient, err := client.New(&s.ClientConfig)
	if err != nil {
		glog.Fatalf("Invalid API configuration: %v", err)
	}

	endpoints := s.createEndpointController(kubeClient)
	go util.Forever(func() { endpoints.SyncServiceEndpoints() }, time.Second*10)

	controllerManager := replicationControllerPkg.NewReplicationManager(kubeClient)
	controllerManager.Run(replicationControllerPkg.DefaultSyncPeriod)

	kubeletClient, err := client.NewKubeletClient(&s.KubeletConfig)
	if err != nil {
		glog.Fatalf("Failure to start kubelet client: %v", err)
	}

	//TODO(jdef) should eventually support more cloud providers here
	if s.CloudProvider != mesos.PluginName {
		glog.Fatalf("Unsupported cloud provider: %v", s.CloudProvider)
	}
	cloud := cloudprovider.InitCloudProvider(s.CloudProvider, s.CloudConfigFile)

	//this is the default "static" capacity reported when there's no kubelet running on a slave
	nodeResources := &api.NodeResources{
		Capacity: api.ResourceList{
			api.ResourceCPU:    *resource.NewMilliQuantity(s.NodeMilliCPU, resource.DecimalSI),
			api.ResourceMemory: s.NodeMemory,
		},
	}

	nodeController := nodeControllerPkg.NewNodeController(cloud, s.MinionRegexp, s.MachineList, nodeResources,
		kubeClient, kubeletClient, record.FromSource(api.EventSource{Component: "controllermanager"}),
		s.RegisterRetryCount, s.PodEvictionTimeout)
	nodeController.Run(s.NodeSyncPeriod, s.SyncNodeList, s.SyncNodeStatus)

	resourceQuotaManager := resourcequota.NewResourceQuotaManager(kubeClient)
	resourceQuotaManager.Run(s.ResourceQuotaSyncPeriod)

	namespaceManager := namespace.NewNamespaceManager(kubeClient)
	namespaceManager.Run(s.NamespaceSyncPeriod)

	go func() {
		mux := http.NewServeMux()
		if s.EnableProfiling {
			profile.InstallHandler(mux)
		}
		controllers := []interface{} { controllerManager, nodeController }
		checkers := make([]healthz.HealthzChecker, len(controllers))
		for _, checkme := range controllers {
			checker, ok := checkme.(healthz.HealthzChecker)
			if ok {
				checkers = append(checkers, checker)
			}
		}

		healthz.InstallHandler(mux, checkers...)
		util.Forever(func() { http.ListenAndServe(net.JoinHostPort(s.Address.String(), strconv.Itoa(s.Port)), mux) }, 5*time.Second)
	}()

	select {}
}

func (s *CMServer) createEndpointController(client *client.Client) kmendpoint.EndpointController {
	if s.UseHostPortEndpoints {
		glog.V(2).Infof("Creating hostIP:hostPort endpoint controller")
		return kmendpoint.NewEndpointController(client)
	}
	glog.V(2).Infof("Creating podIP:containerPort endpoint controller")
	stockEndpointController := kendpoint.NewEndpointController(client)
	return stockEndpointController
}
