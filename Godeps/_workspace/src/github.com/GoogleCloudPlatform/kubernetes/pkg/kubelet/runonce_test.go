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

package kubelet

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/cadvisor"
	kubecontainer "github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/container"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/dockertools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/network"
	docker "github.com/fsouza/go-dockerclient"
	cadvisorApi "github.com/google/cadvisor/info/v1"
)

type listContainersResult struct {
	label      string
	containers []docker.APIContainers
	err        error
}

type inspectContainersResult struct {
	label     string
	container docker.Container
	err       error
}

type testDocker struct {
	listContainersResults    []listContainersResult
	inspectContainersResults []inspectContainersResult
	dockertools.FakeDockerClient
	t *testing.T
}

func (d *testDocker) ListContainers(options docker.ListContainersOptions) ([]docker.APIContainers, error) {
	if len(d.listContainersResults) > 0 {
		result := d.listContainersResults[0]
		d.listContainersResults = d.listContainersResults[1:]
		d.t.Logf("ListContainers: %q, returning: (%v, %v)", result.label, result.containers, result.err)
		return result.containers, result.err
	}
	return nil, fmt.Errorf("ListContainers error: no more test results")
}

func (d *testDocker) InspectContainer(id string) (*docker.Container, error) {
	if len(d.inspectContainersResults) > 0 {
		result := d.inspectContainersResults[0]
		d.inspectContainersResults = d.inspectContainersResults[1:]
		d.t.Logf("InspectContainers: %q, returning: (%v, %v)", result.label, result.container, result.err)
		return &result.container, result.err
	}
	return nil, fmt.Errorf("InspectContainer error: no more test results")
}

func TestRunOnce(t *testing.T) {
	cadvisor := &cadvisor.Mock{}
	cadvisor.On("MachineInfo").Return(&cadvisorApi.MachineInfo{}, nil)
	kb := &Kubelet{
		rootDirectory:       "/tmp/kubelet",
		recorder:            &record.FakeRecorder{},
		cadvisor:            cadvisor,
		nodeLister:          testNodeLister{},
		statusManager:       newStatusManager(nil),
		containerRefManager: kubecontainer.NewRefManager(),
	}

	kb.networkPlugin, _ = network.InitNetworkPlugin([]network.NetworkPlugin{}, "", network.NewFakeHost(nil))
	if err := kb.setupDataDirs(); err != nil {
		t.Errorf("Failed to init data dirs: %v", err)
	}
	podContainers := []docker.APIContainers{
		{
			Names:  []string{"/k8s_bar." + strconv.FormatUint(dockertools.HashContainer(&api.Container{Name: "bar"}), 16) + "_foo_new_12345678_42"},
			ID:     "1234",
			Status: "running",
		},
		{
			Names:  []string{"/k8s_net_foo.new.test_abcdefgh_42"},
			ID:     "9876",
			Status: "running",
		},
	}
	kb.dockerClient = &testDocker{
		listContainersResults: []listContainersResult{
			{label: "list pod container", containers: []docker.APIContainers{}},
			{label: "syncPod", containers: []docker.APIContainers{}},
			{label: "list pod container", containers: []docker.APIContainers{}},
			{label: "syncPod", containers: podContainers},
			{label: "list pod container", containers: podContainers},
		},
		inspectContainersResults: []inspectContainersResult{
			{
				label: "syncPod",
				container: docker.Container{
					Config: &docker.Config{Image: "someimage"},
					State:  docker.State{Running: true, Pid: 42},
				},
			},
			{
				label: "syncPod",
				container: docker.Container{
					Config: &docker.Config{Image: "someimage"},
					State:  docker.State{Running: true, Pid: 42},
				},
			},
			{
				label: "syncPod",
				container: docker.Container{
					Config: &docker.Config{Image: "someimage"},
					State:  docker.State{Running: true, Pid: 42},
				},
			},
			{
				label: "syncPod",
				container: docker.Container{
					Config: &docker.Config{Image: "someimage"},
					State:  docker.State{Running: true, Pid: 42},
				},
			},
		},
		t: t,
	}
	kb.dockerPuller = &dockertools.FakeDockerPuller{}
	results, err := kb.runOnce([]api.Pod{
		{
			ObjectMeta: api.ObjectMeta{
				UID:       "12345678",
				Name:      "foo",
				Namespace: "new",
			},
			Spec: api.PodSpec{
				Containers: []api.Container{
					{Name: "bar"},
				},
			},
		},
	}, time.Millisecond)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if results[0].Err != nil {
		t.Errorf("unexpected run pod error: %v", results[0].Err)
	}
	if results[0].Pod.Name != "foo" {
		t.Errorf("unexpected pod: %q", results[0].Pod.Name)
	}
}
