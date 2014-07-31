package main

import (
	"flag"
	"os"
	"os/exec"
	"time"
	"net/http"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/fsouza/go-dockerclient"
	log "github.com/golang/glog"
	kconfig "github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/config"
	"github.com/mesosphere/kubernetes-mesos/executor"
	"github.com/mesosphere/mesos-go/mesos"
)

var (
	syncFrequency    = flag.Duration("sync_frequency", 10*time.Second, "Max period between synchronizing running containers and config")
	hostnameOverride = flag.String("hostname_override", "", "If non-empty, will use this string as identification instead of the actual hostname.")
	dockerEndpoint   = flag.String("docker_endpoint", "", "If non-empty, use this for the docker endpoint to communicate with")
)

func main() {
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
			hostname = hostname[0:len(hostname) - 1]
		}
	}

	cfg := kconfig.NewPodConfig(kconfig.PodConfigNotificationSnapshotAndUpdates)

	// TODO(nnielsen): Wire up etcd client.
	kl := kubelet.NewMainKubelet(hostname, dockerClient, nil, nil, "/")

	driver := new(mesos.MesosExecutorDriver)
	kubeletExecutor := executor.New(driver, kl)
	driver.Executor = kubeletExecutor

	go kubeletExecutor.RunKubelet()

	log.V(2).Infof("Init executor driver")
	driver.Init()
	defer driver.Destroy()

	log.V(2).Infof("Executor driver is running")
	driver.Start()

	go util.Forever(func() {
		// TODO(nnielsen): Don't hardwire port, but use port from
		// resource offer.
		kubelet.ListenAndServeKubeletServer(kl, cfg.Channel("http"), http.DefaultServeMux, hostname, 10250)
	}, 0)

	driver.Join()
}
