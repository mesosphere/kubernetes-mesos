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
	"strings"
	"time"
	"fmt"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/apiserver"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry"
	kscheduler "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/coreos/go-etcd/etcd"
	log "github.com/golang/glog"
	"github.com/mesosphere/kubernetes-mesos/scheduler"
	"github.com/mesosphere/kubernetes-mesos/mesospodinfogetter"
	"github.com/mesosphere/mesos-go/mesos"
)

var (
	port                        = flag.Uint("port", 8080, "The port to listen on.  Default 8080.")
	address                     = flag.String("address", "127.0.0.1", "The address on the local server to listen to. Default 127.0.0.1")
	apiPrefix                   = flag.String("api_prefix", "/api/v1beta1", "The prefix for API requests on the server. Default '/api/v1beta1'")
	mesosMaster                 = flag.String("mesos_master", "localhost:5050", "Location of leading Mesos master")
	executorPath                = flag.String("executor_path", "", "Location of the kubernetes executor executable")
	proxyPath                   = flag.String("proxy_path", "", "Location of the kubernetes proxy executable")
	etcdServerList, machineList util.StringList
)

const (
	artifactPort =     9000
	cachePeriod =      10 * time.Second
	syncPeriod =       30 * time.Second
	httpReadTimeout =  10 * time.Second
	httpWriteTimeout = 10 * time.Second
)

func init() {
	flag.Var(&etcdServerList, "etcd_servers", "Servers for the etcd (http://ip:port), comma separated")
	flag.Var(&machineList, "machines", "List of machines to schedule onto, comma separated.")
}

type kubernetesMaster struct {
	podRegistry        registry.PodRegistry
	controllerRegistry registry.ControllerRegistry
	serviceRegistry    registry.ServiceRegistry
	minionRegistry     registry.MinionRegistry
	storage            map[string]apiserver.RESTStorage
	client             *client.Client
}

// Copied from cmd/apiserver.go
func main() {
	flag.Parse()
	util.InitLogs()
	defer util.FlushLogs()

	if len(machineList) == 0 {
		log.Fatal("No machines specified!")
	}

	serveExecutorArtifact := func(path string) string {
		serveFile := func(pattern string, filename string){
			http.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
				http.ServeFile(w, r, filename)
			})
		}

		// Create base path (http://foobar:5000/<base>)
		pathSplit := strings.Split(path, "/")
		var base string
		if len(pathSplit) > 0 {
			base = pathSplit[len(pathSplit) - 1]
		} else {
			base = path
		}
		serveFile("/" + base, path)

		hostURI := fmt.Sprintf("http://%s:%d/%s", *address, artifactPort, base)
		log.V(2).Infof("Hosting artifact '%s' at '%s'", path, hostURI)

		return hostURI
	}

	executorURI := serveExecutorArtifact(*executorPath)
	proxyURI := serveExecutorArtifact(*proxyPath)

	go http.ListenAndServe(":9000", nil)

	client := client.New("http://"+net.JoinHostPort(*address, strconv.Itoa(int(*port))), nil)

	executorCommand := "./kubernetes-executor"
	if len(etcdServerList) > 0 {
		etcdServerArguments := strings.Join(etcdServerList, ",")
		executorCommand = "./kubernetes-executor -v=2 -etcd_servers=" + etcdServerArguments
	}

	// Create mesos scheduler driver.
	executor := &mesos.ExecutorInfo{
		ExecutorId: &mesos.ExecutorID{Value: proto.String("KubeleteExecutorID")},
		Command: &mesos.CommandInfo{
			Value: proto.String(executorCommand),
			Uris: []*mesos.CommandInfo_URI{
				&mesos.CommandInfo_URI{Value: &executorURI},
				&mesos.CommandInfo_URI{Value: &proxyURI},
			},
		},
		Name:   proto.String("Kubelet Executor"),
		Source: proto.String("kubernetes"),
	}

	mesosPodScheduler := scheduler.New(executor, scheduler.FCFSScheduleFunc)
	driver := &mesos.MesosSchedulerDriver{
		Master: *mesosMaster,
		Framework: mesos.FrameworkInfo{
			Name: proto.String("KubernetesScheduler"),
			User: proto.String("root"),
		},
		Scheduler: mesosPodScheduler,
	}
	mesosPodScheduler.Driver = driver

	driver.Init()
	defer driver.Destroy()
	go driver.Start()

	log.V(2).Info("Serving executor artifacts...")

	podInfoGetter := MesosPodInfoGetter.New(mesosPodScheduler)

	var cloud cloudprovider.Interface
	m := newKubernetesMaster(mesosPodScheduler, &master.Config{
		Client:        client,
		Cloud:         cloud,
		Minions:       machineList,
		PodInfoGetter: podInfoGetter,
		EtcdServers:   etcdServerList,
	})
	log.Fatal(m.run(net.JoinHostPort(*address, strconv.Itoa(int(*port))), *apiPrefix))
}

func newKubernetesMaster(scheduler *scheduler.KubernetesScheduler, c *master.Config) *kubernetesMaster {
	var m *kubernetesMaster

	if len(c.EtcdServers) > 0 {
		etcdClient := etcd.NewClient(c.EtcdServers)
		minionRegistry := registry.MakeMinionRegistry(c.Minions)
		m = &kubernetesMaster{
			podRegistry:        scheduler,
			controllerRegistry: registry.MakeEtcdRegistry(etcdClient, minionRegistry),
			serviceRegistry:    registry.MakeEtcdRegistry(etcdClient, minionRegistry),
			minionRegistry:     minionRegistry,
			client:             c.Client,
		}
		m.init(scheduler, c.Cloud, c.PodInfoGetter)
	} else {
		m = &kubernetesMaster{
			podRegistry:        scheduler,
			controllerRegistry: registry.MakeMemoryRegistry(),
			serviceRegistry:    registry.MakeMemoryRegistry(),
			minionRegistry:     registry.MakeMinionRegistry(c.Minions),
			client:             c.Client,
		}
		m.init(scheduler, c.Cloud, c.PodInfoGetter)
	}
	return m
}

func (m *kubernetesMaster) init(scheduler kscheduler.Scheduler, cloud cloudprovider.Interface, podInfoGetter client.PodInfoGetter) {
	podCache := master.NewPodCache(podInfoGetter, m.podRegistry, cachePeriod)
	go podCache.Loop()
	m.storage = map[string]apiserver.RESTStorage{
		"pods": registry.MakePodRegistryStorage(m.podRegistry, podInfoGetter, scheduler, m.minionRegistry, cloud, podCache),
		"replicationControllers": registry.NewControllerRegistryStorage(m.controllerRegistry, m.podRegistry),
		"services":               registry.MakeServiceRegistryStorage(m.serviceRegistry, cloud, m.minionRegistry),
		"minions":                registry.MakeMinionRegistryStorage(m.minionRegistry),
	}
}

// Run begins serving the Kubernetes API. It never returns.
func (m *kubernetesMaster) run(myAddress, apiPrefix string) error {
	endpoints := registry.MakeEndpointController(m.serviceRegistry, m.client)
	go util.Forever(func() { endpoints.SyncServiceEndpoints() }, syncPeriod)

	s := &http.Server{
		Addr:           myAddress,
		Handler:        apiserver.New(m.storage, apiPrefix),
		ReadTimeout:    httpReadTimeout,
		WriteTimeout:   httpWriteTimeout,
		MaxHeaderBytes: 1 << 20,
	}
	return s.ListenAndServe()
}
