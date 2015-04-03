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

package service

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/mesosphere/kubernetes-mesos/pkg/profile"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/clientauth"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/hyperkube"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master/ports"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/gogo/protobuf/proto"
	log "github.com/golang/glog"
	"github.com/kardianos/osext"
	"github.com/mesos/mesos-go/auth"
	"github.com/mesos/mesos-go/auth/sasl"
	"github.com/mesos/mesos-go/auth/sasl/mech"
	mesos "github.com/mesos/mesos-go/mesosproto"
	mutil "github.com/mesos/mesos-go/mesosutil"
	bindings "github.com/mesos/mesos-go/scheduler"
	kmcloud "github.com/mesosphere/kubernetes-mesos/pkg/cloud/mesos"
	"github.com/mesosphere/kubernetes-mesos/pkg/election"
	execcfg "github.com/mesosphere/kubernetes-mesos/pkg/executor/config"
	"github.com/mesosphere/kubernetes-mesos/pkg/runtime"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler"
	schedcfg "github.com/mesosphere/kubernetes-mesos/pkg/scheduler/config"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/ha"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/meta"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/metrics"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/uid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/pflag"
	"golang.org/x/net/context"
)

const (
	defaultMesosUser         = "root" // should have privs to execute docker and iptables commands
	defaultReconcileInterval = 300    // 5m default task reconciliation interval
	defaultReconcileCooldown = 15 * time.Second
	defaultHttpBindInterval  = 5 * time.Second
)

type SchedulerServer struct {
	Port                   int
	Address                util.IP
	AuthPath               string
	APIServerList          util.StringList
	EtcdServerList         util.StringList
	EtcdConfigFile         string
	AllowPrivileged        bool
	ExecutorPath           string
	ProxyPath              string
	MesosUser              string
	MesosRole              string
	MesosAuthPrincipal     string
	MesosAuthSecretFile    string
	Checkpoint             bool
	FailoverTimeout        float64
	ExecutorBindall        bool
	ExecutorRunProxy       bool
	ExecutorProxyBindall   bool
	ExecutorLogV           int
	ExecutorSuicideTimeout time.Duration
	MesosAuthProvider      string
	DriverPort             uint
	HostnameOverride       string
	ReconcileInterval      int64
	ReconcileCooldown      time.Duration
	Graceful               bool
	FrameworkName          string
	HA                     bool
	HADomain               string
	KMPath                 string
	ClusterDNS             util.IP
	ClusterDomain          string
	RootDirectory          string
	DockerEndpoint         string
	PodInfraContainerImage string

	executable string // path to the binary running this service
	client     *client.Client
	driver     atomic.Value // bindings.SchedulerDriver
}

// useful for unit testing specific funcs
type schedulerProcessInterface interface {
	Done() <-chan struct{}
	End()
	Failover() <-chan struct{}
}

// NewSchedulerServer creates a new SchedulerServer with default parameters
func NewSchedulerServer() *SchedulerServer {
	s := SchedulerServer{
		Port:                   ports.SchedulerPort,
		Address:                util.IP(net.ParseIP("127.0.0.1")),
		FailoverTimeout:        time.Duration((1 << 62) - 1).Seconds(),
		ExecutorRunProxy:       true,
		ExecutorSuicideTimeout: execcfg.DefaultSuicideTimeout,
		MesosAuthProvider:      sasl.ProviderName,
		MesosUser:              defaultMesosUser,
		ReconcileInterval:      defaultReconcileInterval,
		ReconcileCooldown:      defaultReconcileCooldown,
		Checkpoint:             true,
		FrameworkName:          schedcfg.DefaultInfoName,
		HA:                     false,
		// TODO(jdef) hard dependency on schedcfg.DefaultInfoName, also assumes
		// that mesos-dns has a k8s plugin that registers services there.
		// ClusterDomain: lower(schedcfg.DefaultInfoName)+".mesos" -- probably "kubernetes.mesos"
		// HADomain: {service-namespace}.ClusterDomain -- based on kube2sky naming rules
	}
	// cache this for later use. also useful in case the original binary gets deleted, e.g.
	// during upgrades, development deployments, etc.
	if filename, err := osext.Executable(); err != nil {
		log.Fatalf("failed to determine path to currently running executable: %v", err)
	} else {
		s.executable = filename
		s.KMPath = filename
	}

	return &s
}

// NewHyperkubeServer creates a new hyperkube Server object that includes the
// description and flags.
func NewHyperkubeServer() *hyperkube.Server {
	s := NewSchedulerServer()

	hks := hyperkube.Server{
		SimpleUsage: "scheduler",
		Long: `Implements the Kubernetes-Mesos scheduler. This will launch Mesos tasks which
results in pods assigned to kubelets based on capacity and constraints.`,
		Run: func(hks *hyperkube.Server, args []string) error {
			return s.Run(hks, args)
		},
	}
	s.AddHyperkubeFlags(hks.Flags())
	return &hks
}

func (s *SchedulerServer) addCoreFlags(fs *pflag.FlagSet) {
	fs.IntVar(&s.Port, "port", s.Port, "The port that the scheduler's http service runs on")
	fs.Var(&s.Address, "address", "The IP address to serve on (set to 0.0.0.0 for all interfaces)")
	fs.Var(&s.APIServerList, "api_servers", "List of Kubernetes API servers for publishing events, and reading pods and services. (ip:port), comma separated.")
	fs.StringVar(&s.AuthPath, "auth_path", s.AuthPath, "Path to .kubernetes_auth file, specifying how to authenticate to API server.")
	fs.Var(&s.EtcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated. Mutually exclusive with -etcd_config")
	fs.StringVar(&s.EtcdConfigFile, "etcd_config", s.EtcdConfigFile, "The config file for the etcd client. Mutually exclusive with -etcd_servers.")
	fs.BoolVar(&s.AllowPrivileged, "allow_privileged", s.AllowPrivileged, "If true, allow privileged containers.")
	fs.StringVar(&s.MesosUser, "mesos_user", s.MesosUser, "Mesos user for this framework, defaults to root.")
	fs.StringVar(&s.MesosRole, "mesos_role", s.MesosRole, "Mesos role for this framework, defaults to none.")
	fs.StringVar(&s.MesosAuthPrincipal, "mesos_authentication_principal", s.MesosAuthPrincipal, "Mesos authentication principal.")
	fs.StringVar(&s.MesosAuthSecretFile, "mesos_authentication_secret_file", s.MesosAuthSecretFile, "Mesos authentication secret file.")
	fs.BoolVar(&s.Checkpoint, "checkpoint", s.Checkpoint, "Enable/disable checkpointing for the kubernetes-mesos framework.")
	fs.Float64Var(&s.FailoverTimeout, "failover_timeout", s.FailoverTimeout, fmt.Sprintf("Framework failover timeout, in sec."))
	fs.BoolVar(&s.ExecutorBindall, "executor_bindall", s.ExecutorBindall, "When true will set -address and -hostname_override of the executor to 0.0.0.0.")
	fs.BoolVar(&s.ExecutorRunProxy, "executor_run_proxy", s.ExecutorRunProxy, "Run the kube-proxy as a child process of the executor.")
	fs.BoolVar(&s.ExecutorProxyBindall, "executor_proxy_bindall", s.ExecutorProxyBindall, "When true pass -proxy_bindall to the executor.")
	fs.StringVar(&s.MesosAuthProvider, "mesos_authentication_provider", s.MesosAuthProvider, fmt.Sprintf("Authentication provider to use, default is SASL that supports mechanisms: %+v", mech.ListSupported()))
	fs.UintVar(&s.DriverPort, "driver_port", s.DriverPort, "Port that the Mesos scheduler driver process should listen on.")
	fs.StringVar(&s.HostnameOverride, "hostname_override", s.HostnameOverride, "If non-empty, will use this string as identification instead of the actual hostname.")
	fs.IntVar(&s.ExecutorLogV, "executor_logv", s.ExecutorLogV, "Logging verbosity of spawned executor processes.")
	fs.DurationVar(&s.ExecutorSuicideTimeout, "executor_suicide_timeout", s.ExecutorSuicideTimeout, "Executor self-terminates after this period of inactivity. Zero disables suicide watch.")
	fs.Int64Var(&s.ReconcileInterval, "reconcile_interval", s.ReconcileInterval, "Interval at which to execute task reconciliation, in sec. Zero disables.")
	fs.DurationVar(&s.ReconcileCooldown, "reconcile_cooldown", s.ReconcileCooldown, "Minimum rest period between task reconciliation operations.")
	fs.BoolVar(&s.Graceful, "graceful", s.Graceful, "Indicator of a graceful failover, intended for internal use only.")
	fs.BoolVar(&s.HA, "ha", s.HA, "Run the scheduler in high availability mode with leader election. All peers should be configured exactly the same.")
	fs.StringVar(&s.FrameworkName, "framework_name", s.FrameworkName, "The framework name to register with Mesos.")
	fs.StringVar(&s.ClusterDomain, "cluster_domain", s.ClusterDomain, "Domain for this cluster.  If set, kubelet will configure all containers to search this domain in addition to the host's search domains")
	fs.Var(&s.ClusterDNS, "cluster_dns", "IP address for a cluster DNS server.  If set, kubelet will configure all containers to use this for DNS resolution in addition to the host's DNS servers")
	fs.StringVar(&s.RootDirectory, "root_dir", s.RootDirectory, "Directory path for managing kubelet files (volume mounts,etc). Defaults to executor sandbox.")
	fs.StringVar(&s.DockerEndpoint, "docker_endpoint", s.DockerEndpoint, "If non-empty, kubelet will use this for the docker endpoint to communicate with.")
	fs.StringVar(&s.PodInfraContainerImage, "pod_infra_container_image", s.PodInfraContainerImage, "The image whose network/ipc namespaces containers in each pod will use.")
	//TODO(jdef) support this flag once we have a better handle on mesos-dns and k8s DNS integration
	//fs.StringVar(&s.HADomain, "ha_domain", s.HADomain, "Domain of the HA scheduler service, only used in HA mode. If specified may be used to construct artifact download URIs.")
}

func (s *SchedulerServer) AddStandaloneFlags(fs *pflag.FlagSet) {
	s.addCoreFlags(fs)
	fs.StringVar(&s.ExecutorPath, "executor_path", s.ExecutorPath, "Location of the kubernetes executor executable")
	fs.StringVar(&s.ProxyPath, "proxy_path", s.ProxyPath, "Location of the kubernetes proxy executable")
}

func (s *SchedulerServer) AddHyperkubeFlags(fs *pflag.FlagSet) {
	s.addCoreFlags(fs)
	fs.StringVar(&s.KMPath, "km_path", s.KMPath, "Location of the km executable, may be a URI or an absolute file path.")
}

// returns (downloadURI, basename(path))
func (s *SchedulerServer) serveFrameworkArtifact(path string) (string, string) {
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

	hostURI := ""
	if s.HA && s.HADomain != "" {
		hostURI = fmt.Sprintf("http://%s.%s:%d/%s", SCHEDULER_SERVICE_NAME, s.HADomain, ports.SchedulerPort, base)
	} else {
		hostURI = fmt.Sprintf("http://%s:%d/%s", s.Address.String(), s.Port, base)
	}
	log.V(2).Infof("Hosting artifact '%s' at '%s'", path, hostURI)

	return hostURI, base
}

func (s *SchedulerServer) prepareExecutorInfo(hks *hyperkube.Server) *mesos.ExecutorInfo {
	ci := &mesos.CommandInfo{
		Shell: proto.Bool(false),
	}

	//TODO(jdef) these should be shared constants with km
	const (
		KM_EXECUTOR = "executor"
		KM_PROXY    = "proxy"
	)

	if s.ExecutorPath != "" {
		uri, executorCmd := s.serveFrameworkArtifact(s.ExecutorPath)
		ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri), Executable: proto.Bool(true)})
		ci.Value = proto.String(fmt.Sprintf("./%s", executorCmd))
	} else if _, err := hks.FindServer(KM_EXECUTOR); err != nil {
		log.Fatalf("either run this scheduler via km or else --executor_path is required: %v", err)
	} else {
		if strings.Index(s.KMPath, "://") > 0 {
			// URI could point directly to executable, e.g. hdfs:///km
			// or else indirectly, e.g. http://acmestorage/tarball.tgz
			// so we assume that for this case the command will always "km"
			ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(s.KMPath), Executable: proto.Bool(true)})
			ci.Value = proto.String("./km") // TODO(jdef) extract constant
		} else if s.KMPath != "" {
			uri, kmCmd := s.serveFrameworkArtifact(s.KMPath)
			ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri), Executable: proto.Bool(true)})
			ci.Value = proto.String(fmt.Sprintf("./%s", kmCmd))
		} else {
			uri, kmCmd := s.serveFrameworkArtifact(s.executable)
			ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri), Executable: proto.Bool(true)})
			ci.Value = proto.String(fmt.Sprintf("./%s", kmCmd))
		}
		ci.Arguments = append(ci.Arguments, KM_EXECUTOR)
	}

	if s.ProxyPath != "" {
		uri, proxyCmd := s.serveFrameworkArtifact(s.ProxyPath)
		ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri), Executable: proto.Bool(true)})
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--proxy_exec=./%s", proxyCmd))
	} else if _, err := hks.FindServer(KM_PROXY); err != nil {
		log.Fatalf("either run this scheduler via km or else --proxy_path is required: %v", err)
	} else if s.ExecutorPath != "" {
		log.Fatalf("proxy can only use km binary if executor does the same")
	} // else, executor is smart enough to know when proxy_path is required, or to use km

	//TODO(jdef): provide some way (env var?) for user's to customize executor config
	//TODO(jdef): set -hostname_override and -address to 127.0.0.1 if `address` is 127.0.0.1
	//TODO(jdef): propagate dockercfg from RootDirectory?

	apiServerArgs := strings.Join(s.APIServerList, ",")
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--api_servers=%s", apiServerArgs))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--v=%d", s.ExecutorLogV))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--allow_privileged=%t", s.AllowPrivileged))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--suicide_timeout=%v", s.ExecutorSuicideTimeout))

	if s.ExecutorBindall {
		ci.Arguments = append(ci.Arguments, "--hostname_override=0.0.0.0")
		ci.Arguments = append(ci.Arguments, "--address=0.0.0.0")
	}

	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--proxy_bindall=%v", s.ExecutorProxyBindall))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--run_proxy=%v", s.ExecutorRunProxy))

	if len(s.EtcdServerList) > 0 {
		etcdServerArguments := strings.Join(s.EtcdServerList, ",")
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--etcd_servers=%s", etcdServerArguments))
	} else {
		//TODO(jdef) should probably support non-local files, e.g. hdfs:///some/config/file
		uri, basename := s.serveFrameworkArtifact(s.EtcdConfigFile)
		ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri)})
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--etcd_config=./%s", basename))
	}

	if s.AuthPath != "" {
		//TODO(jdef) should probably support non-local files, e.g. hdfs:///some/config/file
		uri, basename := s.serveFrameworkArtifact(s.AuthPath)
		ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri)})
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--auth_path=%s", basename))
	}
	if s.ClusterDNS != nil {
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--cluster_dns=%v", s.ClusterDNS))
	}
	if s.ClusterDomain != "" {
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--cluster_domain=%s", s.ClusterDomain))
	}
	if s.RootDirectory != "" {
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--root_dir=%s", s.RootDirectory))
	}
	if s.DockerEndpoint != "" {
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--docker_endpoint=%s", s.DockerEndpoint))
	}
	if s.PodInfraContainerImage != "" {
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--pod_infra_container_image=%s", s.PodInfraContainerImage))
	}

	log.V(1).Infof("prepared executor command %q with args '%+v'", ci.GetValue(), ci.Arguments)

	// Create mesos scheduler driver.
	return &mesos.ExecutorInfo{
		Command: ci,
		Name:    proto.String(execcfg.DefaultInfoName),
		Source:  proto.String(execcfg.DefaultInfoSource),
	}
}

// TODO(jdef): hacked from kubelet/server/server.go
// TODO(k8s): replace this with clientcmd
func (s *SchedulerServer) createAPIServerClient() (*client.Client, error) {
	authInfo, err := clientauth.LoadFromFile(s.AuthPath)
	if err != nil {
		log.Warningf("Could not load kubernetes auth path: %v. Continuing with defaults.", err)
	}
	if authInfo == nil {
		// authInfo didn't load correctly - continue with defaults.
		authInfo = &clientauth.Info{}
	}
	clientConfig, err := authInfo.MergeWithConfig(client.Config{})
	if err != nil {
		return nil, err
	}
	if len(s.APIServerList) < 1 {
		return nil, fmt.Errorf("no api servers specified")
	}
	// TODO: adapt Kube client to support LB over several servers
	if len(s.APIServerList) > 1 {
		log.Infof("Multiple api servers specified.  Picking first one")
	}
	clientConfig.Host = s.APIServerList[0]
	c, err := client.New(&clientConfig)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *SchedulerServer) setDriver(driver bindings.SchedulerDriver, err error) {
	if err == nil {
		s.driver.Store(driver)
	}
}

func (s *SchedulerServer) getDriver() (driver bindings.SchedulerDriver) {
	if d := s.driver.Load(); d != nil {
		driver = d.(bindings.SchedulerDriver)
	}
	return
}

func (s *SchedulerServer) Run(hks *hyperkube.Server, _ []string) error {

	schedulerProcess, driverFactory, etcdClient, ehash := s.bootstrap(hks)

	go s.serviceWriterLoop(schedulerProcess.Done())

	go runtime.Until(func() {
		log.V(1).Info("Starting HTTP interface")
		log.Error(http.ListenAndServe(net.JoinHostPort(s.Address.String(), strconv.Itoa(s.Port)), nil))
	}, defaultHttpBindInterval, schedulerProcess.Done())

	if s.HA {
		validation := ha.ValidationFunc(validateLeadershipTransition)
		srv := ha.NewCandidate(schedulerProcess, driverFactory, validation)
		path := fmt.Sprintf(meta.DefaultElectionFormat, s.FrameworkName)
		sid := uid.New(ehash, "").String()
		log.Infof("registering for election at %v with id %v", path, sid)
		go election.Notify(election.NewEtcdMasterElector(etcdClient), path, sid, srv)
	} else {
		log.Infoln("self-electing in non-HA mode")
		schedulerProcess.Elect(driverFactory)
	}
	return s.awaitFailover(schedulerProcess, func() error { return s.failover(s.getDriver(), hks) })
}

// watch the scheduler process for failover signals and properly handle such. may never return.
func (s *SchedulerServer) awaitFailover(schedulerProcess schedulerProcessInterface, handler func() error) error {

	// we only want to return the first error (if any), everyone else can block forever
	errCh := make(chan error, 1)
	doFailover := func() error {
		// we really don't expect handler to return, if it does something went seriously wrong
		err := handler()
		if err != nil {
			defer schedulerProcess.End()
			err = fmt.Errorf("failover failed, scheduler will terminate: %v", err)
		}
		return err
	}

	// guard for failover signal processing, first signal processor wins
	failoverLatch := &runtime.Latch{}
	runtime.On(schedulerProcess.Done(), func() {
		if !failoverLatch.Acquire() {
			log.V(1).Infof("scheduler process ended, already failing over")
			select {}
		}
		var err error
		defer func() { errCh <- err }()
		select {
		case <-schedulerProcess.Failover():
			err = doFailover()
		default:
			if s.HA {
				err = fmt.Errorf("ha scheduler exiting instead of failing over")
			} else {
				log.Infof("exiting scheduler")
			}
		}
	})
	runtime.OnOSSignal(makeFailoverSigChan(), func(_ os.Signal) {
		if !failoverLatch.Acquire() {
			log.V(1).Infof("scheduler process signalled, already failing over")
			select {}
		}
		errCh <- doFailover()
	})
	return <-errCh
}

func validateLeadershipTransition(desired, current string) {
	log.Infof("validating leadership transition")
	d := uid.Parse(desired).Group()
	c := uid.Parse(current).Group()
	if d == 0 {
		// should *never* happen, but..
		log.Fatalf("illegal scheduler UID: %q", desired)
	}
	if d != c && c != 0 {
		log.Fatalf("desired scheduler group (%x) != current scheduler group (%x)", d, c)
	}
}

func (s *SchedulerServer) bootstrap(hks *hyperkube.Server) (*ha.SchedulerProcess, ha.DriverFactory, tools.EtcdClient, uint64) {

	s.FrameworkName = strings.TrimSpace(s.FrameworkName)
	if s.FrameworkName == "" {
		log.Fatalf("framework_name must be a non-empty string")
	}

	metrics.Register()
	runtime.Register()
	http.Handle("/metrics", prometheus.Handler())

	etcdClient := kubelet.EtcdClientOrDie(s.EtcdServerList, s.EtcdConfigFile)
	if etcdClient == nil {
		log.Fatalf("specify either --etcd_servers or --etcd_config")
	}

	if len(s.APIServerList) < 1 {
		log.Fatal("No api servers specified.")
	}

	client, err := s.createAPIServerClient()
	if err != nil {
		log.Fatalf("Unable to make apiserver client: %v", err)
	}
	s.client = client

	// Send events to APIserver if there is a client.
	record.StartRecording(client.Events(""), api.EventSource{Component: "scheduler"})

	if s.ReconcileCooldown < defaultReconcileCooldown {
		s.ReconcileCooldown = defaultReconcileCooldown
		log.Warningf("user-specified reconcile cooldown too small, defaulting to %v", s.ReconcileCooldown)
	}

	// Create mesos scheduler driver.
	executor := s.prepareExecutorInfo(hks)

	// calculate ExecutorInfo hash to be used for validating compatibility
	// of ExecutorInfo's generated by other HA schedulers.
	ehash := hashExecutorInfo(executor)
	eid := uid.New(ehash, execcfg.DefaultInfoID).String()
	executor.ExecutorId = &mesos.ExecutorID{Value: proto.String(eid)}

	mesosPodScheduler := scheduler.New(scheduler.Config{
		Executor:          executor,
		ScheduleFunc:      scheduler.FCFSScheduleFunc,
		Client:            client,
		EtcdClient:        etcdClient,
		FailoverTimeout:   s.FailoverTimeout,
		ReconcileInterval: s.ReconcileInterval,
		ReconcileCooldown: s.ReconcileCooldown,
	})

	schedulerProcess := ha.New(mesosPodScheduler)
	masterUri := kmcloud.MasterURI()
	info, cred, err := s.buildFrameworkInfo()
	if err != nil {
		log.Fatalf("Misconfigured mesos framework: %v", err)
	}

	dconfig := &bindings.DriverConfig{
		Scheduler:        schedulerProcess,
		Framework:        info,
		Master:           masterUri,
		Credential:       cred,
		BindingAddress:   net.IP(s.Address),
		BindingPort:      uint16(s.DriverPort),
		HostnameOverride: s.HostnameOverride,
		WithAuthContext: func(ctx context.Context) context.Context {
			ctx = auth.WithLoginProvider(ctx, s.MesosAuthProvider)
			ctx = sasl.WithBindingAddress(ctx, net.IP(s.Address))
			return ctx
		},
	}

	kpl := scheduler.NewPlugin(mesosPodScheduler.NewPluginConfig(schedulerProcess.Done()))
	runtime.On(mesosPodScheduler.Registration(), kpl.Run)

	schedulerProcess.Begin()

	deferredInit := func() (err error) {
		if err = mesosPodScheduler.Init(schedulerProcess.Master(), kpl); err != nil {
			err = fmt.Errorf("failed to initialize pod scheduler: %v", err)
		}
		return
	}

	driverFactory := ha.DriverFactory(func() (drv bindings.SchedulerDriver, err error) {
		log.V(1).Infoln("performing deferred initialization")
		if err = deferredInit(); err == nil {
			log.V(1).Infoln("deferred init complete")
			// defer obtaining framework ID to prevent multiple schedulers
			// from overwriting each other's framework IDs
			if dconfig.Framework.Id, err = s.fetchFrameworkID(etcdClient); err == nil {
				log.V(1).Infoln("instantiating mesos scheduler driver")
				drv, err = bindings.NewMesosSchedulerDriver(*dconfig)
				s.setDriver(drv, err)
			}
		}
		return
	})

	return schedulerProcess, driverFactory, etcdClient, ehash
}

func (s *SchedulerServer) failover(driver bindings.SchedulerDriver, hks *hyperkube.Server) error {
	if driver != nil {
		stat, err := driver.Stop(true)
		if stat != mesos.Status_DRIVER_STOPPED {
			return fmt.Errorf("failed to stop driver for failover, received unexpected status code: %v", stat)
		} else if err != nil {
			return err
		}
	}

	// there's no guarantee that all goroutines are actually programmed intelligently with 'done'
	// signals, so we'll need to restart if we want to really stop everything

	// run the same command that we were launched with
	//TODO(jdef) assumption here is that the sheduler is the only service running in this process, we should probably validate that somehow
	args := []string{}
	flags := pflag.CommandLine
	if hks != nil {
		args = append(args, hks.Name())
		flags = hks.Flags()
	}
	flags.Visit(func(flag *pflag.Flag) {
		if flag.Name != "api_servers" && flag.Name != "etcd_servers" {
			args = append(args, fmt.Sprintf("--%s=%s", flag.Name, flag.Value.String()))
		}
	})
	if !s.Graceful {
		args = append(args, "--graceful")
	}
	if len(s.APIServerList) > 0 {
		args = append(args, "--api_servers="+strings.Join(s.APIServerList, ","))
	}
	if len(s.EtcdServerList) > 0 {
		args = append(args, "--etcd_servers="+strings.Join(s.EtcdServerList, ","))
	}
	args = append(args, flags.Args()...)

	log.V(1).Infof("spawning scheduler for graceful failover: %s %+v", s.executable, args)

	cmd := exec.Command(s.executable, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = makeDisownedProcAttr()

	// TODO(jdef) pass in a pipe FD so that we can block, waiting for the child proc to be ready
	//cmd.ExtraFiles = []*os.File{}

	exitcode := 0
	log.Flush() // TODO(jdef) it would be really nice to ensure that no one else in our process was still logging
	if err := cmd.Start(); err != nil {
		//log to stdtout here to avoid conflicts with normal stderr logging
		fmt.Fprintf(os.Stdout, "failed to spawn failover process: %v\n", err)
		os.Exit(1)
	}
	os.Exit(exitcode)
	select {} // will never reach here
}

func (s *SchedulerServer) buildFrameworkInfo() (info *mesos.FrameworkInfo, cred *mesos.Credential, err error) {
	username, err := s.getUsername()
	if err != nil {
		return nil, nil, err
	}
	log.V(2).Infof("Framework configured with mesos user %v", username)
	info = &mesos.FrameworkInfo{
		Name:       proto.String(s.FrameworkName),
		User:       proto.String(username),
		Checkpoint: proto.Bool(s.Checkpoint),
	}
	if s.FailoverTimeout > 0 {
		info.FailoverTimeout = proto.Float64(s.FailoverTimeout)
	}
	if s.MesosRole != "" {
		info.Role = proto.String(s.MesosRole)
	}
	if s.MesosAuthPrincipal != "" {
		info.Principal = proto.String(s.MesosAuthPrincipal)
		if s.MesosAuthSecretFile == "" {
			return nil, nil, errors.New("authentication principal specified without the required credentials file")
		}
		secret, err := ioutil.ReadFile(s.MesosAuthSecretFile)
		if err != nil {
			return nil, nil, err
		}
		cred = &mesos.Credential{
			Principal: proto.String(s.MesosAuthPrincipal),
			Secret:    secret,
		}
	}
	return
}

func (s *SchedulerServer) fetchFrameworkID(client tools.EtcdClient) (*mesos.FrameworkID, error) {
	if s.FailoverTimeout > 0 {
		if response, err := client.Get(meta.FrameworkIDKey, false, false); err != nil {
			if !tools.IsEtcdNotFound(err) {
				return nil, fmt.Errorf("unexpected failure attempting to load framework ID from etcd: %v", err)
			}
			log.V(1).Infof("did not find framework ID in etcd")
		} else if response.Node.Value != "" {
			log.Infof("configuring FrameworkInfo with Id found in etcd: '%s'", response.Node.Value)
			return mutil.NewFrameworkID(response.Node.Value), nil
		}
	} else {
		//TODO(jdef) this seems like a totally hackish way to clean up the framework ID
		if _, err := client.Delete(meta.FrameworkIDKey, true); err != nil {
			if !tools.IsEtcdNotFound(err) {
				return nil, fmt.Errorf("failed to delete framework ID from etcd: %v", err)
			}
			log.V(1).Infof("nothing to delete: did not find framework ID in etcd")
		}
	}
	return nil, nil
}

func (s *SchedulerServer) getUsername() (username string, err error) {
	username = s.MesosUser
	if username == "" {
		if u, err := user.Current(); err == nil {
			username = u.Username
			if username == "" {
				username = defaultMesosUser
			}
		}
	}
	return
}
