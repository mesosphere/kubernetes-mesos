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

package config

import (
	"reflect"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
)

func expectEmptyChannel(t *testing.T, ch <-chan interface{}) {
	select {
	case update := <-ch:
		t.Errorf("Expected no update in channel, Got %v", update)
	default:
	}
}

type sortedPods []kubelet.Pod

func (s sortedPods) Len() int {
	return len(s)
}
func (s sortedPods) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s sortedPods) Less(i, j int) bool {
	if s[i].Namespace < s[j].Namespace {
		return true
	}
	return s[i].Name < s[j].Name
}

func CreateValidPod(name, namespace string) kubelet.Pod {
	return kubelet.Pod{
		Name:      name,
		Namespace: namespace,
		Manifest: api.ContainerManifest{
			Version: "v1beta1",
		},
	}
}

func CreatePodUpdate(op kubelet.PodOperation, pods ...kubelet.Pod) kubelet.PodUpdate {
	newPods := make([]kubelet.Pod, len(pods))
	for i := range pods {
		newPods[i] = pods[i]
	}
	return kubelet.PodUpdate{newPods, op}
}

func createPodConfigTester(mode PodConfigNotificationMode) (chan<- interface{}, <-chan kubelet.PodUpdate, *PodConfig) {
	config := NewPodConfig(mode)
	channel := config.Channel("test")
	ch := config.Updates()
	return channel, ch, config
}

func expectPodUpdate(t *testing.T, ch <-chan kubelet.PodUpdate, expected ...kubelet.PodUpdate) {
	for i := range expected {
		update := <-ch
		if !reflect.DeepEqual(expected[i], update) {
			t.Fatalf("Expected %#v, Got %#v", expected[i], update)
		}
	}
	expectNoPodUpdate(t, ch)
}

func expectNoPodUpdate(t *testing.T, ch <-chan kubelet.PodUpdate) {
	select {
	case update := <-ch:
		t.Errorf("Expected no update in channel, Got %#v", update)
	default:
	}
}

func TestNewPodAdded(t *testing.T) {
	channel, ch, config := createPodConfigTester(PodConfigNotificationIncremental)

	// see an update
	podUpdate := CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", "test")))

	config.Sync()
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, CreateValidPod("foo", "test")))
}

func TestInvalidPodFiltered(t *testing.T) {
	channel, ch, _ := createPodConfigTester(PodConfigNotificationIncremental)

	// see an update
	podUpdate := CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", "test")))

	// add an invalid update
	podUpdate = CreatePodUpdate(kubelet.UPDATE, kubelet.Pod{Name: "foo"})
	channel <- podUpdate
	expectNoPodUpdate(t, ch)
}

func TestNewPodAddedSnapshotAndUpdates(t *testing.T) {
	channel, ch, config := createPodConfigTester(PodConfigNotificationSnapshotAndUpdates)

	// see an set
	podUpdate := CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, CreateValidPod("foo", "test")))

	config.Sync()
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, CreateValidPod("foo", "test")))

	// container updates are separated as UPDATE
	pod := podUpdate.Pods[0]
	pod.Manifest.Containers = []api.Container{{Name: "bar", Image: "test"}}
	channel <- CreatePodUpdate(kubelet.ADD, pod)
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.UPDATE, pod))
}

func TestNewPodAddedSnapshot(t *testing.T) {
	channel, ch, config := createPodConfigTester(PodConfigNotificationSnapshot)

	// see an set
	podUpdate := CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, CreateValidPod("foo", "test")))

	config.Sync()
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, CreateValidPod("foo", "test")))

	// container updates are separated as UPDATE
	pod := podUpdate.Pods[0]
	pod.Manifest.Containers = []api.Container{{Name: "bar", Image: "test"}}
	channel <- CreatePodUpdate(kubelet.ADD, pod)
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, pod))
}

func TestNewPodAddedUpdatedRemoved(t *testing.T) {
	channel, ch, _ := createPodConfigTester(PodConfigNotificationIncremental)

	// should register an add
	podUpdate := CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", "test")))

	// should ignore ADDs that are identical
	expectNoPodUpdate(t, ch)

	// an kubelet.ADD should be converted to kubelet.UPDATE
	pod := CreateValidPod("foo", "test")
	pod.Manifest.Containers = []api.Container{{Name: "bar", Image: "test"}}
	podUpdate = CreatePodUpdate(kubelet.ADD, pod)
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.UPDATE, pod))

	podUpdate = CreatePodUpdate(kubelet.REMOVE, kubelet.Pod{Name: "foo"})
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.REMOVE, pod))
}

func TestNewPodAddedUpdatedSet(t *testing.T) {
	channel, ch, _ := createPodConfigTester(PodConfigNotificationIncremental)

	// should register an add
	podUpdate := CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", ""), CreateValidPod("foo2", ""), CreateValidPod("foo3", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, CreateValidPod("foo", "test"), CreateValidPod("foo2", "test"), CreateValidPod("foo3", "test")))

	// should ignore ADDs that are identical
	expectNoPodUpdate(t, ch)

	// should be converted to an kubelet.ADD, kubelet.REMOVE, and kubelet.UPDATE
	pod := CreateValidPod("foo2", "test")
	pod.Manifest.Containers = []api.Container{{Name: "bar", Image: "test"}}
	podUpdate = CreatePodUpdate(kubelet.SET, pod, CreateValidPod("foo3", ""), CreateValidPod("foo4", "test"))
	channel <- podUpdate
	expectPodUpdate(t, ch,
		CreatePodUpdate(kubelet.REMOVE, CreateValidPod("foo", "test")),
		CreatePodUpdate(kubelet.ADD, CreateValidPod("foo4", "test")),
		CreatePodUpdate(kubelet.UPDATE, pod))
}
