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
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
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
	"github.com/mesosphere/kubernetes-mesos/pkg/executor/config"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler"
	sconfig "github.com/mesosphere/kubernetes-mesos/pkg/scheduler/config"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/ha"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/meta"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/pflag"
	"golang.org/x/net/context"
)

const (
	defaultMesosUser         = "root" // should have privs to execute docker and iptables commands
	defaultReconcileInterval = 300    // 5m default task reconciliation interval
)

type SchedulerServer struct {
	Port                 int
	Address              util.IP
	AuthPath             string
	APIServerList        util.StringList
	EtcdServerList       util.StringList
	EtcdConfigFile       string
	AllowPrivileged      bool
	ExecutorPath         string
	ProxyPath            string
	MesosUser            string
	MesosRole            string
	MesosAuthPrincipal   string
	MesosAuthSecretFile  string
	Checkpoint           bool
	FailoverTimeout      float64
	ExecutorBindall      bool
	ExecutorRunProxy     bool
	ExecutorProxyBindall bool
	ExecutorLogV         int
	MesosAuthProvider    string
	DriverPort           uint
	HostnameOverride     string
	ReconcileInterval    int64
	Graceful             bool
}

// NewSchedulerServer creates a new SchedulerServer with default parameters
func NewSchedulerServer() *SchedulerServer {
	s := SchedulerServer{
		Port:              ports.SchedulerPort,
		Address:           util.IP(net.ParseIP("127.0.0.1")),
		FailoverTimeout:   time.Duration((1 << 62) - 1).Seconds(),
		ExecutorRunProxy:  true,
		MesosAuthProvider: sasl.ProviderName,
		MesosUser:         defaultMesosUser,
		ReconcileInterval: defaultReconcileInterval,
		Checkpoint:        true,
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
	s.AddFlags(hks.Flags())
	return &hks
}

func (s *SchedulerServer) AddFlags(fs *pflag.FlagSet) {
	fs.IntVar(&s.Port, "port", s.Port, "The port that the scheduler's http service runs on")
	fs.Var(&s.Address, "address", "The IP address to serve on (set to 0.0.0.0 for all interfaces)")
	fs.Var(&s.APIServerList, "api_servers", "List of Kubernetes API servers for publishing events, and reading pods and services. (ip:port), comma separated.")
	fs.StringVar(&s.AuthPath, "auth_path", s.AuthPath, "Path to .kubernetes_auth file, specifying how to authenticate to API server.")
	fs.Var(&s.EtcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated. Mutually exclusive with -etcd_config")
	fs.StringVar(&s.EtcdConfigFile, "etcd_config", s.EtcdConfigFile, "The config file for the etcd client. Mutually exclusive with -etcd_servers.")
	fs.BoolVar(&s.AllowPrivileged, "allow_privileged", s.AllowPrivileged, "If true, allow privileged containers.")
	fs.StringVar(&s.ExecutorPath, "executor_path", s.ExecutorPath, "Location of the kubernetes executor executable")
	fs.StringVar(&s.ProxyPath, "proxy_path", s.ProxyPath, "Location of the kubernetes proxy executable")
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
	fs.Int64Var(&s.ReconcileInterval, "reconcile_interval", s.ReconcileInterval, "Interval at which to execute task reconciliation, in sec. Zero disables.")
	fs.BoolVar(&s.Graceful, "graceful", s.Graceful, "Indicator of a graceful failover, intended for internal use only.")
}

// returns (downloadURI, basename(path))
func (s *SchedulerServer) serveExecutorArtifact(path string) (string, string) {
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

	hostURI := fmt.Sprintf("http://%s:%d/%s", s.Address.String(), s.Port, base)
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
		uri, executorCmd := s.serveExecutorArtifact(s.ExecutorPath)
		ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri), Executable: proto.Bool(true)})
		ci.Value = proto.String(fmt.Sprintf("./%s", executorCmd))
	} else if _, err := hks.FindServer(KM_EXECUTOR); err != nil {
		log.Fatalf("either run this scheduler via km or else --executor_path is required: %v", err)
	} else if filename, err := osext.Executable(); err != nil {
		log.Fatalf("failed to determine path to currently running executable: %v", err)
	} else {
		uri, kmCmd := s.serveExecutorArtifact(filename)
		ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri), Executable: proto.Bool(true)})
		ci.Value = proto.String(fmt.Sprintf("./%s", kmCmd))
		ci.Arguments = append(ci.Arguments, KM_EXECUTOR)
	}

	if s.ProxyPath != "" {
		uri, proxyCmd := s.serveExecutorArtifact(s.ProxyPath)
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
		uri, basename := s.serveExecutorArtifact(s.EtcdConfigFile)
		ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri)})
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--etcd_config=./%s", basename))
	}

	if s.AuthPath != "" {
		uri, basename := s.serveExecutorArtifact(s.AuthPath)
		ci.Uris = append(ci.Uris, &mesos.CommandInfo_URI{Value: proto.String(uri)})
		ci.Arguments = append(ci.Arguments, fmt.Sprintf("--auth_path=%s", basename))
	}

	log.V(1).Infof("prepared executor command '%+v' with args '%+v'", ci.Value, ci.Arguments)

	// Create mesos scheduler driver.
	return &mesos.ExecutorInfo{
		ExecutorId: &mesos.ExecutorID{Value: proto.String(config.DefaultInfoID)},
		Command:    ci,
		Name:       proto.String(config.DefaultInfoName),
		Source:     proto.String(config.DefaultInfoSource),
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

func (s *SchedulerServer) Run(hks *hyperkube.Server, _ []string) error {

	metrics.Register()
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

	// Send events to APIserver if there is a client.
	record.StartRecording(client.Events(""), api.EventSource{Component: "scheduler"})

	// Create mesos scheduler driver.
	executor := s.prepareExecutorInfo(hks)
	mesosPodScheduler := scheduler.New(scheduler.Config{
		Executor:          executor,
		ScheduleFunc:      scheduler.FCFSScheduleFunc,
		Client:            client,
		EtcdClient:        etcdClient,
		FailoverTimeout:   s.FailoverTimeout,
		ReconcileInterval: s.ReconcileInterval,
	})
	info, cred, err := s.buildFrameworkInfo(etcdClient)
	if err != nil {
		log.Fatalf("Misconfigured mesos framework: %v", err)
	}
	masterUri := kmcloud.MasterURI()
	schedulerProcess := ha.New(mesosPodScheduler)
	dconfig := bindings.DriverConfig{
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
	driver, err := bindings.NewMesosSchedulerDriver(dconfig)
	if err != nil {
		log.Fatalf("failed to create mesos scheduler driver: %v", err)
	}

	kpl := scheduler.NewPlugin(mesosPodScheduler.NewPluginConfig())
	if err = mesosPodScheduler.Init(driver, kpl); err != nil {
		log.Fatalf("failed to initialize pod scheduler: %v", err)
	}

	schedulerProcess.Begin()

	//TODO(jdef) get rid of this once we have true master election
	schedulerProcess.Elect(driver)

	failoverSignal := make(chan os.Signal, 1)
	signal.Notify(failoverSignal, syscall.SIGUSR1)

	select {
	case <-schedulerProcess.Elected():
		go util.Forever(func() {
			log.V(1).Info("Starting HTTP interface")
			log.Error(http.ListenAndServe(net.JoinHostPort(s.Address.String(), strconv.Itoa(s.Port)), nil))
		}, 5*time.Second) // TODO(jdef) extract constant

		log.V(1).Info("Spinning up scheduling loop")
		kpl.Run()
		select {
		case <-failoverSignal:
			log.Infoln("received failover signal")
			goto doFailover
		case <-schedulerProcess.Failover():
			goto doFailover
		case <-schedulerProcess.Done():
			//TODO(jdef) should probably WARN here in HA mode
			log.Infof("scheduler process exited without failover")
		}
	case <-schedulerProcess.Done():
		log.Fatalf("scheduler process exited abnormally, before election")
	}
	os.Exit(0)

doFailover:
	if err := s.failover(driver, hks); err != nil {
		schedulerProcess.End()
		log.Fatalf("failed to failover, scheduler will terminate: %v", err)
	}

	select {} // will never arrive here
}

func (s *SchedulerServer) failover(driver bindings.SchedulerDriver, hks *hyperkube.Server) error {
	stat, err := driver.Stop(true)
	if stat != mesos.Status_DRIVER_STOPPED {
		return fmt.Errorf("failed to stop driver for failover, received unexpected status code: %v", stat)
	} else if err != nil {
		return err
	}

	binary, err := osext.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate scheduler binary, failover aborted: %v", err)
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

	log.V(1).Infof("spawning scheduler for graceful failover: %s %+v", binary, args)

	cmd := exec.Command(binary, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // disown the spawned scheduler
	}

	// TODO(jdef) pass in a pipe FD so that we can block, waiting for the child proc to be read
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

func (s *SchedulerServer) buildFrameworkInfo(client tools.EtcdClient) (info *mesos.FrameworkInfo, cred *mesos.Credential, err error) {

	var (
		frameworkId *mesos.FrameworkID
		failover    *float64
	)
	if s.FailoverTimeout > 0 {
		failover = proto.Float64(s.FailoverTimeout)
		if response, err := client.Get(meta.FrameworkIDKey, false, false); err != nil {
			if !tools.IsEtcdNotFound(err) {
				log.Fatalf("unexpected failure attempting to load framework ID from etcd: %v", err)
			}
			log.V(1).Infof("did not find framework ID in etcd")
		} else if response.Node.Value != "" {
			log.Infof("configuring FrameworkInfo with Id found in etcd: '%s'", response.Node.Value)
			frameworkId = mutil.NewFrameworkID(response.Node.Value)
		}
	} else {
		if _, err := client.Delete(meta.FrameworkIDKey, true); err != nil {
			if !tools.IsEtcdNotFound(err) {
				log.Fatalf("failed to delete framework ID from etcd: %v", err)
			}
			log.V(1).Infof("nothing to delete: did not find framework ID in etcd")
		}
	}

	username, err := s.getUsername()
	if err != nil {
		return nil, nil, err
	}
	log.V(2).Infof("Framework configured with mesos user %v", username)
	info = &mesos.FrameworkInfo{
		Name:            proto.String(sconfig.DefaultInfoName),
		User:            proto.String(username),
		Checkpoint:      proto.Bool(s.Checkpoint),
		Id:              frameworkId,
		FailoverTimeout: failover,
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
