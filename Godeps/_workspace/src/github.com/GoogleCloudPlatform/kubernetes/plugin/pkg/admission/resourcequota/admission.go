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
	"fmt"
	"io"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/admission"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	apierrors "github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/meta"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/resource"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/resourcequota"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
)

func init() {
	admission.RegisterPlugin("ResourceQuota", func(client client.Interface, config io.Reader) (admission.Interface, error) {
		return NewResourceQuota(client), nil
	})
}

type quota struct {
	client  client.Interface
	indexer cache.Indexer
}

func NewResourceQuota(client client.Interface) admission.Interface {
	lw := &cache.ListWatch{
		ListFunc: func() (runtime.Object, error) {
			return client.ResourceQuotas(api.NamespaceAll).List(labels.Everything())
		},
		WatchFunc: func(resourceVersion string) (watch.Interface, error) {
			return client.ResourceQuotas(api.NamespaceAll).Watch(labels.Everything(), fields.Everything(), resourceVersion)
		},
	}
	indexer, reflector := cache.NewNamespaceKeyedIndexerAndReflector(lw, &api.ResourceQuota{}, 0)
	reflector.Run()
	return &quota{client: client, indexer: indexer}
}

var resourceToResourceName = map[string]api.ResourceName{
	"pods":                   api.ResourcePods,
	"services":               api.ResourceServices,
	"replicationControllers": api.ResourceReplicationControllers,
	"resourceQuotas":         api.ResourceQuotas,
}

func (q *quota) Admit(a admission.Attributes) (err error) {
	if a.GetOperation() == "DELETE" {
		return nil
	}

	obj := a.GetObject()
	resource := a.GetResource()
	name := "Unknown"
	if obj != nil {
		name, _ = meta.NewAccessor().Name(obj)
	}

	key := &api.ResourceQuota{
		ObjectMeta: api.ObjectMeta{
			Namespace: a.GetNamespace(),
			Name:      "",
		},
	}
	items, err := q.indexer.Index("namespace", key)
	if err != nil {
		return apierrors.NewForbidden(a.GetResource(), name, fmt.Errorf("Unable to %s %s at this time because there was an error enforcing quota", a.GetOperation(), resource))
	}
	if len(items) == 0 {
		return nil
	}

	for i := range items {
		quota := items[i].(*api.ResourceQuota)

		// we cannot modify the value directly in the cache, so we copy
		status := &api.ResourceQuotaStatus{
			Hard: api.ResourceList{},
			Used: api.ResourceList{},
		}
		for k, v := range quota.Status.Hard {
			status.Hard[k] = *v.Copy()
		}
		for k, v := range quota.Status.Used {
			status.Used[k] = *v.Copy()
		}

		dirty, err := IncrementUsage(a, status, q.client)
		if err != nil {
			return err
		}

		if dirty {
			// construct a usage record
			usage := api.ResourceQuota{
				ObjectMeta: api.ObjectMeta{
					Name:            quota.Name,
					Namespace:       quota.Namespace,
					ResourceVersion: quota.ResourceVersion,
					Labels:          quota.Labels,
					Annotations:     quota.Annotations},
			}
			usage.Status = *status
			_, err = q.client.ResourceQuotas(usage.Namespace).UpdateStatus(&usage)
			if err != nil {
				return apierrors.NewForbidden(a.GetResource(), name, fmt.Errorf("Unable to %s %s at this time because there was an error enforcing quota", a.GetOperation(), a.GetResource()))
			}
		}
	}
	return nil
}

// IncrementUsage updates the supplied ResourceQuotaStatus object based on the incoming operation
// Return true if the usage must be recorded prior to admitting the new resource
// Return an error if the operation should not pass admission control
func IncrementUsage(a admission.Attributes, status *api.ResourceQuotaStatus, client client.Interface) (bool, error) {
	obj := a.GetObject()
	resourceName := a.GetResource()
	name := "Unknown"
	if obj != nil {
		name, _ = meta.NewAccessor().Name(obj)
	}
	dirty := false
	set := map[api.ResourceName]bool{}
	for k := range status.Hard {
		set[k] = true
	}
	// handle max counts for each kind of resource (pods, services, replicationControllers, etc.)
	if a.GetOperation() == "CREATE" {
		resourceName := resourceToResourceName[a.GetResource()]
		hard, hardFound := status.Hard[resourceName]
		if hardFound {
			used, usedFound := status.Used[resourceName]
			if !usedFound {
				return false, apierrors.NewForbidden(a.GetResource(), name, fmt.Errorf("Quota usage stats are not yet known, unable to admit resource until an accurate count is completed."))
			}
			if used.Value() >= hard.Value() {
				return false, apierrors.NewForbidden(a.GetResource(), name, fmt.Errorf("Limited to %s %s", hard.String(), a.GetResource()))
			} else {
				status.Used[resourceName] = *resource.NewQuantity(used.Value()+int64(1), resource.DecimalSI)
				dirty = true
			}
		}
	}
	// handle memory/cpu constraints, and any diff of usage based on memory/cpu on updates
	if a.GetResource() == "pods" && (set[api.ResourceMemory] || set[api.ResourceCPU]) {
		pod := obj.(*api.Pod)
		deltaCPU := resourcequota.PodCPU(pod)
		deltaMemory := resourcequota.PodMemory(pod)
		// if this is an update, we need to find the delta cpu/memory usage from previous state
		if a.GetOperation() == "UPDATE" {
			oldPod, err := client.Pods(a.GetNamespace()).Get(pod.Name)
			if err != nil {
				return false, apierrors.NewForbidden(resourceName, name, err)
			}
			oldCPU := resourcequota.PodCPU(oldPod)
			oldMemory := resourcequota.PodMemory(oldPod)
			deltaCPU = resource.NewMilliQuantity(deltaCPU.MilliValue()-oldCPU.MilliValue(), resource.DecimalSI)
			deltaMemory = resource.NewQuantity(deltaMemory.Value()-oldMemory.Value(), resource.DecimalSI)
		}

		hardMem, hardMemFound := status.Hard[api.ResourceMemory]
		if hardMemFound {
			used, usedFound := status.Used[api.ResourceMemory]
			if !usedFound {
				return false, apierrors.NewForbidden(resourceName, name, fmt.Errorf("Quota usage stats are not yet known, unable to admit resource until an accurate count is completed."))
			}
			if used.Value()+deltaMemory.Value() > hardMem.Value() {
				return false, apierrors.NewForbidden(resourceName, name, fmt.Errorf("Limited to %s memory", hardMem.String()))
			} else {
				status.Used[api.ResourceMemory] = *resource.NewQuantity(used.Value()+deltaMemory.Value(), resource.DecimalSI)
				dirty = true
			}
		}
		hardCPU, hardCPUFound := status.Hard[api.ResourceCPU]
		if hardCPUFound {
			used, usedFound := status.Used[api.ResourceCPU]
			if !usedFound {
				return false, apierrors.NewForbidden(resourceName, name, fmt.Errorf("Quota usage stats are not yet known, unable to admit resource until an accurate count is completed."))
			}
			if used.MilliValue()+deltaCPU.MilliValue() > hardCPU.MilliValue() {
				return false, apierrors.NewForbidden(resourceName, name, fmt.Errorf("Limited to %s CPU", hardCPU.String()))
			} else {
				status.Used[api.ResourceCPU] = *resource.NewMilliQuantity(used.MilliValue()+deltaCPU.MilliValue(), resource.DecimalSI)
				dirty = true
			}
		}
	}
	return dirty, nil
}
