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
	"bufio"
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

	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/clientauth"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master/ports"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/coreos/go-etcd/etcd"
	"github.com/gogo/protobuf/proto"
	log "github.com/golang/glog"
	"github.com/kardianos/osext"
	"github.com/mesos/mesos-go/auth"
	"github.com/mesos/mesos-go/auth/sasl"
	"github.com/mesos/mesos-go/auth/sasl/mech"
	mesos "github.com/mesos/mesos-go/mesosproto"
	mutil "github.com/mesos/mesos-go/mesosutil"
	bindings "github.com/mesos/mesos-go/scheduler"
	"github.com/mesosphere/kubernetes-mesos/pkg/election"
	execcfg "github.com/mesosphere/kubernetes-mesos/pkg/executor/config"
	"github.com/mesosphere/kubernetes-mesos/pkg/hyperkube"
	"github.com/mesosphere/kubernetes-mesos/pkg/profile"
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
	defaultMesosMaster       = "localhost:5050"
	defaultMesosUser         = "root" // should have privs to execute docker and iptables commands
	defaultReconcileInterval = 300    // 5m default task reconciliation interval
	defaultReconcileCooldown = 15 * time.Second
	defaultFrameworkName     = "Kubernetes"
)

type SchedulerServer struct {
	Port                          int
	Address                       util.IP
	EnableProfiling               bool
	AuthPath                      string
	APIServerList                 util.StringList
	EtcdServerList                util.StringList
	EtcdConfigFile                string
	AllowPrivileged               bool
	ExecutorPath                  string
	ProxyPath                     string
	MesosMaster                   string
	MesosUser                     string
	MesosRole                     string
	MesosAuthPrincipal            string
	MesosAuthSecretFile           string
	Checkpoint                    bool
	FailoverTimeout               float64
	ExecutorBindall               bool
	ExecutorRunProxy              bool
	ExecutorProxyBindall          bool
	ExecutorLogV                  int
	ExecutorSuicideTimeout        time.Duration
	MesosAuthProvider             string
	DriverPort                    uint
	HostnameOverride              string
	ReconcileInterval             int64
	ReconcileCooldown             time.Duration
	SchedulerConfigFileName       string
	Graceful                      bool
	FrameworkName                 string
	FrameworkWebURI               string
	HA                            bool
	AdvertisedAddress             string
	ServiceAddress                util.IP
	HADomain                      string
	KMPath                        string
	ClusterDNS                    util.IP
	ClusterDomain                 string
	KubeletRootDirectory          string
	KubeletDockerEndpoint         string
	KubeletPodInfraContainerImage string
	KubeletCadvisorPort           uint
	KubeletHostNetworkSources     string
	KubeletSyncFrequency          time.Duration
	KubeletNetworkPluginName      string

	executable string // path to the binary running this service
	client     *client.Client
	driver     atomic.Value // bindings.SchedulerDriver
	mux        *http.ServeMux
}

// useful for unit testing specific funcs
type schedulerProcessInterface interface {
	End() <-chan struct{}
	Failover() <-chan struct{}
	Terminal() <-chan struct{}
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
		MesosMaster:            defaultMesosMaster,
		MesosUser:              defaultMesosUser,
		ReconcileInterval:      defaultReconcileInterval,
		ReconcileCooldown:      defaultReconcileCooldown,
		Checkpoint:             true,
		FrameworkName:          defaultFrameworkName,
		HA:                     false,
		mux:                    http.NewServeMux(),
		KubeletCadvisorPort:    4194, // copied from github.com/GoogleCloudPlatform/kubernetes/blob/release-0.14/cmd/kubelet/app/server.go
		KubeletSyncFrequency:   10 * time.Second,
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

func (s *SchedulerServer) addCoreFlags(fs *pflag.FlagSet) {
	fs.IntVar(&s.Port, "port", s.Port, "The port that the scheduler's http service runs on")
	fs.Var(&s.Address, "address", "The IP address to serve on (set to 0.0.0.0 for all interfaces)")
	fs.BoolVar(&s.EnableProfiling, "profiling", s.EnableProfiling, "Enable profiling via web interface host:port/debug/pprof/")
	fs.Var(&s.APIServerList, "api_servers", "List of Kubernetes API servers for publishing events, and reading pods and services. (ip:port), comma separated.")
	fs.StringVar(&s.AuthPath, "auth_path", s.AuthPath, "Path to .kubernetes_auth file, specifying how to authenticate to API server.")
	fs.Var(&s.EtcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated. Mutually exclusive with -etcd_config")
	fs.StringVar(&s.EtcdConfigFile, "etcd_config", s.EtcdConfigFile, "The config file for the etcd client. Mutually exclusive with -etcd_servers.")
	fs.BoolVar(&s.AllowPrivileged, "allow_privileged", s.AllowPrivileged, "If true, allow privileged containers.")
	fs.StringVar(&s.ClusterDomain, "cluster_domain", s.ClusterDomain, "Domain for this cluster.  If set, kubelet will configure all containers to search this domain in addition to the host's search domains")
	fs.Var(&s.ClusterDNS, "cluster_dns", "IP address for a cluster DNS server. If set, kubelet will configure all containers to use this for DNS resolution in addition to the host's DNS servers")

	fs.StringVar(&s.MesosMaster, "mesos_master", s.MesosMaster, "Location of the Mesos master. The format is a comma-delimited list of of hosts like zk://host1:port,host2:port/mesos. If using ZooKeeper, pay particular attention to the leading zk:// and trailing /mesos! If not using ZooKeeper, standard URLs like http://localhost are also acceptable.")
	fs.StringVar(&s.MesosUser, "mesos_user", s.MesosUser, "Mesos user for this framework, defaults to root.")
	fs.StringVar(&s.MesosRole, "mesos_role", s.MesosRole, "Mesos role for this framework, defaults to none.")
	fs.StringVar(&s.MesosAuthPrincipal, "mesos_authentication_principal", s.MesosAuthPrincipal, "Mesos authentication principal.")
	fs.StringVar(&s.MesosAuthSecretFile, "mesos_authentication_secret_file", s.MesosAuthSecretFile, "Mesos authentication secret file.")
	fs.StringVar(&s.MesosAuthProvider, "mesos_authentication_provider", s.MesosAuthProvider, fmt.Sprintf("Authentication provider to use, default is SASL that supports mechanisms: %+v", mech.ListSupported()))
	fs.BoolVar(&s.Checkpoint, "checkpoint", s.Checkpoint, "Enable/disable checkpointing for the kubernetes-mesos framework.")
	fs.Float64Var(&s.FailoverTimeout, "failover_timeout", s.FailoverTimeout, fmt.Sprintf("Framework failover timeout, in sec."))
	fs.UintVar(&s.DriverPort, "driver_port", s.DriverPort, "Port that the Mesos scheduler driver process should listen on.")
	fs.StringVar(&s.HostnameOverride, "hostname_override", s.HostnameOverride, "If non-empty, will use this string as identification instead of the actual hostname.")
	fs.Int64Var(&s.ReconcileInterval, "reconcile_interval", s.ReconcileInterval, "Interval at which to execute task reconciliation, in sec. Zero disables.")
	fs.DurationVar(&s.ReconcileCooldown, "reconcile_cooldown", s.ReconcileCooldown, "Minimum rest period between task reconciliation operations.")
	fs.StringVar(&s.SchedulerConfigFileName, "scheduler_config", s.SchedulerConfigFileName, "An ini-style configuration file with low-level scheduler settings.")
	fs.BoolVar(&s.Graceful, "graceful", s.Graceful, "Indicator of a graceful failover, intended for internal use only.")
	fs.BoolVar(&s.HA, "ha", s.HA, "Run the scheduler in high availability mode with leader election. All peers should be configured exactly the same.")
	fs.StringVar(&s.FrameworkName, "framework_name", s.FrameworkName, "The framework name to register with Mesos.")
	fs.StringVar(&s.FrameworkWebURI, "framework_weburi", s.FrameworkWebURI, "A URI that points to a web-based interface for interacting with the framework.")
	fs.StringVar(&s.AdvertisedAddress, "advertised_address", s.AdvertisedAddress, "host:port address that is advertised to clients. May be used to construct artifact download URIs.")
	fs.Var(&s.ServiceAddress, "service_address", "The service portal IP address that the scheduler should register with (if unset, chooses randomly)")

	fs.BoolVar(&s.ExecutorBindall, "executor_bindall", s.ExecutorBindall, "When true will set -address of the executor to 0.0.0.0.")
	fs.IntVar(&s.ExecutorLogV, "executor_logv", s.ExecutorLogV, "Logging verbosity of spawned executor processes.")
	fs.BoolVar(&s.ExecutorProxyBindall, "executor_proxy_bindall", s.ExecutorProxyBindall, "When true pass -proxy_bindall to the executor.")
	fs.BoolVar(&s.ExecutorRunProxy, "executor_run_proxy", s.ExecutorRunProxy, "Run the kube-proxy as a child process of the executor.")
	fs.DurationVar(&s.ExecutorSuicideTimeout, "executor_suicide_timeout", s.ExecutorSuicideTimeout, "Executor self-terminates after this period of inactivity. Zero disables suicide watch.")

	fs.StringVar(&s.KubeletRootDirectory, "kubelet_root_dir", s.KubeletRootDirectory, "Directory path for managing kubelet files (volume mounts,etc). Defaults to executor sandbox.")
	fs.StringVar(&s.KubeletDockerEndpoint, "kubelet_docker_endpoint", s.KubeletDockerEndpoint, "If non-empty, kubelet will use this for the docker endpoint to communicate with.")
	fs.StringVar(&s.KubeletPodInfraContainerImage, "kubelet_pod_infra_container_image", s.KubeletPodInfraContainerImage, "The image whose network/ipc namespaces containers in each pod will use.")
	fs.UintVar(&s.KubeletCadvisorPort, "kubelet_cadvisor_port", s.KubeletCadvisorPort, "The port of the kubelet's local cAdvisor endpoint")
	fs.StringVar(&s.KubeletHostNetworkSources, "kubelet_host_network_sources", s.KubeletHostNetworkSources, "Comma-separated list of sources from which the Kubelet allows pods to use of host network. For all sources use \"*\" [default=\"file\"]")
	fs.DurationVar(&s.KubeletSyncFrequency, "kubelet_sync_frequency", s.KubeletSyncFrequency, "Max period between synchronizing running containers and config")
	fs.StringVar(&s.KubeletNetworkPluginName, "kubelet_network_plugin", s.KubeletNetworkPluginName, "<Warning: Alpha feature> The name of the network plugin to be invoked for various events in kubelet/pod lifecycle")

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
		s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
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
	if s.AdvertisedAddress != "" {
		hostURI = fmt.Sprintf("http://%s/%s", s.AdvertisedAddress, base)
	} else if s.HA && s.HADomain != "" {
		hostURI = fmt.Sprintf("http://%s.%s:%d/%s", SCHEDULER_SERVICE_NAME, s.HADomain, ports.SchedulerPort, base)
	} else {
		hostURI = fmt.Sprintf("http://%s:%d/%s", s.Address.String(), s.Port, base)
	}
	log.V(2).Infof("Hosting artifact '%s' at '%s'", path, hostURI)

	return hostURI, base
}

func (s *SchedulerServer) prepareExecutorInfo(hks hyperkube.Interface) (*mesos.ExecutorInfo, *uid.UID, error) {
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
	} else if !hks.FindServer(KM_EXECUTOR) {
		return nil, nil, fmt.Errorf("either run this scheduler via km or else --executor_path is required")
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
	} else if !hks.FindServer(KM_PROXY) {
		return nil, nil, fmt.Errorf("either run this scheduler via km or else --proxy_path is required")
	} else if s.ExecutorPath != "" {
		return nil, nil, fmt.Errorf("proxy can only use km binary if executor does the same")
	} // else, executor is smart enough to know when proxy_path is required, or to use km

	//TODO(jdef): provide some way (env var?) for users to customize executor config
	//TODO(jdef): set -address to 127.0.0.1 if `address` is 127.0.0.1
	//TODO(jdef): propagate dockercfg from RootDirectory?

	apiServerArgs := strings.Join(s.APIServerList, ",")
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--api_servers=%s", apiServerArgs))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--v=%d", s.ExecutorLogV))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--allow_privileged=%t", s.AllowPrivileged))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--suicide_timeout=%v", s.ExecutorSuicideTimeout))

	if s.ExecutorBindall {
		//TODO(jdef) determine whether hostname_override is really needed for bindall because
		//it conflicts with kubelet node status checks/updates
		//ci.Arguments = append(ci.Arguments, "--hostname_override=0.0.0.0")
		ci.Arguments = append(ci.Arguments, "--address=0.0.0.0")
	}

	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--proxy_bindall=%v", s.ExecutorProxyBindall))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--run_proxy=%v", s.ExecutorRunProxy))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--profiling=%t", s.EnableProfiling))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--cadvisor_port=%v", s.KubeletCadvisorPort))
	ci.Arguments = append(ci.Arguments, fmt.Sprintf("--sync_frequency=%v", s.KubeletSyncFrequency))

	if s.AuthPath != "" {
		//TODO(jdef) should probably support non-local files, e.g. hdfs:///some/config/file
		uri, basename := s.serveFrameworkArtifact(s.AuthPath)
		ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri)})
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--auth_path=%s", basename))
	}
	appendOptional := func(name string, value string) {
		if value != "" {
			ci.Arguments = append(ci.Arguments, fmt.Sprintf("--%s=%s", name, value))
		}
	}
	if s.ClusterDNS != nil {
		appendOptional("cluster_dns", s.ClusterDNS.String())
	}
	appendOptional("cluster_domain", s.ClusterDomain)
	appendOptional("root_dir", s.KubeletRootDirectory)
	appendOptional("docker_endpoint", s.KubeletDockerEndpoint)
	appendOptional("pod_infra_container_image", s.KubeletPodInfraContainerImage)
	appendOptional("host_network_sources", s.KubeletHostNetworkSources)
	appendOptional("network_plugin", s.KubeletNetworkPluginName)

	log.V(1).Infof("prepared executor command %q with args '%+v'", ci.GetValue(), ci.Arguments)

	// Create mesos scheduler driver.
	info := &mesos.ExecutorInfo{
		Command: ci,
		Name:    proto.String(execcfg.DefaultInfoName),
		Source:  proto.String(execcfg.DefaultInfoSource),
	}

	// calculate ExecutorInfo hash to be used for validating compatibility
	// of ExecutorInfo's generated by other HA schedulers.
	ehash := hashExecutorInfo(info)
	eid := uid.New(ehash, execcfg.DefaultInfoID)
	info.ExecutorId = &mesos.ExecutorID{Value: proto.String(eid.String())}

	return info, eid, nil
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

func (s *SchedulerServer) Run(hks hyperkube.Interface, _ []string) error {
	// get scheduler low-level config
	sc := schedcfg.CreateDefaultConfig()
	if s.SchedulerConfigFileName != "" {
		f, err := os.Open(s.SchedulerConfigFileName)
		if err != nil {
			log.Fatalf("Cannot open scheduler config file: %v", err)
		}

		err = sc.Read(bufio.NewReader(f))
		if err != nil {
			log.Fatalf("Invalid scheduler config file: %v", err)
		}
	}

	schedulerProcess, driverFactory, etcdClient, eid := s.bootstrap(hks, sc)

	if s.EnableProfiling {
		profile.InstallHandler(s.mux)
	}
	go runtime.Until(func() {
		log.V(1).Info("Starting HTTP interface")
		log.Error(http.ListenAndServe(net.JoinHostPort(s.Address.String(), strconv.Itoa(s.Port)), s.mux))
	}, sc.HttpBindInterval.Duration, schedulerProcess.Terminal())

	if s.HA {
		validation := ha.ValidationFunc(validateLeadershipTransition)
		srv := ha.NewCandidate(schedulerProcess, driverFactory, validation)
		path := fmt.Sprintf(meta.DefaultElectionFormat, s.FrameworkName)
		sid := uid.New(eid.Group(), "").String()
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
	runtime.On(schedulerProcess.Terminal(), func() {
		if !failoverLatch.Acquire() {
			log.V(1).Infof("scheduler process ending, already failing over")
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

// hacked from https://github.com/GoogleCloudPlatform/kubernetes/blob/release-0.14/cmd/kube-apiserver/app/server.go
func newEtcd(etcdConfigFile string, etcdServerList util.StringList) (client tools.EtcdGetSet, err error) {
	if etcdConfigFile != "" {
		client, err = etcd.NewClientFromFile(etcdConfigFile)
	} else {
		client = etcd.NewClient(etcdServerList)
	}
	return
}

func (s *SchedulerServer) bootstrap(hks hyperkube.Interface, sc *schedcfg.Config) (*ha.SchedulerProcess, ha.DriverFactory, tools.EtcdGetSet, *uid.UID) {

	s.FrameworkName = strings.TrimSpace(s.FrameworkName)
	if s.FrameworkName == "" {
		log.Fatalf("framework_name must be a non-empty string")
	}
	s.FrameworkWebURI = strings.TrimSpace(s.FrameworkWebURI)

	metrics.Register()
	runtime.Register()
	s.mux.Handle("/metrics", prometheus.Handler())

	if (s.EtcdConfigFile != "" && len(s.EtcdServerList) != 0) || (s.EtcdConfigFile == "" && len(s.EtcdServerList) == 0) {
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
	record.StartRecording(client.Events(""))

	if s.ReconcileCooldown < defaultReconcileCooldown {
		s.ReconcileCooldown = defaultReconcileCooldown
		log.Warningf("user-specified reconcile cooldown too small, defaulting to %v", s.ReconcileCooldown)
	}

	executor, eid, err := s.prepareExecutorInfo(hks)
	if err != nil {
		log.Fatalf("misconfigured executor: %v", err)
	}

	etcdClient, err := newEtcd(s.EtcdConfigFile, s.EtcdServerList)
	if err != nil {
		log.Fatalf("misconfigured etcd: %v", err)
	}

	mesosPodScheduler := scheduler.New(scheduler.Config{
		Schedcfg:          *sc,
		Executor:          executor,
		ScheduleFunc:      scheduler.FCFSScheduleFunc,
		Client:            client,
		EtcdClient:        etcdClient,
		FailoverTimeout:   s.FailoverTimeout,
		ReconcileInterval: s.ReconcileInterval,
		ReconcileCooldown: s.ReconcileCooldown,
	})

	masterUri := s.MesosMaster
	info, cred, err := s.buildFrameworkInfo()
	if err != nil {
		log.Fatalf("Misconfigured mesos framework: %v", err)
	}

	schedulerProcess := ha.New(mesosPodScheduler)
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

	kpl := scheduler.NewPlugin(mesosPodScheduler.NewPluginConfig(schedulerProcess.Terminal(), s.mux))
	runtime.On(mesosPodScheduler.Registration(), func() { kpl.Run(schedulerProcess.Terminal()) })
	runtime.On(mesosPodScheduler.Registration(), s.newServiceWriter(schedulerProcess.Terminal()))

	deferredInit := func() (err error) {
		if err = mesosPodScheduler.Init(schedulerProcess.Master(), kpl, s.mux); err != nil {
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

	return schedulerProcess, driverFactory, etcdClient, eid
}

func (s *SchedulerServer) failover(driver bindings.SchedulerDriver, hks hyperkube.Interface) error {
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
	if s.FrameworkWebURI != "" {
		info.WebuiUrl = proto.String(s.FrameworkWebURI)
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

func (s *SchedulerServer) fetchFrameworkID(client tools.EtcdGetSet) (*mesos.FrameworkID, error) {
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
