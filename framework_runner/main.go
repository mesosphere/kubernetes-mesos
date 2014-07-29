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

// apiserver is the main api server and master for the cluster.
// it is responsible for serving the cluster management API.
package main

import (
	"flag"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/apiserver"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	log "github.com/golang/glog"
	"code.google.com/p/goprotobuf/proto"
	"github.com/mesosphere/mesos-go/mesos"
	"github.com/mesosphere/kubernetes-mesos/framework"
)

var (
	port                        = flag.Uint("port", 8080, "The port to listen on.  Default 8080.")
	address                     = flag.String("address", "127.0.0.1", "The address on the local server to listen to. Default 127.0.0.1")
	apiPrefix                   = flag.String("api_prefix", "/api/v1beta1", "The prefix for API requests on the server. Default '/api/v1beta1'")
	cloudProvider               = flag.String("cloud_provider", "", "The provider for cloud services.  Empty string for no provider.")
	minionRegexp                = flag.String("minion_regexp", "", "If non empty, and -cloud_provider is specified, a regular expression for matching minion VMs")
	minionPort                  = flag.Uint("minion_port", 10250, "The port at which kubelet will be listening on the minions.")
	mesosMaster                 = flag.String("mesos_master", "localhost:5050", "Location of leading Mesos master")
	executorURI                 = flag.String("executor_uri", "", "URI of dir that contains the executor executable")
	etcdServerList, machineList util.StringList
)

func init() {
	flag.Var(&etcdServerList, "etcd_servers", "Servers for the etcd (http://ip:port), comma separated")
	flag.Var(&machineList, "machines", "List of machines to schedule onto, comma separated.")
}

type kubernatesMaster struct {
	podRegistry        registry.PodRegistry
	controllerRegistry registry.ControllerRegistry
	serviceRegistry    registry.ServiceRegistry
	minionRegistry     registry.MinionRegistry

	storage map[string]apiserver.RESTStorage
}

func main() {
	flag.Parse()
	util.InitLogs()
	defer util.FlushLogs()

	if len(machineList) == 0 {
		log.Fatal("No machines specified!")
	}

	var cloud cloudprovider.Interface
	switch *cloudProvider {
	case "gce":
		var err error
		cloud, err = cloudprovider.NewGCECloud()
		if err != nil {
			log.Fatal("Couldn't connect to GCE cloud: %#v", err)
		}
	default:
		if len(*cloudProvider) > 0 {
			log.Infof("Unknown cloud provider: %s", *cloudProvider)
		} else {
			log.Info("No cloud provider specified.")
		}
	}

	executor := &mesos.ExecutorInfo{
		ExecutorId: &mesos.ExecutorID{Value: proto.String("KubeleteExecutorID")},
		Command: &mesos.CommandInfo{
			Value: proto.String("./executor_runner"),
			Uris: []*mesos.CommandInfo_URI{
				&mesos.CommandInfo_URI{Value: executorURI},
			},
		},
		Name:   proto.String("Executor for kubelete"),
		Source: proto.String("for test"),
	}

	mesosPodScheduler := framework.New(executor, framework.FCFSScheduleFunc)
	driver := &mesos.MesosSchedulerDriver{
		Master: *mesosMaster,
		Framework: mesos.FrameworkInfo{
			Name: proto.String("KubernetesFramework"),
			User: proto.String(""),
		},
		Scheduler: mesosPodScheduler,
	}
	mesosPodScheduler.Driver = driver

	driver.Init()
	defer driver.Destroy()
	go driver.Start()

	m := newKubernatesMaster(mesosPodScheduler, machineList, cloud)
	log.Fatal(m.run(net.JoinHostPort(*address, strconv.Itoa(int(*port))), *apiPrefix))
}

func newKubernatesMaster(mesosPodScheduler *framework.KubernetesFramework, minions []string, cloud cloudprovider.Interface) *kubernatesMaster {
	// TODO(yifan): Let mesos handle pod info getter.
	podInfoGetter := &client.HTTPPodInfoGetter{
		Client: http.DefaultClient,
		Port:   *minionPort,
	}

	m := &kubernatesMaster{
		podRegistry:        mesosPodScheduler,
		controllerRegistry: registry.MakeMemoryRegistry(),
		serviceRegistry:    registry.MakeMemoryRegistry(),
		minionRegistry:     registry.MakeMinionRegistry(minions),
	}
	m.init(cloud, podInfoGetter, mesosPodScheduler)
	return m
}

func (m *kubernatesMaster) init(cloud cloudprovider.Interface, podInfoGetter client.PodInfoGetter, s scheduler.Scheduler) {
	podCache := master.NewPodCache(podInfoGetter, m.podRegistry, time.Second*30)
	go podCache.Loop()
	m.storage = map[string]apiserver.RESTStorage{
		"pods": registry.MakePodRegistryStorage(m.podRegistry, podInfoGetter, s, m.minionRegistry, cloud, podCache),
		"replicationControllers": registry.MakeControllerRegistryStorage(m.controllerRegistry, m.podRegistry),
		"services":               registry.MakeServiceRegistryStorage(m.serviceRegistry, cloud, m.minionRegistry),
		"minions":                registry.MakeMinionRegistryStorage(m.minionRegistry),
	}
}

func (m *kubernatesMaster) run(myAddress, apiPrefix string) error {
	endpoints := registry.MakeEndpointController(m.serviceRegistry, m.podRegistry)
	go util.Forever(func() { endpoints.SyncServiceEndpoints() }, time.Second*10)

	s := &http.Server{
		Addr:           myAddress,
		Handler:        apiserver.New(m.storage, apiPrefix),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	return s.ListenAndServe()
}
