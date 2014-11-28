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
	"flag"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/controller"
	_ "github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	masterPkg "github.com/GoogleCloudPlatform/kubernetes/pkg/master"
	kendpoint "github.com/GoogleCloudPlatform/kubernetes/pkg/service"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/version/verflag"
	"github.com/golang/glog"

	kmendpoint "github.com/mesosphere/kubernetes-mesos/service"
)

var (
	port                 = flag.Int("port", masterPkg.ControllerManagerPort, "The port that the controller-manager's http service runs on")
	address              = util.IP(net.ParseIP("127.0.0.1"))
	useHostPortEndpoints = flag.Bool("host_port_endpoints", true, "Map service endpoints to hostIP:hostPort instead of podIP:containerPort. Default true.")
	clientConfig         = &client.Config{}
)

func init() {
	flag.Var(&address, "address", "The IP address to serve on (set to 0.0.0.0 for all interfaces)")
	client.BindClientConfigFlags(flag.CommandLine, clientConfig)
}

func main() {
	flag.Parse()
	util.InitLogs()
	defer util.FlushLogs()

	verflag.PrintAndExitIfRequested()

	if len(clientConfig.Host) == 0 {
		glog.Fatal("usage: controller-manager -master <master>")
	}

	kubeClient, err := client.New(clientConfig)
	if err != nil {
		glog.Fatalf("Invalid API configuration: %v", err)
	}

	go http.ListenAndServe(net.JoinHostPort(address.String(), strconv.Itoa(*port)), nil)

	endpoints := createEndpointController(kubeClient)
	go util.Forever(func() { endpoints.SyncServiceEndpoints() }, time.Second*10)

	controllerManager := controller.NewReplicationManager(kubeClient)
	controllerManager.Run(10 * time.Second)

	select {}
}

func createEndpointController(client *client.Client) kmendpoint.EndpointController {
	if *useHostPortEndpoints {
		glog.V(2).Infof("Creating hostIP:hostPort endpoint controller")
		return kmendpoint.NewEndpointController(client)
	}
	glog.V(2).Infof("Creating podIP:containerPort endpoint controller")
	stockEndpointController := kendpoint.NewEndpointController(client)
	return stockEndpointController
}
