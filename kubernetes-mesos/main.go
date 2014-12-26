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
	"github.com/GoogleCloudPlatform/kubernetes/pkg/capabilities"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/pod"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
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
	// Note: the weird ""+ in below lines seems to be the only way to get gofmt to
	// arrange these text blocks sensibly. Grrr.
	port = flag.Int("port", 8888, ""+
		"The port to listen on.  Default 8888. It is assumed that firewall rules are "+
		"set up such that this port is not reachable from outside of the cluster. It is "+
		"further assumed that port 443 on the cluster's public address is proxied to this "+
		"port.")
	address               = util.IP(net.ParseIP("127.0.0.1"))
	publicAddressOverride = flag.String("public_address_override", "", ""+
		"Public serving address. Read only port will be opened on this address, "+
		"and it is assumed that port 443 at this address will be proxied/redirected "+
		"to '-address':'-port'. If blank, the address in the first listed interface "+
		"will be used.")
	readOnlyPort = flag.Int("read_only_port", 7080, ""+
		"The port from which to serve read-only resources. If 0, don't serve on a "+
		"read-only address. It is assumed that firewall rules are set up such that "+
		"this port is not reachable from outside of the cluster.")
	securePort              = flag.Int("secure_port", 0, "The port from which to serve HTTPS with authentication and authorization. If 0, don't serve HTTPS ")
	tlsCertFile             = flag.String("tls_cert_file", "", "File containing x509 Certificate for HTTPS.  (CA cert, if any, concatenated after server cert).")
	tlsPrivateKeyFile       = flag.String("tls_private_key_file", "", "File containing x509 private key matching --tls_cert_file.")
	apiPrefix               = flag.String("api_prefix", "/api", "The prefix for API requests on the server. Default '/api'")
	storageVersion          = flag.String("storage_version", "", "The version to store resources with. Defaults to server preferred")
	healthCheckMinions      = flag.Bool("health_check_minions", true, "If true, health check minions and filter unhealthy ones. Default true.")
	minionCacheTTL          = flag.Duration("minion_cache_ttl", 30*time.Second, "Duration of time to cache minion information. Default 30 seconds.")
	eventTTL                = flag.Duration("event_ttl", 48*time.Hour, "Amount of time to retain events. Default 2 days.")
	tokenAuthFile           = flag.String("token_auth_file", "", "If set, the file that will be used to secure the API server via token authentication.")
	authorizationMode       = flag.String("authorization_mode", "AlwaysAllow", "Selects how to do authorization on the secure port.  One of: "+strings.Join(apiserver.AuthorizationModeChoices, ","))
	authorizationPolicyFile = flag.String("authorization_policy_file", "", "File with authorization policy in csv format, used with --authorization_mode=ABAC, on the secure port.")
	etcdServerList          util.StringList
	etcdConfigFile          = flag.String("etcd_config", "", "The config file for the etcd client. Mutually exclusive with -etcd_servers.")
	corsAllowedOriginList   util.StringList
	allowPrivileged         = flag.Bool("allow_privileged", false, "If true, allow privileged containers. Default false.")
	portalNet               util.IPNet // TODO: make this a list
	enableLogsSupport       = flag.Bool("enable_logs_support", true, "Enables server endpoint for log collection. Default true.")
	kubeletConfig           = client.KubeletConfig{
		Port:        10250,
		EnableHttps: false,
	}
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
	flag.Var(&etcdServerList, "etcd_servers", "Servers for the etcd (http://ip:port), comma separated")
	flag.Var(&corsAllowedOriginList, "cors_allowed_origins", "List of allowed origins for CORS, comma separated.  An allowed origin can be a regular expression to support subdomain matching.  If this list is empty CORS will not be enabled.")
	flag.Var(&portalNet, "portal_net", "A CIDR notation IP range from which to assign portal IPs. This must not overlap with any IP ranges assigned to nodes for pods.")
	client.BindKubeletClientConfigFlags(flag.CommandLine, &kubeletConfig)
}

// TODO(k8s): Longer term we should read this from some config store, rather than a flag.
func verifyPortalFlags() {
	if portalNet.IP == nil {
		log.Fatal("No -portal_net specified")
	}
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
	executorUris = append(executorUris, &mesos.CommandInfo_URI{Value: uri, Executable: proto.Bool(true)})
	uri, executorCmd := serveExecutorArtifact(*executorPath)
	executorUris = append(executorUris, &mesos.CommandInfo_URI{Value: uri, Executable: proto.Bool(true)})

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
	// TODO(jdef): don't hardcode executor user here
	return &mesos.ExecutorInfo{
		ExecutorId: &mesos.ExecutorID{Value: proto.String("KubeleteExecutorID")},
		Command: &mesos.CommandInfo{
			Value: proto.String(executorCommand),
			Uris:  executorUris,
			User:  proto.String("root"), // must be able to use docker socket
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
	verifyPortalFlags()

	if (*etcdConfigFile != "" && len(etcdServerList) != 0) || (*etcdConfigFile == "" && len(etcdServerList) == 0) {
		log.Fatalf("specify either -etcd_servers or -etcd_config")
	}

	capabilities.Initialize(capabilities.Capabilities{
		AllowPrivileged: *allowPrivileged,
	})

	// TODO(nnielsen): Using default pod info getter until
	// MesosPodInfoGetter supports network containers.
	kubeletClient, err := client.NewKubeletClient(&kubeletConfig)
	if err != nil {
		log.Fatalf("Failure to start kubelet client: %v", err)
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

	n := net.IPNet(portalNet)

	authenticator, err := apiserver.NewAuthenticatorFromTokenFile(*tokenAuthFile)
	if err != nil {
		log.Fatalf("Invalid Authentication Config: %v", err)
	}

	authorizer, err := apiserver.NewAuthorizerFromAuthorizationConfig(*authorizationMode, *authorizationPolicyFile)
	if err != nil {
		log.Fatalf("Invalid Authorization Config: %v", err)
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
	config := &kmmaster.Config{
		Client:                client,
		Cloud:                 &kmscheduler.MesosCloud{mesosPodScheduler},
		EtcdHelper:            helper,
		HealthCheckMinions:    *healthCheckMinions,
		EventTTL:              *eventTTL,
		KubeletClient:         kubeletClient,
		PortalNet:             &n,
		EnableLogsSupport:     *enableLogsSupport,
		EnableUISupport:       true,
		APIPrefix:             *apiPrefix,
		CorsAllowedOriginList: corsAllowedOriginList,
		ReadOnlyPort:          *readOnlyPort,
		ReadWritePort:         *port,
		PublicAddress:         *publicAddressOverride,
		Authenticator:         authenticator,
		Authorizer:            authorizer,
		PRFactory:             func() pod.Registry { return mesosPodScheduler },
	}
	m := kmmaster.New(config)
	mesosPodScheduler.Init(driver, m.GetBoundPodFactory())

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

	//TODO(jdef): upstream, the scheduler runs as a separate process... but not in this distro yet
	plugin.New(mesosPodScheduler.NewPluginConfig()).Run()

	log.Fatal(runApiServer(config, m))
}

// Run begins serving the Kubernetes API. It never returns.
func runApiServer(config *kmmaster.Config, m *kmmaster.Master) error {
	// We serve on 3 ports.  See docs/reaching_the_api.md
	roLocation := ""
	if *readOnlyPort != 0 {
		roLocation = net.JoinHostPort(config.PublicAddress, strconv.Itoa(config.ReadOnlyPort))
	}
	secureLocation := ""
	if *securePort != 0 {
		secureLocation = net.JoinHostPort(config.PublicAddress, strconv.Itoa(*securePort))
	}
	rwLocation := net.JoinHostPort(address.String(), strconv.Itoa(int(*port)))

	// See the flag commentary to understand our assumptions when opening the read-only and read-write ports.

	if roLocation != "" {
		// Allow 1 read-only request per second, allow up to 20 in a burst before enforcing.
		rl := util.NewTokenBucketRateLimiter(1.0, 20)
		readOnlyServer := &http.Server{
			Addr:           roLocation,
			Handler:        apiserver.RecoverPanics(apiserver.ReadOnly(apiserver.RateLimit(rl, m.InsecureHandler))),
			ReadTimeout:    5 * time.Minute,
			WriteTimeout:   5 * time.Minute,
			MaxHeaderBytes: 1 << 20,
		}
		log.Infof("Serving read-only insecurely on %s", roLocation)
		go func() {
			defer util.HandleCrash()
			for {
				if err := readOnlyServer.ListenAndServe(); err != nil {
					log.Errorf("Unable to listen for read only traffic (%v); will try again.", err)
				}
				time.Sleep(15 * time.Second)
			}
		}()
	}

	if secureLocation != "" {
		secureServer := &http.Server{
			Addr:           secureLocation,
			Handler:        apiserver.RecoverPanics(m.Handler),
			ReadTimeout:    5 * time.Minute,
			WriteTimeout:   5 * time.Minute,
			MaxHeaderBytes: 1 << 20,
		}
		log.Infof("Serving securely on %s", secureLocation)
		go func() {
			defer util.HandleCrash()
			for {
				if err := secureServer.ListenAndServeTLS(*tlsCertFile, *tlsPrivateKeyFile); err != nil {
					log.Errorf("Unable to listen for secure (%v); will try again.", err)
				}
				time.Sleep(15 * time.Second)
			}
		}()
	}

	s := &http.Server{
		Addr:           rwLocation,
		Handler:        apiserver.RecoverPanics(m.InsecureHandler),
		ReadTimeout:    5 * time.Minute,
		WriteTimeout:   5 * time.Minute,
		MaxHeaderBytes: 1 << 20,
	}
	log.Infof("Serving insecurely on %s", rwLocation)
	return s.ListenAndServe()
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
