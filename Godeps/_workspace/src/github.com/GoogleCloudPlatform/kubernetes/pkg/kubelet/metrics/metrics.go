/*
Copyright 2015 Google Inc. All rights reserved.

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

package metrics

import (
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/dockertools"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
)

const kubeletSubsystem = "kubelet"

var (
	ImagePullLatency = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Subsystem: kubeletSubsystem,
			Name:      "image_pull_latency_microseconds",
			Help:      "Image pull latency in microseconds.",
		},
	)
	ContainersPerPodCount = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Subsystem: kubeletSubsystem,
			Name:      "containers_per_pod_count",
			Help:      "The number of containers per pod.",
		},
	)
	SyncPodLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Subsystem: kubeletSubsystem,
			Name:      "sync_pod_latency_microseconds",
			Help:      "Latency in microseconds to sync a single pod. Broken down by operation type: create, update, or sync",
		},
		[]string{"operation_type"},
	)
	SyncPodsLatency = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Subsystem: kubeletSubsystem,
			Name:      "sync_pods_latency_microseconds",
			Help:      "Latency in microseconds to sync all pods.",
		},
	)
	DockerOperationsLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Subsystem: kubeletSubsystem,
			Name:      "docker_operations_latency_microseconds",
			Help:      "Latency in microseconds of Docker operations. Broken down by operation type.",
		},
		[]string{"operation_type"},
	)
)

var registerMetrics sync.Once

// Register all metrics.
func Register(containerCache dockertools.DockerCache) {
	// Register the metrics.
	registerMetrics.Do(func() {
		prometheus.MustRegister(ImagePullLatency)
		prometheus.MustRegister(SyncPodLatency)
		prometheus.MustRegister(DockerOperationsLatency)
		prometheus.MustRegister(SyncPodsLatency)
		prometheus.MustRegister(ContainersPerPodCount)
		prometheus.MustRegister(newPodAndContainerCollector(containerCache))
	})
}

type SyncPodType int

const (
	SyncPodCreate SyncPodType = iota
	SyncPodUpdate
	SyncPodSync
)

func (self SyncPodType) String() string {
	switch self {
	case SyncPodCreate:
		return "create"
	case SyncPodUpdate:
		return "update"
	case SyncPodSync:
		return "sync"
	default:
		return "unknown"
	}
}

// Gets the time since the specified start in microseconds.
func SinceInMicroseconds(start time.Time) float64 {
	return float64(time.Since(start).Nanoseconds() / time.Microsecond.Nanoseconds())
}

func newPodAndContainerCollector(containerCache dockertools.DockerCache) *podAndContainerCollector {
	return &podAndContainerCollector{
		containerCache: containerCache,
	}
}

// Custom collector for current pod and container counts.
type podAndContainerCollector struct {
	// Cache for accessing information about running containers.
	containerCache dockertools.DockerCache
}

// TODO(vmarmol): Split by source?
var (
	runningPodCountDesc = prometheus.NewDesc(
		prometheus.BuildFQName("", kubeletSubsystem, "running_pod_count"),
		"Number of pods currently running",
		nil, nil)
	runningContainerCountDesc = prometheus.NewDesc(
		prometheus.BuildFQName("", kubeletSubsystem, "running_container_count"),
		"Number of containers currently running",
		nil, nil)
)

func (self *podAndContainerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- runningPodCountDesc
	ch <- runningContainerCountDesc
}

func (self *podAndContainerCollector) Collect(ch chan<- prometheus.Metric) {
	runningPods, err := self.containerCache.GetPods()
	if err != nil {
		glog.Warning("Failed to get running container information while collecting metrics: %v", err)
		return
	}

	runningContainers := 0
	for _, p := range runningPods {
		runningContainers += len(p.Containers)
	}
	ch <- prometheus.MustNewConstMetric(
		runningPodCountDesc,
		prometheus.GaugeValue,
		float64(len(runningPods)))
	ch <- prometheus.MustNewConstMetric(
		runningContainerCountDesc,
		prometheus.GaugeValue,
		float64(runningContainers))
}
