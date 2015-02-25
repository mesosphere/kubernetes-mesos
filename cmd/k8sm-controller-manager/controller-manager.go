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
package main

// HACK(jdef): copy/pasted from k8s /cmd/controller-manager package, hacked to use
// a modified endpoint-controller

import (
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
	nodeControllerPkg "github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider/controller"
	replicationControllerPkg "github.com/GoogleCloudPlatform/kubernetes/pkg/controller"
	server "github.com/GoogleCloudPlatform/kubernetes/pkg/controllermanager"
	_ "github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/resourcequota"
	kendpoint "github.com/GoogleCloudPlatform/kubernetes/pkg/service"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/version/verflag"
	"github.com/golang/glog"

	kmendpoint "github.com/mesosphere/kubernetes-mesos/pkg/service"
	"github.com/spf13/pflag"
)

// CMServer is the mail context object for the controller manager.
type CMServer struct {
	*server.CMServer
	UseHostPortEndpoints bool
}

// NewCMServer creates a new CMServer with a default config.
func NewCMServer() *CMServer {
	s := &CMServer{
		CMServer: server.NewCMServer(),
	}
	s.CloudProvider = "mesos"
	s.UseHostPortEndpoints = true
	s.MinionRegexp = "^.*$"
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
		glog.Fatal("usage: controller-manager -master <master>")
	}

	kubeClient, err := client.New(&s.ClientConfig)
	if err != nil {
		glog.Fatalf("Invalid API configuration: %v", err)
	}

	go http.ListenAndServe(net.JoinHostPort(s.Address.String(), strconv.Itoa(s.Port)), nil)

	endpoints := s.createEndpointController(kubeClient)
	go util.Forever(func() { endpoints.SyncServiceEndpoints() }, time.Second*10)

	controllerManager := replicationControllerPkg.NewReplicationManager(kubeClient)
	controllerManager.Run(10 * time.Second)

	kubeletClient, err := client.NewKubeletClient(&s.KubeletConfig)
	if err != nil {
		glog.Fatalf("Failure to start kubelet client: %v", err)
	}

	//TODO(jdef) should eventually support more cloud providers here
	if s.CloudProvider != "mesos" {
		glog.Fatalf("Unsupported cloud provider: %v", s.CloudProvider)
	}
	cloud := cloudprovider.InitCloudProvider(s.CloudProvider, s.CloudConfigFile)

	nodeController := nodeControllerPkg.NewNodeController(cloud, s.MinionRegexp, s.MachineList,
		nil, kubeClient, kubeletClient, s.RegisterRetryCount, s.PodEvictionTimeout)
	nodeController.Run(s.NodeSyncPeriod, s.SyncNodeList)

	resourceQuotaManager := resourcequota.NewResourceQuotaManager(kubeClient)
	resourceQuotaManager.Run(s.ResourceQuotaSyncPeriod)

	select {}
}

func main() {
	s := NewCMServer()
	s.AddFlags(pflag.CommandLine)

	util.InitFlags()
	util.InitLogs()
	defer util.FlushLogs()

	verflag.PrintAndExitIfRequested()

	s.Run(pflag.CommandLine.Args())
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
