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
	"io/ioutil"
	"net"
	"net/http"
	"os/user"
	"strings"
	"time"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master/ports"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/version/verflag"
	plugin "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/scheduler"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	_ "github.com/mesosphere/kubernetes-mesos/profile"
	kmscheduler "github.com/mesosphere/kubernetes-mesos/scheduler"
)

var (
	port                = flag.Int("port", ports.SchedulerPort, "The port that the scheduler's http service runs on")
	address             = util.IP(net.ParseIP("127.0.0.1"))
	etcdServerList      util.StringList
	etcdConfigFile      = flag.String("etcd_config", "", "The config file for the etcd client. Mutually exclusive with -etcd_servers.")
	clientConfig        = &client.Config{}
	mesosMaster         = flag.String("mesos_master", "localhost:5050", "Location of leading Mesos master. Default localhost:5050.")
	executorPath        = flag.String("executor_path", "", "Location of the kubernetes executor executable")
	proxyPath           = flag.String("proxy_path", "", "Location of the kubernetes proxy executable")
	mesosUser           = flag.String("mesos_user", "", "Mesos user for this framework, defaults to the username that owns the framework process.")
	mesosRole           = flag.String("mesos_role", "", "Mesos role for this framework, defaults to none.")
	mesosAuthPrincipal  = flag.String("mesos_authentication_principal", "", "Mesos authentication principal.")
	mesosAuthSecretFile = flag.String("mesos_authentication_secret_file", "", "Mesos authentication secret file.")
)

const (
	artifactPort     = 9000             // port of the service that services mesos artifacts (executor); TODO(jdef): make this configurable
	httpReadTimeout  = 10 * time.Second // k8s api server config: maximum duration before timing out read of the request
	httpWriteTimeout = 10 * time.Second // k8s api server config: maximum duration before timing out write of the response
)

func init() {
	flag.Var(&address, "address", "The IP address on to serve on (set to 0.0.0.0 for all interfaces). Default 127.0.0.1.")
	client.BindClientConfigFlags(flag.CommandLine, clientConfig)
}

// returns (downloadURI, basename(path))
func serveExecutorArtifact(path string) (*string, string) {
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

	hostURI := fmt.Sprintf("http://%s:%d/%s", address.String(), artifactPort, base)
	log.V(2).Infof("Hosting artifact '%s' at '%s'", path, hostURI)

	return &hostURI, base
}

func prepareExecutorInfo() *mesos.ExecutorInfo {
	executorUris := []*mesos.CommandInfo_URI{}
	uri, _ := serveExecutorArtifact(*proxyPath)
	executorUris = append(executorUris, &mesos.CommandInfo_URI{Value: uri})
	uri, executorCmd := serveExecutorArtifact(*executorPath)
	executorUris = append(executorUris, &mesos.CommandInfo_URI{Value: uri})

	//TODO(jdef): provide some way (env var?) for user's to customize executor config
	//TODO(jdef): set -hostname_override and -address to 127.0.0.1 if `address` is 127.0.0.1
	//TODO(jdef): kubelet can publish events to the api server, we should probably tell it our IP address
	executorCommand := fmt.Sprintf("./%s -v=2 -hostname_override=0.0.0.0", executorCmd)
	if len(etcdServerList) > 0 {
		etcdServerArguments := strings.Join(etcdServerList, ",")
		executorCommand = fmt.Sprintf("%s -etcd_servers=%s", executorCommand, etcdServerArguments)
	} else {
		uri, basename := serveExecutorArtifact(*etcdConfigFile)
		executorUris = append(executorUris, &mesos.CommandInfo_URI{Value: uri})
		executorCommand = fmt.Sprintf("%s -etcd_config=./%s", executorCommand, basename)
	}

	go http.ListenAndServe(fmt.Sprintf("%s:%d", address.String(), artifactPort), nil)
	log.V(2).Info("Serving executor artifacts...")

	// Create mesos scheduler driver.
	return &mesos.ExecutorInfo{
		ExecutorId: &mesos.ExecutorID{Value: proto.String("KubeleteExecutorID")},
		Command: &mesos.CommandInfo{
			Value: proto.String(executorCommand),
			Uris:  executorUris,
		},
		Name:   proto.String("Kubelet Executor"),
		Source: proto.String("kubernetes"),
	}
}

// Copied from cmd/apiserver.go
func main() {
	flag.Parse()
	util.InitLogs()
	defer util.FlushLogs()

	verflag.PrintAndExitIfRequested()

	// we'll need this for the kubelet-executor
	if (*etcdConfigFile != "" && len(etcdServerList) != 0) || (*etcdConfigFile == "" && len(etcdServerList) == 0) {
		log.Fatalf("specify either -etcd_servers or -etcd_config")
	}

	client, err := client.New(clientConfig)
	if err != nil {
		log.Fatalf("Invalid API configuration: %v", err)
	}

	record.StartRecording(client.Events(""), "scheduler")

	// Create mesos scheduler driver.
	executor := prepareExecutorInfo()
	mesosPodScheduler := kmscheduler.New(executor, kmscheduler.FCFSScheduleFunc, client)
	info, cred, err := buildFrameworkInfo()
	if err != nil {
		log.Fatalf("Misconfigured mesos framework: %v", err)
	}
	driver := &mesos.MesosSchedulerDriver{
		Master:    *mesosMaster,
		Framework: *info,
		Scheduler: mesosPodScheduler,
		Cred:      cred,
	}

	mesosPodScheduler.Init(driver)
	driver.Init()
	defer driver.Destroy()

	go func() {
		if st, err := driver.Start(); err == nil {
			if st != mesos.Status_DRIVER_RUNNING {
				log.Fatalf("Scheduler driver failed to start, has status: %v", st)
			}
			if st, err = driver.Join(); err != nil {
				log.Fatal(err)
			} else if st != mesos.Status_DRIVER_RUNNING {
				log.Fatalf("Scheduler driver failed to join, has status: %v", st)
			}
		} else {
			log.Fatalf("Failed to start driver: %v", err)
		}
	}()

	plugin.New(mesosPodScheduler.NewPluginConfig()).Run()

	select {}
}

func buildFrameworkInfo() (info *mesos.FrameworkInfo, cred *mesos.Credential, err error) {

	username, err := getUsername()
	if err != nil {
		return nil, nil, err
	}
	log.V(2).Infof("Framework configured with mesos user %v", username)
	info = &mesos.FrameworkInfo{
		Name: proto.String("KubernetesScheduler"),
		User: proto.String(username),
	}
	if *mesosRole != "" {
		info.Role = proto.String(*mesosRole)
	}
	if *mesosAuthPrincipal != "" {
		info.Principal = proto.String(*mesosAuthPrincipal)
		secret, err := ioutil.ReadFile(*mesosAuthSecretFile)
		if err != nil {
			return nil, nil, err
		}
		cred = &mesos.Credential{
			Principal: proto.String(*mesosAuthPrincipal),
			Secret:    secret,
		}
	}
	return
}

func getUsername() (username string, err error) {
	username = *mesosUser
	if username == "" {
		if u, err := user.Current(); err == nil {
			username = u.Username
			if username == "" {
				username = "root"
			}
		}
	}
	return
}
