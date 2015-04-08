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

package resourcequota

import (
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/resource"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

func getResourceRequirements(cpu, memory string) api.ResourceRequirements {
	res := api.ResourceRequirements{}
	res.Limits = api.ResourceList{}
	if cpu != "" {
		res.Limits[api.ResourceCPU] = resource.MustParse(cpu)
	}
	if memory != "" {
		res.Limits[api.ResourceMemory] = resource.MustParse(memory)
	}

	return res
}

func TestFilterQuotaPods(t *testing.T) {
	pods := []api.Pod{
		{
			ObjectMeta: api.ObjectMeta{Name: "pod-running"},
			Status:     api.PodStatus{Phase: api.PodRunning},
		},
		{
			ObjectMeta: api.ObjectMeta{Name: "pod-pending"},
			Status:     api.PodStatus{Phase: api.PodPending},
		},
		{
			ObjectMeta: api.ObjectMeta{Name: "pod-succeeded"},
			Status:     api.PodStatus{Phase: api.PodSucceeded},
		},
		{
			ObjectMeta: api.ObjectMeta{Name: "pod-unknown"},
			Status:     api.PodStatus{Phase: api.PodUnknown},
		},
		{
			ObjectMeta: api.ObjectMeta{Name: "pod-failed"},
			Status:     api.PodStatus{Phase: api.PodFailed},
		},
		{
			ObjectMeta: api.ObjectMeta{Name: "pod-failed-with-restart-always"},
			Spec: api.PodSpec{
				RestartPolicy: api.RestartPolicyAlways,
			},
			Status: api.PodStatus{Phase: api.PodFailed},
		},
		{
			ObjectMeta: api.ObjectMeta{Name: "pod-failed-with-restart-on-failure"},
			Spec: api.PodSpec{
				RestartPolicy: api.RestartPolicyOnFailure,
			},
			Status: api.PodStatus{Phase: api.PodFailed},
		},
		{
			ObjectMeta: api.ObjectMeta{Name: "pod-failed-with-restart-never"},
			Spec: api.PodSpec{
				RestartPolicy: api.RestartPolicyNever,
			},
			Status: api.PodStatus{Phase: api.PodFailed},
		},
	}
	expectedResults := util.NewStringSet("pod-running",
		"pod-pending", "pod-unknown", "pod-failed-with-restart-always",
		"pod-failed-with-restart-on-failure")

	actualResults := util.StringSet{}
	result := FilterQuotaPods(pods)
	for i := range result {
		actualResults.Insert(result[i].Name)
	}

	if len(expectedResults) != len(actualResults) || !actualResults.HasAll(expectedResults.List()...) {
		t.Errorf("Expected results %v, Actual results %v", expectedResults, actualResults)
	}
}

func TestSyncResourceQuota(t *testing.T) {
	podList := api.PodList{
		Items: []api.Pod{
			{
				ObjectMeta: api.ObjectMeta{Name: "pod-running"},
				Status:     api.PodStatus{Phase: api.PodRunning},
				Spec: api.PodSpec{
					Volumes:    []api.Volume{{Name: "vol"}},
					Containers: []api.Container{{Name: "ctr", Image: "image", Resources: getResourceRequirements("100m", "1Gi")}},
				},
			},
			{
				ObjectMeta: api.ObjectMeta{Name: "pod-running-2"},
				Status:     api.PodStatus{Phase: api.PodRunning},
				Spec: api.PodSpec{
					Volumes:    []api.Volume{{Name: "vol"}},
					Containers: []api.Container{{Name: "ctr", Image: "image", Resources: getResourceRequirements("100m", "1Gi")}},
				},
			},
			{
				ObjectMeta: api.ObjectMeta{Name: "pod-failed"},
				Status:     api.PodStatus{Phase: api.PodFailed},
				Spec: api.PodSpec{
					Volumes:    []api.Volume{{Name: "vol"}},
					Containers: []api.Container{{Name: "ctr", Image: "image", Resources: getResourceRequirements("100m", "1Gi")}},
				},
			},
		},
	}
	quota := api.ResourceQuota{
		Spec: api.ResourceQuotaSpec{
			Hard: api.ResourceList{
				api.ResourceCPU:    resource.MustParse("3"),
				api.ResourceMemory: resource.MustParse("100Gi"),
				api.ResourcePods:   resource.MustParse("5"),
			},
		},
	}
	expectedUsage := api.ResourceQuota{
		Status: api.ResourceQuotaStatus{
			Hard: api.ResourceList{
				api.ResourceCPU:    resource.MustParse("3"),
				api.ResourceMemory: resource.MustParse("100Gi"),
				api.ResourcePods:   resource.MustParse("5"),
			},
			Used: api.ResourceList{
				api.ResourceCPU:    resource.MustParse("200m"),
				api.ResourceMemory: resource.MustParse("2147483648"),
				api.ResourcePods:   resource.MustParse("2"),
			},
		},
	}

	kubeClient := &client.Fake{
		PodsList: podList,
	}

	resourceQuotaManager := NewResourceQuotaManager(kubeClient)
	err := resourceQuotaManager.syncResourceQuota(quota)
	if err != nil {
		t.Errorf("Unexpected error %v", err)
	}

	usage := kubeClient.ResourceQuotaStatus

	// ensure hard and used limits are what we expected
	for k, v := range expectedUsage.Status.Hard {
		actual := usage.Status.Hard[k]
		actualValue := actual.String()
		expectedValue := v.String()
		if expectedValue != actualValue {
			t.Errorf("Usage Hard: Key: %v, Expected: %v, Actual: %v", k, expectedValue, actualValue)
		}
	}
	for k, v := range expectedUsage.Status.Used {
		actual := usage.Status.Used[k]
		actualValue := actual.String()
		expectedValue := v.String()
		if expectedValue != actualValue {
			t.Errorf("Usage Used: Key: %v, Expected: %v, Actual: %v", k, expectedValue, actualValue)
		}
	}

}
