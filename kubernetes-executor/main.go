package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/capabilities"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/health"
	_ "github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	kconfig "github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/config"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
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
	syncFrequency         = flag.Duration("sync_frequency", 10*time.Second, "Max period between synchronizing running containers and config")
	hostnameOverride      = flag.String("hostname_override", "", "If non-empty, will use this string as identification instead of the actual hostname.")
	dockerEndpoint        = flag.String("docker_endpoint", "", "If non-empty, use this for the docker endpoint to communicate with")
	etcdServerList        util.StringList
	rootDirectory         = flag.String("root_dir", defaultRootDir, "Directory path for managing kubelet files (volume mounts,etc).")
	networkContainerImage = flag.String("network_container_image", kubelet.NetworkContainerImage, "The image that network containers in each pod will use.")
	minimumGCAge          = flag.Duration("minimum_container_ttl_duration", 0, "Minimum age for a finished container before it is garbage collected.  Examples: '300ms', '10s' or '2h45m'")
	maxContainerCount     = flag.Int("maximum_dead_containers_per_container", 5, "Maximum number of old instances of a container to retain per container.  Each container takes up some disk space.  Default: 5.")
	registryPullQPS       = flag.Float64("registry_qps", 0.0, "If > 0, limit registry pull QPS to this value.  If 0, unlimited. [default=0.0]")
	registryBurst         = flag.Int("registry_burst", 10, "Maximum size of a bursty pulls, temporarily allows pulls to burst to this number, while still not exceeding registry_qps.  Only used if --registry_qps > 0")
	allowPrivileged       = flag.Bool("allow_privileged", false, "If true, allow containers to request privileged mode. [default=false]")
)

func init() {
	flag.Var(&etcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated")
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

	cfg := kconfig.NewPodConfig(kconfig.PodConfigNotificationSnapshotAndUpdates)
	var etcdClient tools.EtcdClient
	if len(etcdServerList) > 0 {
		log.Infof("Connecting to etcd at %v", etcdServerList)
		etcdClient = etcd.NewClient(etcdServerList)
	}

	// Hack: Destroy existing k8s containers for now - we don't know how to reconcile yet.
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

	log.V(2).Infof("Starting kubelet server...")
	go util.Forever(func() {
		// TODO(nnielsen): Don't hardwire port, but use port from
		// resource offer.
		executor.ListenAndServeKubeletServer(kl, cfg.Channel("http"), hostname, 10250, POD_NS)
	}, 1*time.Second)

	log.V(2).Infof("Starting proxy process...")
	var cmd *exec.Cmd
	if len(etcdServerList) > 0 {
		etcdServerArguments := strings.Join(etcdServerList, ",")
		cmd = exec.Command("./proxy", "-etcd_servers="+etcdServerArguments, "-logtostderr=true", "-v=1")
	} else {
		cmd = exec.Command("./proxy", "-logtostderr=true", "-v=1")
	}
	_, err = cmd.StdoutPipe()
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
	go io.Copy(writer, proxylogs)

	// TODO(nnielsen): Factor check-pointing into subsystem.
	dat, err := ioutil.ReadFile("/tmp/kubernetes-pods")
	if err == nil {
		var target []api.PodInfo
		err := json.Unmarshal(dat, &target)
		if err == nil {
			log.Infof("Checkpoint: '%v'", target)
		}
	}

	// TODO(who?) Recover running containers from check pointed pod list.

	go util.Forever(func() { kl.Run(cfg.Updates()) }, 0)
	driver.Join()
}
