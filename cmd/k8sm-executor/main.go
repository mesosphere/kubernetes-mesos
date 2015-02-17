package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	_ "github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	kconfig "github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/config"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master/ports"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/standalone"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/version/verflag"
	log "github.com/golang/glog"
	bindings "github.com/mesos/mesos-go/executor"
	"github.com/mesosphere/kubernetes-mesos/pkg/executor"
)

const (
	MESOS_CFG_SOURCE = "mesos" // @see ConfigSourceAnnotationKey
	defaultRootDir   = "/var/lib/kubelet"
)

var (
	syncFrequency           = flag.Duration("sync_frequency", 10*time.Second, "Max period between synchronizing running containers and config")
	address                 = util.IP(net.ParseIP(defaultBindingAddress()))
	port                    = flag.Uint("port", ports.KubeletPort, "The port for the info server to serve on")
	hostnameOverride        = flag.String("hostname_override", defaultBindingAddress(), "If non-empty, will use this string as identification instead of the actual hostname.")
	networkContainerImage   = flag.String("network_container_image", kubelet.NetworkContainerImage, "The image that network containers in each pod will use.")
	dockerEndpoint          = flag.String("docker_endpoint", "", "If non-empty, use this for the docker endpoint to communicate with")
	etcdServerList          util.StringList
	etcdConfigFile          = flag.String("etcd_config", "", "The config file for the etcd client. Mutually exclusive with -etcd_servers")
	rootDirectory           = flag.String("root_dir", defaultRootDir, "Directory path for managing kubelet files (volume mounts,etc).")
	allowPrivileged         = flag.Bool("allow_privileged", false, "If true, allow containers to request privileged mode. [default=false]")
	registryPullQPS         = flag.Float64("registry_qps", 0.0, "If > 0, limit registry pull QPS to this value.  If 0, unlimited. [default=0.0]")
	registryBurst           = flag.Int("registry_burst", 10, "Maximum size of a bursty pulls, temporarily allows pulls to burst to this number, while still not exceeding registry_qps.  Only used if --registry_qps > 0")
	enableDebuggingHandlers = flag.Bool("enable_debugging_handlers", true, "Enables server endpoints for log collection and local running of containers and commands. Default true.")
	minimumGCAge            = flag.Duration("minimum_container_ttl_duration", 0, "Minimum age for a finished container before it is garbage collected.  Examples: '300ms', '10s' or '2h45m'")
	maxContainerCount       = flag.Int("maximum_dead_containers_per_container", 5, "Maximum number of old instances of a container to retain per container.  Each container takes up some disk space.  Default: 5.")
	authPath                = flag.String("auth_path", "", "Path to .kubernetes_auth file, specifying how to authenticate to API server.")
	cAdvisorPort            = flag.Uint("cadvisor_port", 4194, "The port of the localhost cAdvisor endpoint")
	oomScoreAdj             = flag.Int("oom_score_adj", -900, "The oom_score_adj value for kubelet process. Values must be within the range [-1000, 1000]")
	apiServerList           util.StringList
	clusterDomain           = flag.String("cluster_domain", "", "Domain for this cluster.  If set, kubelet will configure all containers to search this domain in addition to the host's search domains")
	clusterDNS              = util.IP(nil)

	//.. mesos-specific flags ..

	runProxy     = flag.Bool("run_proxy", true, "Maintain a running kube-proxy instance as a child proc of this kubelet-executor.")
	proxyLogV    = flag.Int("proxy_logv", 0, "Log verbosity of the child kube-proxy.")
	proxyExec    = flag.String("proxy_exec", "./kube-proxy", "Path to the kube-proxy executable.")
	proxyLog     = flag.String("proxy_logfile", "./proxy-log", "Path to the kube-proxy log file.")
	proxyBindall = flag.Bool("proxy_bindall", false, "When true will cause kube-proxy to bind to 0.0.0.0. Defaults to false.")
)

func init() {
	flag.Var(&etcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated")
	flag.Var(&address, "address", "The IP address for the info and proxy servers to serve on. Default to $LIBPROCESS_IP or else 0.0.0.0.")
	flag.Var(&apiServerList, "api_servers", "List of Kubernetes API servers to publish events to. (ip:port), comma separated.")
	flag.Var(&clusterDNS, "cluster_dns", "IP address for a cluster DNS server.  If set, kubelet will configure all containers to use this for DNS resolution in addition to the host's DNS servers")
}

func defaultBindingAddress() string {
	libProcessIP := os.Getenv("LIBPROCESS_IP")
	if libProcessIP == "" {
		return "0.0.0.0"
	} else {
		return libProcessIP
	}
}

func main() {

	flag.Parse()
	util.InitLogs()
	defer util.FlushLogs()
	rand.Seed(time.Now().UTC().UnixNano())

	verflag.PrintAndExitIfRequested()

	if err := util.ApplyOomScoreAdj(*oomScoreAdj); err != nil {
		log.Info(err)
	}

	// TODO(nnielsen): use port from resource offer, instead of command line
	// flag. (jdef) order of initialization becomes tricky once we go that way.
	kcfg := standalone.KubeletConfig{
		Address:                 address,
		AuthPath:                *authPath,
		ApiServerList:           apiServerList,
		AllowPrivileged:         *allowPrivileged,
		HostnameOverride:        *hostnameOverride,
		RootDirectory:           *rootDirectory,
		NetworkContainerImage:   *networkContainerImage,
		SyncFrequency:           *syncFrequency,
		RegistryPullQPS:         *registryPullQPS,
		RegistryBurst:           *registryBurst,
		MinimumGCAge:            *minimumGCAge,
		MaxContainerCount:       *maxContainerCount,
		ClusterDomain:           *clusterDomain,
		ClusterDNS:              clusterDNS,
		Port:                    *port,
		CAdvisorPort:            *cAdvisorPort,
		EnableServer:            true,
		EnableDebuggingHandlers: *enableDebuggingHandlers,
		DockerClient:            util.ConnectToDockerOrDie(*dockerEndpoint),
		EtcdClient:              kubelet.EtcdClientOrDie(etcdServerList, *etcdConfigFile),
	}

	finished := make(chan struct{})
	// @see kubernetes/pkg/standalone.go:createAndInitKubelet
	builder := standalone.KubeletBuilder(func(kc *standalone.KubeletConfig) (standalone.KubeletBootstrap, *kconfig.PodConfig) {
		watch := kubelet.SetupEventSending(kc.AuthPath, kc.ApiServerList)
		pc := kconfig.NewPodConfig(kconfig.PodConfigNotificationSnapshotAndUpdates)
		updates := pc.Channel(MESOS_CFG_SOURCE)

		// TODO(k8s): block until all sources have delivered at least one update to the channel, or break the sync loop
		// up into "per source" synchronizations

		k := &kubeletExecutor{
			Kubelet: kubelet.NewMainKubelet(
				kc.Hostname,
				kc.DockerClient,
				kc.EtcdClient,
				kc.RootDirectory,
				kc.NetworkContainerImage,
				kc.SyncFrequency,
				float32(kc.RegistryPullQPS),
				kc.RegistryBurst,
				kc.MinimumGCAge,
				kc.MaxContainerCount,
				pc.SeenAllSources,
				kc.ClusterDomain,
				net.IP(kc.ClusterDNS)),
			finished: finished,
		}
		//TODO(jdef) would be nice to share the same api client instance as the kubelet event recorder
		apiClient, err := kubelet.GetApiserverClient(kc.AuthPath, kc.ApiServerList)
		if err != nil {
			log.Fatalf("Failed to configure API server client: %v", err)
		}

		exec := executor.New(k.Kubelet, updates, MESOS_CFG_SOURCE, apiClient, watch, kc.DockerClient)
		if driver, err := bindings.NewMesosExecutorDriver(exec); err != nil {
			log.Fatalf("failed to create executor driver: %v", err)
		} else {
			k.driver = driver
		}

		log.V(2).Infof("Initialize executor driver...")

		k.BirthCry()
		executor.KillKubeletContainers(kc.DockerClient)

		go k.GarbageCollectLoop()
		// go k.MonitorCAdvisor(kc.CAdvisorPort) // TODO(jdef) support cadvisor at some point

		k.InitHealthChecking()
		return k, pc
	})

	// create, initialize, and run kubelet services
	standalone.RunKubelet(&kcfg, builder)

	log.Infoln("Waiting for driver initialization to complete")

	// block until driver is shut down
	select {
	case <-finished:
	}
}

// kubelet decorator
type kubeletExecutor struct {
	*kubelet.Kubelet
	initialize sync.Once
	driver     bindings.ExecutorDriver
	finished   chan struct{} // closed once driver.Run() completes
}

func (kl *kubeletExecutor) ListenAndServe(address net.IP, port uint, enableDebuggingHandlers bool) {
	// this func could be called many times, depending how often the HTTP server crashes,
	// so only execute certain initialization procs once
	kl.initialize.Do(func() {
		if *runProxy {
			go util.Forever(runProxyService, 5*time.Second)
		}
		go func() {
			defer close(kl.finished)
			if _, err := kl.driver.Run(); err != nil {
				log.Fatalf("failed to start executor driver: %v", err)
			}
			log.Info("executor Run completed")
		}()

		// TODO(who?) Recover running containers from check pointed pod list.
		// @see reconcileTasks
	})
	log.Infof("Starting kubelet server...")
	log.Error(executor.ListenAndServeKubeletServer(kl, address, port, enableDebuggingHandlers, MESOS_CFG_SOURCE))
}

// this function blocks as long as the proxy service is running; intended to be
// executed asynchronously.
func runProxyService() {
	// TODO(jdef): would be nice if we could run the proxy via an in-memory
	// kubelet config source (in case it crashes, kubelet would restart it);
	// not sure that k8s supports host-networking space for pods
	log.Infof("Starting proxy process...")

	bindAddress := "0.0.0.0"
	if !*proxyBindall {
		bindAddress = address.String()
	}
	args := []string{
		fmt.Sprintf("-bind_address=%s", bindAddress),
		fmt.Sprintf("-v=%d", *proxyLogV),
		"-logtostderr=true",
	}
	if len(etcdServerList) > 0 {
		etcdServerArguments := strings.Join(etcdServerList, ",")
		args = append(args, "-etcd_servers="+etcdServerArguments)
	} else if *etcdConfigFile != "" {
		args = append(args, "-etcd_config="+*etcdConfigFile)
	}

	cmd := exec.Command(*proxyExec, args...)
	_, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}

	proxylogs, err := cmd.StderrPipe()
	if err != nil {
		log.Fatal(err)
	}

	//TODO(jdef) append instead of truncate? what if the disk is full?
	logfile, err := os.Create(*proxyLog)
	if err != nil {
		log.Fatal(err)
	}
	defer logfile.Close()

	go func() {
		defer func() {
			log.Infof("killing proxy process..")
			if err = cmd.Process.Kill(); err != nil {
				log.Errorf("failed to kill proxy process: %v", err)
			}
		}()

		writer := bufio.NewWriter(logfile)
		defer writer.Flush()

		written, err := io.Copy(writer, proxylogs)
		if err != nil {
			log.Errorf("error writing data to proxy log: %v", err)
		}

		log.Infof("wrote %d bytes to proxy log", written)
	}()

	// if the proxy fails to start then we exit the executor, otherwise
	// wait for the proxy process to end (and release resources after).
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	} else if err := cmd.Wait(); err != nil {
		log.Error(err)
	}
}
