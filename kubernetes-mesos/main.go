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
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/apiserver"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/controller"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/endpoint"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/etcd"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/minion"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/pod"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/service"

	kscheduler "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	goetcd "github.com/coreos/go-etcd/etcd"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	kmscheduler "github.com/mesosphere/kubernetes-mesos/scheduler"
)

var (
	port                        = flag.Uint("port", 8080, "The port to listen on.  Default 8080.")
	address                     = flag.String("address", "127.0.0.1", "The address on the local server to listen to. Default 127.0.0.1")
	apiPrefix                   = flag.String("api_prefix", "/api/v1beta1", "The prefix for API requests on the server. Default '/api/v1beta1'")
	mesosMaster                 = flag.String("mesos_master", "localhost:5050", "Location of leading Mesos master")
	executorPath                = flag.String("executor_path", "", "Location of the kubernetes executor executable")
	proxyPath                   = flag.String("proxy_path", "", "Location of the kubernetes proxy executable")
	minionPort                  = flag.Uint("minion_port", 10250, "The port at which kubelet will be listening on the minions.")
	etcdServerList, machineList util.StringList
)

const (
	artifactPort     = 9000
	cachePeriod      = 10 * time.Second
	syncPeriod       = 30 * time.Second
	httpReadTimeout  = 10 * time.Second
	httpWriteTimeout = 10 * time.Second
)

func init() {
	flag.Var(&etcdServerList, "etcd_servers", "Servers for the etcd (http://ip:port), comma separated")
	flag.Var(&machineList, "machines", "List of machines to schedule onto, comma separated.")
}

type kubernetesMaster struct {
	podRegistry        pod.Registry
	controllerRegistry controller.Registry
	serviceRegistry    service.Registry
	minionRegistry     minion.Registry
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

	if len(etcdServerList) <= 0 {
		log.Fatal("No etcd severs specified!")
	}

	serveExecutorArtifact := func(path string) string {
		serveFile := func(pattern string, filename string) {
			http.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
				http.ServeFile(w, r, filename)
			})
		}

		// Create base path (http://foobar:5000/<base>)
		pathSplit := strings.Split(path, "/")
		var base string
		if len(pathSplit) > 0 {
			base = pathSplit[len(pathSplit)-1]
		} else {
			base = path
		}
		serveFile("/"+base, path)

		hostURI := fmt.Sprintf("http://%s:%d/%s", *address, artifactPort, base)
		log.V(2).Infof("Hosting artifact '%s' at '%s'", path, hostURI)

		return hostURI
	}

	executorURI := serveExecutorArtifact(*executorPath)
	proxyURI := serveExecutorArtifact(*proxyPath)

	go http.ListenAndServe(":9000", nil)

	podInfoGetter := &client.HTTPPodInfoGetter{
		Client: http.DefaultClient,
		Port:   *minionPort,
	}

	client := client.New("http://"+net.JoinHostPort(*address, strconv.Itoa(int(*port))), nil)

	executorCommand := "./kubernetes-executor -v=2"
	if len(etcdServerList) > 0 {
		etcdServerArguments := strings.Join(etcdServerList, ",")
		executorCommand = "./kubernetes-executor -v=2 -hostname_override=0.0.0.0 -etcd_servers=" + etcdServerArguments
	}

	// Create mesos scheduler driver.
	executor := &mesos.ExecutorInfo{
		ExecutorId: &mesos.ExecutorID{Value: proto.String("KubeleteExecutorID")},
		Command: &mesos.CommandInfo{
			Value: proto.String(executorCommand),
			Uris: []*mesos.CommandInfo_URI{
				{Value: &executorURI},
				{Value: &proxyURI},
			},
		},
		Name:   proto.String("Kubelet Executor"),
		Source: proto.String("kubernetes"),
	}

	mesosPodScheduler := kmscheduler.New(executor, kmscheduler.FCFSScheduleFunc)
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
	// TODO(nnielsen): Using default pod info getter until
	// MesosPodInfoGetter supports network containers.

	// podInfoGetter := MesosPodInfoGetter.New(mesosPodScheduler)

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

func newKubernetesMaster(scheduler *kmscheduler.KubernetesScheduler, c *master.Config) *kubernetesMaster {
	var m *kubernetesMaster

	etcdClient := goetcd.NewClient(c.EtcdServers)
	minionRegistry := minion.NewRegistry(c.Minions) // TODO(adam): Mimic minionRegistryMaker(c)?
	m = &kubernetesMaster{
		podRegistry:        scheduler,
		controllerRegistry: etcd.NewRegistry(etcdClient, minionRegistry),
		serviceRegistry:    etcd.NewRegistry(etcdClient, minionRegistry),
		minionRegistry:     minionRegistry,
		client:             c.Client,
	}
	m.init(scheduler, c.Cloud, c.PodInfoGetter)

	return m
}

func (m *kubernetesMaster) init(scheduler kscheduler.Scheduler, cloud cloudprovider.Interface, podInfoGetter client.PodInfoGetter) {
	podCache := master.NewPodCache(podInfoGetter, m.podRegistry, cachePeriod)
	go podCache.Loop()
	m.storage = map[string]apiserver.RESTStorage{
		"pods": pod.NewRegistryStorage(&pod.RegistryStorageConfig{
			CloudProvider: cloud,
			MinionLister:  m.minionRegistry,
			PodCache:      podCache,
			PodInfoGetter: podInfoGetter,
			Registry:      m.podRegistry,
			Scheduler:     scheduler,
		}),
		"replicationControllers": controller.NewRegistryStorage(m.controllerRegistry, m.podRegistry),
		"services":               service.NewRegistryStorage(m.serviceRegistry, cloud, m.minionRegistry),
		"minions":                minion.NewRegistryStorage(m.minionRegistry),
	}
}

// Run begins serving the Kubernetes API. It never returns.
func (m *kubernetesMaster) run(myAddress, apiPrefix string) error {
	endpoints := endpoint.NewEndpointController(m.serviceRegistry, m.client)
	go util.Forever(func() { endpoints.SyncServiceEndpoints() }, syncPeriod)

	s := &http.Server{
		Addr:           myAddress,
		Handler:        apiserver.Handle(m.storage, api.Codec, apiPrefix),
		ReadTimeout:    httpReadTimeout,
		WriteTimeout:   httpWriteTimeout,
		MaxHeaderBytes: 1 << 20,
	}
	return s.ListenAndServe()
}
