package main

import (
	"flag"
	"os"
	"os/exec"
	"time"

	"github.com/fsouza/go-dockerclient"
	log "github.com/golang/glog"
	"github.com/mesosphere/mesos-go/mesos"
	"github.com/mesosphere/kubernetes-mesos/executor"
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

	hostname := []byte(*hostnameOverride)
	if string(hostname) == "" {
		// Note: We use exec here instead of os.Hostname() because we
		// want the FQDN, and this is the easiest way to get it.
		hostname, err = exec.Command("hostname", "-f").Output()
		if err != nil {
			log.Fatalf("Couldn't determine hostname: %v", err)
		}
	}

	driver := new(mesos.MesosExecutorDriver)
	kubeletExecutor := executor.New(driver)
	driver.Executor = kubeletExecutor

	// Fill the kuberlet's fields.
	kubeletExecutor.Hostname = string(hostname)
	kubeletExecutor.DockerClient = dockerClient
	kubeletExecutor.SyncFrequency = *syncFrequency

	// TODO(yifan): Cadvisor.
	go kubeletExecutor.RunKubelet(*dockerEndpoint, "", "", "", "", 0)

	log.Info("Init executor driver")
	driver.Init()
	defer driver.Destroy()

	log.Info("Executor driver is running")
	driver.Run()
}
