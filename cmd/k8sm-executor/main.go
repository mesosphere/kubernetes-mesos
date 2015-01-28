package main

import (
	"bufio"
	"flag"
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
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/dockertools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master/ports"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/standalone"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/version/verflag"
	"github.com/fsouza/go-dockerclient"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	"github.com/mesosphere/kubernetes-mesos/pkg/executor"
)

const (
	MESOS_CFG_SOURCE = "mesos" // @see ConfigSourceAnnotationKey
	defaultRootDir   = "/var/lib/kubelet"
)

var (
	syncFrequency           = flag.Duration("sync_frequency", 10*time.Second, "Max period between synchronizing running containers and config")
	address                 = util.IP(net.ParseIP("0.0.0.0"))
	port                    = flag.Uint("port", ports.KubeletPort, "The port for the info server to serve on")
	hostnameOverride        = flag.String("hostname_override", "", "If non-empty, will use this string as identification instead of the actual hostname.")
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
)

func init() {
	flag.Var(&etcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated")
	flag.Var(&address, "address", "The IP address for the info and proxy servers to serve on. Default to 0.0.0.0.")
	flag.Var(&apiServerList, "api_servers", "List of Kubernetes API servers to publish events to. (ip:port), comma separated.")
	flag.Var(&clusterDNS, "cluster_dns", "IP address for a cluster DNS server.  If set, kubelet will configure all containers to use this for DNS resolution in addition to the host's DNS servers")
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

	driver := new(mesos.MesosExecutorDriver)

	// @see kubernetes/pkg/standalone.go:createAndInitKubelet
	initialized := make(chan struct{})
	builder := standalone.KubeletBuilder(func(kc *standalone.KubeletConfig) (standalone.KubeletBootstrap, *kconfig.PodConfig) {
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
			driver:      driver,
			initialized: initialized,
		}
		driver.Executor = executor.New(driver, k.Kubelet, updates, MESOS_CFG_SOURCE)

		log.V(2).Infof("Initialize executor driver...")
		driver.Init()

		k.BirthCry()
		k.reconcileTasks(kc.DockerClient)

		go k.GarbageCollectLoop()
		// go k.MonitorCAdvisor(kc.CAdvisorPort) // TODO(jdef) support cadvisor at some point

		k.InitHealthChecking()
		return k, pc
	})

	// create, initialize, and run kubelet services
	standalone.RunKubelet(&kcfg, builder)

	log.Infoln("Waiting for driver initialization to complete")
	<-initialized
	defer driver.Destroy()

	// block until driver is shut down
	driver.Join()
}

type kubeletExecutor struct {
	*kubelet.Kubelet
	driver      *mesos.MesosExecutorDriver
	initialize  sync.Once
	initialized chan struct{}
}

func (kl *kubeletExecutor) reconcileTasks(dockerClient dockertools.DockerInterface) {
	// TODO(jdef): Destroy existing k8s containers for now - we don't know how to reconcile yet.
	if containers, err := dockertools.GetKubeletDockerContainers(dockerClient, true); err == nil {
		opts := docker.RemoveContainerOptions{
			RemoveVolumes: true,
			Force:         true,
		}
		for _, container := range containers {
			opts.ID = container.ID
			log.V(2).Infof("Removing container: %v", opts.ID)
			if err := dockerClient.RemoveContainer(opts); err != nil {
				log.Warning(err)
			}
		}
	} else {
		log.Warningf("Failed to list kubelet docker containers: %v", err)
	}
}

func (kl *kubeletExecutor) ListenAndServe(address net.IP, port uint, enableDebuggingHandlers bool) {
	// this func could be called many times, depending how often the HTTP server crashes,
	// so only execute certain initialization procs once
	kl.initialize.Do(func() {
		defer close(kl.initialized)
		go util.Forever(runProxyService, 5*time.Second)
		kl.driver.Start()
		log.V(2).Infof("Executor driver is running!")

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

	args := []string{"-bind_address=" + address.String(), "-logtostderr=true", "-v=1"}
	if len(etcdServerList) > 0 {
		etcdServerArguments := strings.Join(etcdServerList, ",")
		args = append(args, "-etcd_servers="+etcdServerArguments)
	} else if *etcdConfigFile != "" {
		args = append(args, "-etcd_config="+*etcdConfigFile)
	}
	//TODO(jdef): don't hardcode name of the proxy executable here
	cmd := exec.Command("./kube-proxy", args...)
	_, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	proxylogs, err := cmd.StderrPipe()
	if err != nil {
		log.Fatal(err)
	}
	logfile, err := os.Create("./proxy-log")
	if err != nil {
		log.Fatal(err)
	}
	defer logfile.Close()
	writer := bufio.NewWriter(logfile)
	defer writer.Flush()

	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	defer func() {
		log.V(2).Infof("Cleaning up proxy process...")
		cmd.Process.Kill()
	}()
	io.Copy(writer, proxylogs)
}
