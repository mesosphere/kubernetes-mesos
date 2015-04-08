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

package kubelet

import (
	"sync"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	kubecontainer "github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/container"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

type fakeMirrorClient struct {
	mirrorPodLock sync.RWMutex
	// Note that a real mirror manager does not store the mirror pods in
	// itself. This fake manager does this to track calls.
	mirrorPods   util.StringSet
	createCounts map[string]int
	deleteCounts map[string]int
}

func (self *fakeMirrorClient) CreateMirrorPod(pod api.Pod, _ string) error {
	self.mirrorPodLock.Lock()
	defer self.mirrorPodLock.Unlock()
	podFullName := kubecontainer.GetPodFullName(&pod)
	self.mirrorPods.Insert(podFullName)
	self.createCounts[podFullName]++
	return nil
}

func (self *fakeMirrorClient) DeleteMirrorPod(podFullName string) error {
	self.mirrorPodLock.Lock()
	defer self.mirrorPodLock.Unlock()
	self.mirrorPods.Delete(podFullName)
	self.deleteCounts[podFullName]++
	return nil
}

func newFakeMirrorClient() *fakeMirrorClient {
	m := fakeMirrorClient{}
	m.mirrorPods = util.NewStringSet()
	m.createCounts = make(map[string]int)
	m.deleteCounts = make(map[string]int)
	return &m
}

func (self *fakeMirrorClient) HasPod(podFullName string) bool {
	self.mirrorPodLock.RLock()
	defer self.mirrorPodLock.RUnlock()
	return self.mirrorPods.Has(podFullName)
}

func (self *fakeMirrorClient) NumOfPods() int {
	self.mirrorPodLock.RLock()
	defer self.mirrorPodLock.RUnlock()
	return self.mirrorPods.Len()
}

func (self *fakeMirrorClient) GetPods() []string {
	self.mirrorPodLock.RLock()
	defer self.mirrorPodLock.RUnlock()
	return self.mirrorPods.List()
}

func (self *fakeMirrorClient) GetCounts(podFullName string) (int, int) {
	self.mirrorPodLock.RLock()
	defer self.mirrorPodLock.RUnlock()
	return self.createCounts[podFullName], self.deleteCounts[podFullName]
}

func TestParsePodFullName(t *testing.T) {
	type nameTuple struct {
		Name      string
		Namespace string
	}
	successfulCases := map[string]nameTuple{
		"bar_foo":         {Name: "bar", Namespace: "foo"},
		"bar.org_foo.com": {Name: "bar.org", Namespace: "foo.com"},
		"bar-bar_foo":     {Name: "bar-bar", Namespace: "foo"},
	}
	failedCases := []string{"barfoo", "bar_foo_foo", ""}

	for podFullName, expected := range successfulCases {
		name, namespace, err := kubecontainer.ParsePodFullName(podFullName)
		if err != nil {
			t.Errorf("unexpected error when parsing the full name: %v", err)
			continue
		}
		if name != expected.Name || namespace != expected.Namespace {
			t.Errorf("expected name %q, namespace %q; got name %q, namespace %q",
				expected.Name, expected.Namespace, name, namespace)
		}
	}
	for _, podFullName := range failedCases {
		_, _, err := kubecontainer.ParsePodFullName(podFullName)
		if err == nil {
			t.Errorf("expected error when parsing the full name, got none")
		}
	}
}
