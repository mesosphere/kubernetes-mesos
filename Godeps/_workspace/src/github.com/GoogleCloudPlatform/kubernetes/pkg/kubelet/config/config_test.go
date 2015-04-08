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
	"sort"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
)

const (
	NoneSource = ""
	TestSource = "test"
)

func expectEmptyChannel(t *testing.T, ch <-chan interface{}) {
	select {
	case update := <-ch:
		t.Errorf("Expected no update in channel, Got %v", update)
	default:
	}
}

type sortedPods []api.Pod

func (s sortedPods) Len() int {
	return len(s)
}
func (s sortedPods) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s sortedPods) Less(i, j int) bool {
	return s[i].Namespace < s[j].Namespace
}

func CreateValidPod(name, namespace, source string) api.Pod {
	return api.Pod{
		ObjectMeta: api.ObjectMeta{
			UID:         types.UID(name), // for the purpose of testing, this is unique enough
			Name:        name,
			Namespace:   namespace,
			Annotations: map[string]string{kubelet.ConfigSourceAnnotationKey: source},
		},
		Spec: api.PodSpec{
			RestartPolicy: api.RestartPolicyAlways,
			DNSPolicy:     api.DNSClusterFirst,
			Containers:    []api.Container{{Name: "ctr", Image: "image", ImagePullPolicy: "IfNotPresent"}},
		},
	}
}

func CreatePodUpdate(op kubelet.PodOperation, source string, pods ...api.Pod) kubelet.PodUpdate {
	newPods := make([]api.Pod, len(pods))
	for i := range pods {
		newPods[i] = pods[i]
	}
	return kubelet.PodUpdate{newPods, op, source}
}

func createPodConfigTester(mode PodConfigNotificationMode) (chan<- interface{}, <-chan kubelet.PodUpdate, *PodConfig) {
	config := NewPodConfig(mode, record.FromSource(api.EventSource{Component: "kubelet"}))
	channel := config.Channel(TestSource)
	ch := config.Updates()
	return channel, ch, config
}

func expectPodUpdate(t *testing.T, ch <-chan kubelet.PodUpdate, expected ...kubelet.PodUpdate) {
	for i := range expected {
		update := <-ch
		sort.Sort(sortedPods(update.Pods))
		if !api.Semantic.DeepEqual(expected[i], update) {
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
	podUpdate := CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", "test")))

	config.Sync()
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, kubelet.AllSource, CreateValidPod("foo", "new", "test")))
}

func TestNewPodAddedInvalidNamespace(t *testing.T) {
	channel, ch, config := createPodConfigTester(PodConfigNotificationIncremental)

	// see an update
	podUpdate := CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "", ""))
	channel <- podUpdate
	config.Sync()
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, kubelet.AllSource))
}

func TestNewPodAddedDefaultNamespace(t *testing.T) {
	channel, ch, config := createPodConfigTester(PodConfigNotificationIncremental)

	// see an update
	podUpdate := CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "default", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "default", "test")))

	config.Sync()
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, kubelet.AllSource, CreateValidPod("foo", "default", "test")))
}

func TestNewPodAddedDifferentNamespaces(t *testing.T) {
	channel, ch, config := createPodConfigTester(PodConfigNotificationIncremental)

	// see an update
	podUpdate := CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "default", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "default", "test")))

	// see an update in another namespace
	podUpdate = CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", "test")))

	config.Sync()
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, kubelet.AllSource, CreateValidPod("foo", "default", "test"), CreateValidPod("foo", "new", "test")))
}

func TestInvalidPodFiltered(t *testing.T) {
	channel, ch, _ := createPodConfigTester(PodConfigNotificationIncremental)

	// see an update
	podUpdate := CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", "test")))

	// add an invalid update
	podUpdate = CreatePodUpdate(kubelet.UPDATE, NoneSource, api.Pod{ObjectMeta: api.ObjectMeta{Name: "foo"}})
	channel <- podUpdate
	expectNoPodUpdate(t, ch)
}

func TestNewPodAddedSnapshotAndUpdates(t *testing.T) {
	channel, ch, config := createPodConfigTester(PodConfigNotificationSnapshotAndUpdates)

	// see an set
	podUpdate := CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, TestSource, CreateValidPod("foo", "new", "test")))

	config.Sync()
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, kubelet.AllSource, CreateValidPod("foo", "new", "test")))

	// container updates are separated as UPDATE
	pod := podUpdate.Pods[0]
	pod.Spec.Containers = []api.Container{{Name: "bar", Image: "test", ImagePullPolicy: api.PullIfNotPresent}}
	channel <- CreatePodUpdate(kubelet.ADD, NoneSource, pod)
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.UPDATE, NoneSource, pod))
}

func TestNewPodAddedSnapshot(t *testing.T) {
	channel, ch, config := createPodConfigTester(PodConfigNotificationSnapshot)

	// see an set
	podUpdate := CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, TestSource, CreateValidPod("foo", "new", "test")))

	config.Sync()
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, kubelet.AllSource, CreateValidPod("foo", "new", "test")))

	// container updates are separated as UPDATE
	pod := podUpdate.Pods[0]
	pod.Spec.Containers = []api.Container{{Name: "bar", Image: "test", ImagePullPolicy: api.PullIfNotPresent}}
	channel <- CreatePodUpdate(kubelet.ADD, NoneSource, pod)
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.SET, TestSource, pod))
}

func TestNewPodAddedUpdatedRemoved(t *testing.T) {
	channel, ch, _ := createPodConfigTester(PodConfigNotificationIncremental)

	// should register an add
	podUpdate := CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", "test")))

	// should ignore ADDs that are identical
	expectNoPodUpdate(t, ch)

	// an kubelet.ADD should be converted to kubelet.UPDATE
	pod := CreateValidPod("foo", "new", "test")
	pod.Spec.Containers = []api.Container{{Name: "bar", Image: "test", ImagePullPolicy: api.PullIfNotPresent}}
	podUpdate = CreatePodUpdate(kubelet.ADD, NoneSource, pod)
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.UPDATE, NoneSource, pod))

	podUpdate = CreatePodUpdate(kubelet.REMOVE, NoneSource, api.Pod{ObjectMeta: api.ObjectMeta{Name: "foo", Namespace: "new"}})
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.REMOVE, NoneSource, pod))
}

func TestNewPodAddedUpdatedSet(t *testing.T) {
	channel, ch, _ := createPodConfigTester(PodConfigNotificationIncremental)

	// should register an add
	podUpdate := CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", ""), CreateValidPod("foo2", "new", ""), CreateValidPod("foo3", "new", ""))
	channel <- podUpdate
	expectPodUpdate(t, ch, CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo", "new", "test"), CreateValidPod("foo2", "new", "test"), CreateValidPod("foo3", "new", "test")))

	// should ignore ADDs that are identical
	expectNoPodUpdate(t, ch)

	// should be converted to an kubelet.ADD, kubelet.REMOVE, and kubelet.UPDATE
	pod := CreateValidPod("foo2", "new", "test")
	pod.Spec.Containers = []api.Container{{Name: "bar", Image: "test", ImagePullPolicy: api.PullIfNotPresent}}
	podUpdate = CreatePodUpdate(kubelet.SET, NoneSource, pod, CreateValidPod("foo3", "new", ""), CreateValidPod("foo4", "new", "test"))
	channel <- podUpdate
	expectPodUpdate(t, ch,
		CreatePodUpdate(kubelet.REMOVE, NoneSource, CreateValidPod("foo", "new", "test")),
		CreatePodUpdate(kubelet.ADD, NoneSource, CreateValidPod("foo4", "new", "test")),
		CreatePodUpdate(kubelet.UPDATE, NoneSource, pod))
}
