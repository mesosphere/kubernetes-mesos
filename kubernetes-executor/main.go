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
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/clientauth"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/health"
	_ "github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	kconfig "github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/config"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/dockertools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master/ports"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/version/verflag"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	"github.com/mesosphere/kubernetes-mesos/executor"
)

const (
	MESOS_CFG_SOURCE = "mesos" // @see ConfigSourceAnnotationKey
	defaultRootDir   = "/var/lib/kubelet"
)

var (
	syncFrequency           = flag.Duration("sync_frequency", 10*time.Second, "Max period between synchronizing running containers and config")
	address                 = util.IP(net.ParseIP("0.0.0.0"))
	port                    = flag.Uint("port", ports.KubeletPort, "The port for the info server to serve on") // TODO(jdef): use kmmaster.KubeletExecutorPort
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
	apiServerList           util.StringList
)

func init() {
	flag.Var(&etcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated")
	flag.Var(&address, "address", "The IP address for the info and proxy servers to serve on. Default to 0.0.0.0.")
	flag.Var(&apiServerList, "api_servers", "List of Kubernetes API servers to publish events to. (ip:port), comma separated.")
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

func getApiserverClient() (*client.Client, error) {
	authInfo, err := clientauth.LoadFromFile(*authPath)
	if err != nil {
		return nil, err
	}
	clientConfig, err := authInfo.MergeWithConfig(client.Config{})
	if err != nil {
		return nil, err
	}
	// TODO: adapt Kube client to support LB over several servers
	if len(apiServerList) > 1 {
		log.Infof("Mulitple api servers specified.  Picking first one")
	}
	clientConfig.Host = apiServerList[0]
	if c, err := client.New(&clientConfig); err != nil {
		return nil, err
	} else {
		return c, nil
	}
}

func main() {

	flag.Parse()
	util.InitLogs() // TODO(jdef) figure out where this actually sends logs by default
	defer util.FlushLogs()
	rand.Seed(time.Now().UTC().UnixNano())

	verflag.PrintAndExitIfRequested()

	etcd.SetLogger(util.NewLogger("etcd "))

	// Make an API client if possible.
	if len(apiServerList) < 1 {
		log.Info("No api servers specified.")
	} else {
		if apiClient, err := getApiserverClient(); err != nil {
			log.Errorf("Unable to make apiserver client: %v", err)
		} else {
			// Send events to APIserver if there is a client.
			log.Infof("Sending events to APIserver.")
			record.StartRecording(apiClient.Events(""), "kubelet")
		}
	}

	// Log the events locally too.
	record.StartLogging(log.Infof)

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
	if containers, err := dockertools.GetKubeletDockerContainers(dockerClient, true); err == nil {
		for _, container := range containers {
			id := container.ID
			log.V(2).Infof("Removing container: %v", id)
			if err := dockerClient.RemoveContainer(docker.RemoveContainerOptions{ID: id, RemoveVolumes: true}); err != nil {
				log.Warning(err)
			}
		}
	} else {
		log.Warningf("Failed to list kubelet docker containers: %v", err)
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

	kl.BirthCry()

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
	kubeletExecutor := executor.New(driver, kl, cfg.Channel(MESOS_CFG_SOURCE), MESOS_CFG_SOURCE)
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
		log.Error(executor.ListenAndServeKubeletServer(kl, net.IP(address), *port, *enableDebuggingHandlers, MESOS_CFG_SOURCE))
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
