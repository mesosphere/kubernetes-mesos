package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	kconfig "github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/config"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/coreos/go-etcd/etcd"
	"github.com/fsouza/go-dockerclient"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	"github.com/mesosphere/kubernetes-mesos/executor"
	_ "github.com/mesosphere/kubernetes-mesos/profile"
)

var (
	syncFrequency    = flag.Duration("sync_frequency", 10*time.Second, "Max period between synchronizing running containers and config")
	hostnameOverride = flag.String("hostname_override", "", "If non-empty, will use this string as identification instead of the actual hostname.")
	dockerEndpoint   = flag.String("docker_endpoint", "", "If non-empty, use this for the docker endpoint to communicate with")
	etcdServerList   util.StringList
	allowPrivileged  = flag.Bool("allow_privileged", false, "If true, allow containers to request privileged mode.")
)

const (
	POD_NS string = "mesos" // k8s pod namespace
)

func main() {
	flag.Var(&etcdServerList, "etcd_servers", "List of etcd servers to watch (http://ip:port), comma separated")

	flag.Parse()
	var endpoint string
	if len(*dockerEndpoint) > 0 {
		endpoint = *dockerEndpoint
	} else if len(os.Getenv("DOCKER_HOST")) > 0 {
		endpoint = os.Getenv("DOCKER_HOST")
	} else {
		endpoint = "unix:///var/run/docker.sock"
	}
	log.Infof("Connecting to docker on %s", endpoint)
	dockerClient, err := docker.NewClient(endpoint)
	if err != nil {
		log.Fatal("Couldn't connnect to docker.")
	}

	hostname := *hostnameOverride
	if hostname == "" {
		// Note: We use exec here instead of os.Hostname() because we
		// want the FQDN, and this is the easiest way to get it.
		fqdnHostname, hostnameErr := exec.Command("hostname", "-f").Output()
		if err != nil {
			log.Fatalf("Couldn't determine hostname: %v", hostnameErr)
		}

		// hostname(1) returns a terminating newline we need to strip.
		hostname = string(fqdnHostname)
		if len(hostname) > 0 {
			hostname = hostname[0 : len(hostname)-1]
		}
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

	kl := kubelet.NewMainKubelet(hostname, dockerClient, nil, etcdClient, "/", *syncFrequency, *allowPrivileged)

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
	// Recover running containers from check pointed pod list.

	go util.Forever(func() { kl.Run(cfg.Updates()) }, 0)

	driver.Join()

	log.V(2).Infof("Cleaning up proxy process...")

	// Clean up proxy process
	cmd.Process.Kill()
}
