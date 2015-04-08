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

// Package app makes it easy to create a kubelet server for various contexts.
package app

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/clientauth"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/credentialprovider"
	_ "github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/cadvisor"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/config"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/dockertools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/network"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master/ports"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/volume"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
	"github.com/golang/glog"
	"github.com/spf13/pflag"
)

const defaultRootDir = "/var/lib/kubelet"

// KubeletServer encapsulates all of the parameters necessary for starting up
// a kubelet. These can either be set via command line or directly.
type KubeletServer struct {
	Config                         string
	SyncFrequency                  time.Duration
	FileCheckFrequency             time.Duration
	HTTPCheckFrequency             time.Duration
	ManifestURL                    string
	EnableServer                   bool
	Address                        util.IP
	Port                           uint
	HostnameOverride               string
	PodInfraContainerImage         string
	DockerEndpoint                 string
	RootDirectory                  string
	AllowPrivileged                bool
	HostNetworkSources             string
	RegistryPullQPS                float64
	RegistryBurst                  int
	RunOnce                        bool
	EnableDebuggingHandlers        bool
	MinimumGCAge                   time.Duration
	MaxPerPodContainerCount        int
	MaxContainerCount              int
	AuthPath                       string
	CadvisorPort                   uint
	OOMScoreAdj                    int
	APIServerList                  util.StringList
	ClusterDomain                  string
	MasterServiceNamespace         string
	ClusterDNS                     util.IP
	ReallyCrashForTesting          bool
	StreamingConnectionIdleTimeout time.Duration
	ImageGCHighThresholdPercent    int
	ImageGCLowThresholdPercent     int
	NetworkPluginName              string
	CloudProvider                  string
	CloudConfigFile                string
}

// NewKubeletServer will create a new KubeletServer with default values.
func NewKubeletServer() *KubeletServer {
	return &KubeletServer{
		SyncFrequency:      10 * time.Second,
		FileCheckFrequency: 20 * time.Second,
		HTTPCheckFrequency: 20 * time.Second,
		EnableServer:       true,
		Address:            util.IP(net.ParseIP("127.0.0.1")),
		Port:               ports.KubeletPort,
		PodInfraContainerImage:      kubelet.PodInfraContainerImage,
		RootDirectory:               defaultRootDir,
		RegistryBurst:               10,
		EnableDebuggingHandlers:     true,
		MinimumGCAge:                1 * time.Minute,
		MaxPerPodContainerCount:     5,
		MaxContainerCount:           100,
		CadvisorPort:                4194,
		OOMScoreAdj:                 -900,
		MasterServiceNamespace:      api.NamespaceDefault,
		ImageGCHighThresholdPercent: 90,
		ImageGCLowThresholdPercent:  80,
		NetworkPluginName:           "",
		HostNetworkSources:          kubelet.FileSource,
	}
}

// AddFlags adds flags for a specific KubeletServer to the specified FlagSet
func (s *KubeletServer) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&s.Config, "config", s.Config, "Path to the config file or directory of files")
	fs.DurationVar(&s.SyncFrequency, "sync_frequency", s.SyncFrequency, "Max period between synchronizing running containers and config")
	fs.DurationVar(&s.FileCheckFrequency, "file_check_frequency", s.FileCheckFrequency, "Duration between checking config files for new data")
	fs.DurationVar(&s.HTTPCheckFrequency, "http_check_frequency", s.HTTPCheckFrequency, "Duration between checking http for new data")
	fs.StringVar(&s.ManifestURL, "manifest_url", s.ManifestURL, "URL for accessing the container manifest")
	fs.BoolVar(&s.EnableServer, "enable_server", s.EnableServer, "Enable the info server")
	fs.Var(&s.Address, "address", "The IP address for the info server to serve on (set to 0.0.0.0 for all interfaces)")
	fs.UintVar(&s.Port, "port", s.Port, "The port for the info server to serve on")
	fs.StringVar(&s.HostnameOverride, "hostname_override", s.HostnameOverride, "If non-empty, will use this string as identification instead of the actual hostname.")
	fs.StringVar(&s.PodInfraContainerImage, "pod_infra_container_image", s.PodInfraContainerImage, "The image whose network/ipc namespaces containers in each pod will use.")
	fs.StringVar(&s.DockerEndpoint, "docker_endpoint", s.DockerEndpoint, "If non-empty, use this for the docker endpoint to communicate with")
	fs.StringVar(&s.RootDirectory, "root_dir", s.RootDirectory, "Directory path for managing kubelet files (volume mounts,etc).")
	fs.BoolVar(&s.AllowPrivileged, "allow_privileged", s.AllowPrivileged, "If true, allow containers to request privileged mode. [default=false]")
	fs.StringVar(&s.HostNetworkSources, "host_network_sources", s.HostNetworkSources, "Comma-separated list of sources from which the Kubelet allows pods to use of host network. For all sources use \"*\" [default=\"file\"]")
	fs.Float64Var(&s.RegistryPullQPS, "registry_qps", s.RegistryPullQPS, "If > 0, limit registry pull QPS to this value.  If 0, unlimited. [default=0.0]")
	fs.IntVar(&s.RegistryBurst, "registry_burst", s.RegistryBurst, "Maximum size of a bursty pulls, temporarily allows pulls to burst to this number, while still not exceeding registry_qps.  Only used if --registry_qps > 0")
	fs.BoolVar(&s.RunOnce, "runonce", s.RunOnce, "If true, exit after spawning pods from local manifests or remote urls. Exclusive with --api_servers, and --enable-server")
	fs.BoolVar(&s.EnableDebuggingHandlers, "enable_debugging_handlers", s.EnableDebuggingHandlers, "Enables server endpoints for log collection and local running of containers and commands")
	fs.DurationVar(&s.MinimumGCAge, "minimum_container_ttl_duration", s.MinimumGCAge, "Minimum age for a finished container before it is garbage collected.  Examples: '300ms', '10s' or '2h45m'")
	fs.IntVar(&s.MaxPerPodContainerCount, "maximum_dead_containers_per_container", s.MaxPerPodContainerCount, "Maximum number of old instances of a container to retain per container.  Each container takes up some disk space.  Default: 5.")
	fs.IntVar(&s.MaxContainerCount, "maximum_dead_containers", s.MaxContainerCount, "Maximum number of old instances of a containers to retain globally.  Each container takes up some disk space.  Default: 100.")
	fs.StringVar(&s.AuthPath, "auth_path", s.AuthPath, "Path to .kubernetes_auth file, specifying how to authenticate to API server.")
	fs.UintVar(&s.CadvisorPort, "cadvisor_port", s.CadvisorPort, "The port of the localhost cAdvisor endpoint")
	fs.IntVar(&s.OOMScoreAdj, "oom_score_adj", s.OOMScoreAdj, "The oom_score_adj value for kubelet process. Values must be within the range [-1000, 1000]")
	fs.Var(&s.APIServerList, "api_servers", "List of Kubernetes API servers for publishing events, and reading pods and services. (ip:port), comma separated.")
	fs.StringVar(&s.ClusterDomain, "cluster_domain", s.ClusterDomain, "Domain for this cluster.  If set, kubelet will configure all containers to search this domain in addition to the host's search domains")
	fs.StringVar(&s.MasterServiceNamespace, "master_service_namespace", s.MasterServiceNamespace, "The namespace from which the kubernetes master services should be injected into pods")
	fs.Var(&s.ClusterDNS, "cluster_dns", "IP address for a cluster DNS server.  If set, kubelet will configure all containers to use this for DNS resolution in addition to the host's DNS servers")
	fs.BoolVar(&s.ReallyCrashForTesting, "really_crash_for_testing", s.ReallyCrashForTesting, "If true, crash with panics more often.")
	fs.DurationVar(&s.StreamingConnectionIdleTimeout, "streaming_connection_idle_timeout", 0, "Maximum time a streaming connection can be idle before the connection is automatically closed.  Example: '5m'")
	fs.IntVar(&s.ImageGCHighThresholdPercent, "image_gc_high_threshold", s.ImageGCHighThresholdPercent, "The percent of disk usage after which image garbage collection is always run. Default: 90%%")
	fs.IntVar(&s.ImageGCLowThresholdPercent, "image_gc_low_threshold", s.ImageGCLowThresholdPercent, "The percent of disk usage before which image garbage collection is never run. Lowest disk usage to garbage collect to. Default: 80%%")
	fs.StringVar(&s.NetworkPluginName, "network_plugin", s.NetworkPluginName, "<Warning: Alpha feature> The name of the network plugin to be invoked for various events in kubelet/pod lifecycle")
	fs.StringVar(&s.CloudProvider, "cloud_provider", s.CloudProvider, "The provider for cloud services.  Empty string for no provider.")
	fs.StringVar(&s.CloudConfigFile, "cloud_config", s.CloudConfigFile, "The path to the cloud provider configuration file.  Empty string for no configuration file.")
}

// Run runs the specified KubeletServer.  This should never exit.
func (s *KubeletServer) Run(_ []string) error {
	util.ReallyCrash = s.ReallyCrashForTesting
	rand.Seed(time.Now().UTC().UnixNano())

	if err := util.ApplyOomScoreAdj(0, s.OOMScoreAdj); err != nil {
		glog.Info(err)
	}

	client, err := s.createAPIServerClient()
	if err != nil && len(s.APIServerList) > 0 {
		glog.Warningf("No API client: %v", err)
	}

	glog.Infof("Using root directory: %v", s.RootDirectory)

	credentialprovider.SetPreferredDockercfgPath(s.RootDirectory)

	cadvisorInterface, err := cadvisor.New(s.CadvisorPort)
	if err != nil {
		return err
	}

	imageGCPolicy := kubelet.ImageGCPolicy{
		HighThresholdPercent: s.ImageGCHighThresholdPercent,
		LowThresholdPercent:  s.ImageGCLowThresholdPercent,
	}

	cloud := cloudprovider.InitCloudProvider(s.CloudProvider, s.CloudConfigFile)
	glog.Infof("Successfully initialized cloud provider: %q from the config file: %q\n", s.CloudProvider, s.CloudConfigFile)

	hostNetworkSources, err := kubelet.GetValidatedSources(strings.Split(s.HostNetworkSources, ","))
	if err != nil {
		return err
	}
	kcfg := KubeletConfig{
		Address:                        s.Address,
		AllowPrivileged:                s.AllowPrivileged,
		HostNetworkSources:             hostNetworkSources,
		HostnameOverride:               s.HostnameOverride,
		RootDirectory:                  s.RootDirectory,
		ConfigFile:                     s.Config,
		ManifestURL:                    s.ManifestURL,
		FileCheckFrequency:             s.FileCheckFrequency,
		HTTPCheckFrequency:             s.HTTPCheckFrequency,
		PodInfraContainerImage:         s.PodInfraContainerImage,
		SyncFrequency:                  s.SyncFrequency,
		RegistryPullQPS:                s.RegistryPullQPS,
		RegistryBurst:                  s.RegistryBurst,
		MinimumGCAge:                   s.MinimumGCAge,
		MaxPerPodContainerCount:        s.MaxPerPodContainerCount,
		MaxContainerCount:              s.MaxContainerCount,
		ClusterDomain:                  s.ClusterDomain,
		ClusterDNS:                     s.ClusterDNS,
		Runonce:                        s.RunOnce,
		Port:                           s.Port,
		CadvisorInterface:              cadvisorInterface,
		EnableServer:                   s.EnableServer,
		EnableDebuggingHandlers:        s.EnableDebuggingHandlers,
		DockerClient:                   dockertools.ConnectToDockerOrDie(s.DockerEndpoint),
		KubeClient:                     client,
		MasterServiceNamespace:         s.MasterServiceNamespace,
		VolumePlugins:                  ProbeVolumePlugins(),
		NetworkPlugins:                 ProbeNetworkPlugins(),
		NetworkPluginName:              s.NetworkPluginName,
		StreamingConnectionIdleTimeout: s.StreamingConnectionIdleTimeout,
		ImageGCPolicy:                  imageGCPolicy,
		Cloud:                          cloud,
	}

	RunKubelet(&kcfg)

	// runs forever
	select {}

}

func (s *KubeletServer) setupRunOnce() {
	if s.RunOnce {
		// Don't use apiserver source, on the presumption that this flag is used
		// for bootstrapping some system pods.
		if len(s.APIServerList) > 0 {
			glog.Fatalf("invalid option: --runonce and --api_servers are mutually exclusive")
		}
		if s.EnableServer {
			glog.Infof("--runonce is set, disabling server")
			s.EnableServer = false
		}
	}
}

// TODO: replace this with clientcmd
func (s *KubeletServer) createAPIServerClient() (*client.Client, error) {
	authInfo, err := clientauth.LoadFromFile(s.AuthPath)
	if err != nil {
		glog.Warningf("Could not load kubernetes auth path: %v. Continuing with defaults.", err)
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
		glog.Infof("Multiple api servers specified.  Picking first one")
	}
	clientConfig.Host = s.APIServerList[0]
	c, err := client.New(&clientConfig)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// SimpleRunKubelet is a simple way to start a Kubelet talking to dockerEndpoint, using an API Client.
// Under the hood it calls RunKubelet (below)
func SimpleKubelet(client *client.Client,
	dockerClient dockertools.DockerInterface,
	hostname, rootDir, manifestURL, address string,
	port uint,
	masterServiceNamespace string,
	volumePlugins []volume.VolumePlugin,
	tlsOptions *kubelet.TLSOptions,
	cadvisorInterface cadvisor.Interface,
	configFilePath string,
	cloud cloudprovider.Interface) *KubeletConfig {

	imageGCPolicy := kubelet.ImageGCPolicy{
		HighThresholdPercent: 90,
		LowThresholdPercent:  80,
	}
	kcfg := KubeletConfig{
		KubeClient:             client,
		DockerClient:           dockerClient,
		HostnameOverride:       hostname,
		RootDirectory:          rootDir,
		ManifestURL:            manifestURL,
		PodInfraContainerImage: kubelet.PodInfraContainerImage,
		Port:                    port,
		Address:                 util.IP(net.ParseIP(address)),
		EnableServer:            true,
		EnableDebuggingHandlers: true,
		HTTPCheckFrequency:      1 * time.Second,
		FileCheckFrequency:      1 * time.Second,
		SyncFrequency:           3 * time.Second,
		MinimumGCAge:            10 * time.Second,
		MaxPerPodContainerCount: 5,
		MaxContainerCount:       100,
		MasterServiceNamespace:  masterServiceNamespace,
		VolumePlugins:           volumePlugins,
		TLSOptions:              tlsOptions,
		CadvisorInterface:       cadvisorInterface,
		ConfigFile:              configFilePath,
		ImageGCPolicy:           imageGCPolicy,
		Cloud:                   cloud,
	}
	return &kcfg
}

// RunKubelet is responsible for setting up and running a kubelet.  It is used in three different applications:
//   1 Integration tests
//   2 Kubelet binary
//   3 Standalone 'kubernetes' binary
// Eventually, #2 will be replaced with instances of #3
func RunKubelet(kcfg *KubeletConfig) {
	kcfg.Hostname = util.GetHostname(kcfg.HostnameOverride)
	kcfg.Recorder = record.FromSource(api.EventSource{Component: "kubelet", Host: kcfg.Hostname})
	if kcfg.KubeClient != nil {
		kubelet.SetupEventSending(kcfg.KubeClient, kcfg.Hostname)
	} else {
		glog.Infof("No api server defined - no events will be sent.")
	}
	kubelet.SetupLogging()
	kubelet.SetupCapabilities(kcfg.AllowPrivileged, kcfg.HostNetworkSources)

	credentialprovider.SetPreferredDockercfgPath(kcfg.RootDirectory)

	podCfg := makePodSourceConfig(kcfg)
	k, err := createAndInitKubelet(kcfg, podCfg)
	if err != nil {
		glog.Errorf("Failed to create kubelet: %s", err)
		return
	}
	// process pods and exit.
	if kcfg.Runonce {
		if _, err := k.RunOnce(podCfg.Updates()); err != nil {
			glog.Errorf("--runonce failed: %v", err)
		}
	} else {
		startKubelet(k, podCfg, kcfg)
	}
}

func startKubelet(k *kubelet.Kubelet, podCfg *config.PodConfig, kc *KubeletConfig) {
	// start the kubelet
	go util.Forever(func() { k.Run(podCfg.Updates()) }, 0)

	// start the kubelet server
	if kc.EnableServer {
		go util.Forever(func() {
			kubelet.ListenAndServeKubeletServer(k, net.IP(kc.Address), kc.Port, kc.TLSOptions, kc.EnableDebuggingHandlers)
		}, 0)
	}
}

func makePodSourceConfig(kc *KubeletConfig) *config.PodConfig {
	// source of all configuration
	cfg := config.NewPodConfig(config.PodConfigNotificationSnapshotAndUpdates, kc.Recorder)

	// define file config source
	if kc.ConfigFile != "" {
		glog.Infof("Adding manifest file: %v", kc.ConfigFile)
		config.NewSourceFile(kc.ConfigFile, kc.Hostname, kc.FileCheckFrequency, cfg.Channel(kubelet.FileSource))
	}

	// define url config source
	if kc.ManifestURL != "" {
		glog.Infof("Adding manifest url: %v", kc.ManifestURL)
		config.NewSourceURL(kc.ManifestURL, kc.Hostname, kc.HTTPCheckFrequency, cfg.Channel(kubelet.HTTPSource))
	}
	if kc.KubeClient != nil {
		glog.Infof("Watching apiserver")
		config.NewSourceApiserver(kc.KubeClient, kc.Hostname, cfg.Channel(kubelet.ApiserverSource))
	}
	return cfg
}

// KubeletConfig is all of the parameters necessary for running a kubelet.
// TODO: This should probably be merged with KubeletServer.  The extra object is a consequence of refactoring.
type KubeletConfig struct {
	KubeClient                     *client.Client
	DockerClient                   dockertools.DockerInterface
	CadvisorInterface              cadvisor.Interface
	Address                        util.IP
	AllowPrivileged                bool
	HostNetworkSources             []string
	HostnameOverride               string
	RootDirectory                  string
	ConfigFile                     string
	ManifestURL                    string
	FileCheckFrequency             time.Duration
	HTTPCheckFrequency             time.Duration
	Hostname                       string
	PodInfraContainerImage         string
	SyncFrequency                  time.Duration
	RegistryPullQPS                float64
	RegistryBurst                  int
	MinimumGCAge                   time.Duration
	MaxPerPodContainerCount        int
	MaxContainerCount              int
	ClusterDomain                  string
	ClusterDNS                     util.IP
	EnableServer                   bool
	EnableDebuggingHandlers        bool
	Port                           uint
	Runonce                        bool
	MasterServiceNamespace         string
	VolumePlugins                  []volume.VolumePlugin
	NetworkPlugins                 []network.NetworkPlugin
	NetworkPluginName              string
	StreamingConnectionIdleTimeout time.Duration
	Recorder                       record.EventRecorder
	TLSOptions                     *kubelet.TLSOptions
	ImageGCPolicy                  kubelet.ImageGCPolicy
	Cloud                          cloudprovider.Interface
}

func createAndInitKubelet(kc *KubeletConfig, pc *config.PodConfig) (*kubelet.Kubelet, error) {
	// TODO: block until all sources have delivered at least one update to the channel, or break the sync loop
	// up into "per source" synchronizations
	// TODO: KubeletConfig.KubeClient should be a client interface, but client interface misses certain methods
	// used by kubelet. Since NewMainKubelet expects a client interface, we need to make sure we are not passing
	// a nil pointer to it when what we really want is a nil interface.
	var kubeClient client.Interface
	if kc.KubeClient == nil {
		kubeClient = nil
	} else {
		kubeClient = kc.KubeClient
	}

	gcPolicy := kubelet.ContainerGCPolicy{
		MinAge:             kc.MinimumGCAge,
		MaxPerPodContainer: kc.MaxPerPodContainerCount,
		MaxContainers:      kc.MaxContainerCount,
	}

	k, err := kubelet.NewMainKubelet(
		kc.Hostname,
		kc.DockerClient,
		kubeClient,
		kc.RootDirectory,
		kc.PodInfraContainerImage,
		kc.SyncFrequency,
		float32(kc.RegistryPullQPS),
		kc.RegistryBurst,
		gcPolicy,
		pc.SeenAllSources,
		kc.ClusterDomain,
		net.IP(kc.ClusterDNS),
		kc.MasterServiceNamespace,
		kc.VolumePlugins,
		kc.NetworkPlugins,
		kc.NetworkPluginName,
		kc.StreamingConnectionIdleTimeout,
		kc.Recorder,
		kc.CadvisorInterface,
		kc.ImageGCPolicy,
		kc.Cloud)

	if err != nil {
		return nil, err
	}

	k.BirthCry()

	k.StartGarbageCollection()

	return k, nil
}
