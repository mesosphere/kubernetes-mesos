package main

import (
	"bufio"
	"flag"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/capabilities"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/health"
	_ "github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	kconfig "github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/config"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/version/verflag"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	"github.com/mesosphere/kubernetes-mesos/executor"
)

const (
	POD_NS         string = "mesos" // k8s pod namespace
	defaultRootDir        = "/var/lib/kubelet"
)

var (
	syncFrequency           = flag.Duration("sync_frequency", 10*time.Second, "Max period between synchronizing running containers and config")
	address                 = util.IP(net.ParseIP("0.0.0.0"))
	port                    = flag.Uint("port", master.KubeletPort, "The port for the info server to serve on") // TODO(jdef): use kmmaster.KubeletExecutorPort
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
)

func init() {
	flag.Var(&etcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated")
	flag.Var(&address, "address", "The IP address for the info and proxy servers to serve on. Default to 0.0.0.0.")
}

func getDockerEndpoint() string {
	var endpoint string
	if len(*dockerEndpoint) > 0 {
		endpoint = *dockerEndpoint
	} else if len(os.Getenv("DOCKER_HOST")) > 0 {
		endpoint = os.Getenv("DOCKER_HOST")
	} else {
		endpoint = "unix:///var/run/docker.sock"
	}
	log.Infof("Connecting to docker on %s", endpoint)

	return endpoint
}

func getHostname() string {
	hostname := []byte(*hostnameOverride)
	if string(hostname) == "" {
		// Note: We use exec here instead of os.Hostname() because we
		// want the FQDN, and this is the easiest way to get it.
		fqdn, err := exec.Command("hostname", "-f").Output()
		if err != nil {
			log.Fatalf("Couldn't determine hostname: %v", err)
		}
		hostname = fqdn
	}
	return strings.TrimSpace(string(hostname))
}

func main() {

	flag.Parse()
	util.InitLogs() // TODO(jdef) figure out where this actually sends logs by default
	defer util.FlushLogs()
	rand.Seed(time.Now().UTC().UnixNano())

	verflag.PrintAndExitIfRequested()

	etcd.SetLogger(util.NewLogger("etcd "))

	capabilities.Initialize(capabilities.Capabilities{
		AllowPrivileged: *allowPrivileged,
	})

	dockerClient, err := docker.NewClient(getDockerEndpoint())
	if err != nil {
		log.Fatal("Couldn't connnect to docker.")
	}

	hostname := getHostname()

	if *rootDirectory == "" {
		log.Fatal("Invalid root directory path.")
	}
	*rootDirectory = path.Clean(*rootDirectory)
	if err := os.MkdirAll(*rootDirectory, 0750); err != nil {
		log.Warningf("Error creating root directory: %v", err)
	}

	// source of all configuration
	cfg := kconfig.NewPodConfig(kconfig.PodConfigNotificationSnapshotAndUpdates)

	// k8sm: no other pod configuration sources supported in this hybrid kubelet-executor

	// define etcd config source and initialize etcd client
	var etcdClient *etcd.Client
	if len(etcdServerList) > 0 {
		etcdClient = etcd.NewClient(etcdServerList)
	} else if *etcdConfigFile != "" {
		var err error
		etcdClient, err = etcd.NewClientFromFile(*etcdConfigFile)
		if err != nil {
			log.Fatalf("Error with etcd config file: %v", err)
		}
	}

	if etcdClient != nil {
		log.Infof("Connected to etcd at %v", etcdClient.GetCluster())
	}

	// TODO(???): Destroy existing k8s containers for now - we don't know how to reconcile yet.
	containers, err := dockerClient.ListContainers(docker.ListContainersOptions{All: true})
	if err == nil {
		for _, container := range containers {
			log.V(2).Infof("Existing container: %v", container.Names)

			for _, containerName := range container.Names {
				if strings.HasPrefix(containerName, "/k8s--") {
					id := container.ID
					log.V(2).Infof("Removing container: %v", id)
					err = dockerClient.RemoveContainer(docker.RemoveContainerOptions{ID: id, RemoveVolumes: true})
					continue
				}
			}

		}
	}

	// TODO(k8s): block until all sources have delivered at least one update to the channel, or break the sync loop
	// up into "per source" synchronizations

	kl := kubelet.NewMainKubelet(
		hostname,
		dockerClient,
		etcdClient,
		*rootDirectory,
		*networkContainerImage,
		*syncFrequency,
		float32(*registryPullQPS),
		*registryBurst,
		*minimumGCAge,
		*maxContainerCount)
	go func() {
		util.Forever(func() {
			err := kl.GarbageCollectContainers()
			if err != nil {
				log.Errorf("Garbage collect failed: %v", err)
			}
		}, time.Minute*1)
	}()

	/* TODO(jdef): enable cadvisor integration
	go func() {
		defer util.HandleCrash()
		// TODO(k8s): Monitor this connection, reconnect if needed?
		log.V(1).Infof("Trying to create cadvisor client.")
		cadvisorClient, err := cadvisor.NewClient("http://127.0.0.1:4194")
		if err != nil {
			log.Errorf("Error on creating cadvisor client: %v", err)
			return
		}
		log.V(1).Infof("Successfully created cadvisor client.")
		kl.SetCadvisorClient(cadvisorClient)
	}()
	*/

	// TODO(k8s): These should probably become more plugin-ish: register a factory func
	// in each checker's init(), iterate those here.
	health.AddHealthChecker(health.NewExecHealthChecker(kl))
	health.AddHealthChecker(health.NewHTTPHealthChecker(&http.Client{}))
	health.AddHealthChecker(&health.TCPHealthChecker{})

	driver := new(mesos.MesosExecutorDriver)
	kubeletExecutor := executor.New(driver, kl, cfg.Channel(POD_NS), POD_NS)
	driver.Executor = kubeletExecutor

	log.V(2).Infof("Initialize executor driver...")
	driver.Init()
	defer driver.Destroy()

	log.V(2).Infof("Executor driver is running!")
	driver.Start()

	// TODO(who?) Recover running containers from check pointed pod list.

	// start the kubelet
	go util.Forever(func() { kl.Run(cfg.Updates()) }, 0)

	log.Infof("Starting kubelet server...")
	go util.Forever(func() {
		// TODO(nnielsen): Don't hardwire port, but use port from resource offer.
		log.Error(executor.ListenAndServeKubeletServer(kl, net.IP(address), *port, *enableDebuggingHandlers, POD_NS))
	}, 0)

	go runProxyService()

	/*
		// TODO(nnielsen): Factor check-pointing into subsystem.
		dat, err := ioutil.ReadFile("/tmp/kubernetes-pods")
		if err == nil {
			var target []api.PodInfo
			err := json.Unmarshal(dat, &target)
			if err == nil {
				log.Infof("Checkpoint: '%v'", target)
			}
		}
	*/

	driver.Join()
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
	cmd := exec.Command("./proxy", args...)
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
