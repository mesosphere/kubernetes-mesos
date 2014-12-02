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
	"strconv"
	"strings"
	"time"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/apiserver"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/auth/authenticator/bearertoken"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/auth/authenticator/tokenfile"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/auth/handlers"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/capabilities"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/pod"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/ui"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/version/verflag"
	plugin "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/scheduler"
	"github.com/coreos/go-etcd/etcd"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	kmmaster "github.com/mesosphere/kubernetes-mesos/master"
	_ "github.com/mesosphere/kubernetes-mesos/profile"
	kmscheduler "github.com/mesosphere/kubernetes-mesos/scheduler"
)

var (
	port                  = flag.Uint("port", 8888, "The port to listen on.  Default 8888.")
	address               = util.IP(net.ParseIP("127.0.0.1"))
	apiPrefix             = flag.String("api_prefix", "/api", "The prefix for API requests on the server. Default '/api'")
	storageVersion        = flag.String("storage_version", "", "The version to store resources with. Defaults to server preferred")
	minionPort            = flag.Uint("minion_port", 10250, "The port at which kubelet will be listening on the minions. Default 10250.")
	healthCheckMinions    = flag.Bool("health_check_minions", true, "If true, health check minions and filter unhealthy ones. Default true.")
	minionCacheTTL        = flag.Duration("minion_cache_ttl", 30*time.Second, "Duration of time to cache minion information. Default 30 seconds.")
	eventTTL              = flag.Duration("event_ttl", 48*time.Hour, "Amount of time to retain events. Default 2 days.")
	tokenAuthFile         = flag.String("token_auth_file", "", "If set, the file that will be used to secure the API server via token authentication.")
	etcdServerList        util.StringList
	etcdConfigFile        = flag.String("etcd_config", "", "The config file for the etcd client. Mutually exclusive with -etcd_servers.")
	corsAllowedOriginList util.StringList
	allowPrivileged       = flag.Bool("allow_privileged", false, "If true, allow privileged containers. Default false.")
	enableLogsSupport     = flag.Bool("enable_logs_support", true, "Enables server endpoint for log collection. Default true.")
	mesosMaster           = flag.String("mesos_master", "localhost:5050", "Location of leading Mesos master. Default localhost:5050.")
	executorPath          = flag.String("executor_path", "", "Location of the kubernetes executor executable")
	proxyPath             = flag.String("proxy_path", "", "Location of the kubernetes proxy executable")
	mesosUser             = flag.String("mesos_user", "", "Mesos user for this framework, defaults to the username that owns the framework process.")
	mesosRole             = flag.String("mesos_role", "", "Mesos role for this framework, defaults to none.")
	mesosAuthPrincipal    = flag.String("mesos_authentication_principal", "", "Mesos authentication principal.")
	mesosAuthSecretFile   = flag.String("mesos_authentication_secret_file", "", "Mesos authentication secret file.")
)

const (
	artifactPort     = 9000             // port of the service that services mesos artifacts (executor); TODO(jdef): make this configurable
	httpReadTimeout  = 10 * time.Second // k8s api server config: maximum duration before timing out read of the request
	httpWriteTimeout = 10 * time.Second // k8s api server config: maximum duration before timing out write of the response
)

func init() {
	flag.Var(&address, "address", "The IP address on to serve on (set to 0.0.0.0 for all interfaces). Default 127.0.0.1.")
	flag.Var(&etcdServerList, "etcd_servers", "Servers for the etcd (http://ip:port), comma separated")
	flag.Var(&corsAllowedOriginList, "cors_allowed_origins", "List of allowed origins for CORS, comma separated.  An allowed origin can be a regular expression to support subdomain matching.  If this list is empty CORS will not be enabled.")
}

func newEtcd(etcdConfigFile string, etcdServerList util.StringList) (helper tools.EtcdHelper, err error) {
	var client tools.EtcdGetSet
	if etcdConfigFile != "" {
		client, err = etcd.NewClientFromFile(etcdConfigFile)
		if err != nil {
			return helper, err
		}
	} else {
		client = etcd.NewClient(etcdServerList)
	}

	return master.NewEtcdHelper(client, *storageVersion)
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

	if (*etcdConfigFile != "" && len(etcdServerList) != 0) || (*etcdConfigFile == "" && len(etcdServerList) == 0) {
		log.Fatalf("specify either -etcd_servers or -etcd_config")
	}

	capabilities.Initialize(capabilities.Capabilities{
		AllowPrivileged: *allowPrivileged,
	})

	// TODO(nnielsen): Using default pod info getter until
	// MesosPodInfoGetter supports network containers.
	// podInfoGetter := MesosPodInfoGetter.New(mesosPodScheduler)
	podInfoGetter := &client.HTTPPodInfoGetter{
		Client: http.DefaultClient,
		Port:   *minionPort,
	}

	// TODO(k8s): expose same flags as client.BindClientConfigFlags but for a server
	clientConfig := &client.Config{
		Host:    net.JoinHostPort(address.String(), strconv.Itoa(int(*port))),
		Version: *storageVersion,
	}
	client, err := client.New(clientConfig)
	if err != nil {
		log.Fatalf("Invalid server address: %v", err)
	}

	helper, err := newEtcd(*etcdConfigFile, etcdServerList)
	if err != nil {
		log.Fatalf("Invalid storage version or misconfigured etcd: %v", err)
	}

	// Create mesos scheduler driver.
	executor := prepareExecutorInfo()
	mesosPodScheduler := kmscheduler.New(executor, kmscheduler.FCFSScheduleFunc, client, helper)
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
	m := kmmaster.New(&kmmaster.Config{
		Client:             client,
		Cloud:              &kmscheduler.MesosCloud{mesosPodScheduler},
		EtcdHelper:         helper,
		HealthCheckMinions: *healthCheckMinions,
		MinionCacheTTL:     *minionCacheTTL,
		EventTTL:           *eventTTL,
		PodInfoGetter:      podInfoGetter,
		PRFactory:          func() pod.Registry { return mesosPodScheduler },
	})
	mesosPodScheduler.Init(driver, m.GetManifestFactory())

	driver.Init()
	defer driver.Destroy()
	go driver.Start()

	//TODO(jdef): upstream, this runs as a separate process... but not in this distro yet
	plugin.New(mesosPodScheduler.NewPluginConfig()).Run()

	log.Fatal(run(m, net.JoinHostPort(address.String(), strconv.Itoa(int(*port)))))
}

// Run begins serving the Kubernetes API. It never returns.
func run(m *kmmaster.Master, myAddress string) error {
	mux := http.NewServeMux()
	apiserver.NewAPIGroup(m.API_v1beta1()).InstallREST(mux, *apiPrefix+"/v1beta1")
	apiserver.NewAPIGroup(m.API_v1beta2()).InstallREST(mux, *apiPrefix+"/v1beta2")
	apiserver.InstallSupport(mux)
	if *enableLogsSupport {
		apiserver.InstallLogsSupport(mux)
	}
	ui.InstallSupport(mux)

	handler := http.Handler(mux)

	if len(corsAllowedOriginList) > 0 {
		allowedOriginRegexps, err := util.CompileRegexps(corsAllowedOriginList)
		if err != nil {
			log.Fatalf("Invalid CORS allowed origin, --cors_allowed_origins flag was set to %v - %v", strings.Join(corsAllowedOriginList, ","), err)
		}
		handler = apiserver.CORS(handler, allowedOriginRegexps, nil, nil, "true")
	}

	if len(*tokenAuthFile) != 0 {
		auth, err := tokenfile.New(*tokenAuthFile)
		if err != nil {
			log.Fatalf("Unable to load the token authentication file '%s': %v", *tokenAuthFile, err)
		}
		userContexts := handlers.NewUserRequestContext()
		handler = handlers.NewRequestAuthenticator(userContexts, bearertoken.New(auth), handlers.Unauthorized, handler)
	}

	handler = apiserver.RecoverPanics(handler)

	s := &http.Server{
		Addr:           myAddress,
		Handler:        handler,
		ReadTimeout:    httpReadTimeout,
		WriteTimeout:   httpWriteTimeout,
		MaxHeaderBytes: 1 << 20,
	}
	return s.ListenAndServe()
}

func buildFrameworkInfo() (info *mesos.FrameworkInfo, cred *mesos.Credential, err error) {

	username, err := getUsername()
	if err != nil {
		return nil, nil, err
	}
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
