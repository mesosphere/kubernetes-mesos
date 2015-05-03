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

package service

import (
	"reflect"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/endpoints"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/v1beta1"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/v1beta2"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"

	"github.com/golang/glog"
)

type DNSEndpointsClient interface {
	pollDNS(serviceName, serviceNamespace string) (*api.Endpoints, error)
	pushToDNS(serviceNamespace string, ep *api.Endpoints) error
}

// EndpointController manages selector-based service endpoints.
type dnsEndpointController struct {
	client *client.Client
	dns    DNSEndpointsClient
}

// NewEndpointController returns a new *EndpointController.
func NewDNSEndpointController(client *client.Client, dns DNSEndpointsClient) EndpointController {
	return &dnsEndpointController{
		client: client,
		dns:    dns,
	}
}

// TODO(jdef) largely copy/pasted from endpoints_controller.go, but if I refactor then rebasing to upstream
// becomes much more challenging. Keeping copy/paste for now, can refactor once this is upstreamed.
//
// SyncServiceEndpoints syncs endpoints for services with selectors.
func (e *dnsEndpointController) SyncServiceEndpoints() error {
	services, err := e.client.Services(api.NamespaceAll).List(labels.Everything())
	if err != nil {
		glog.Errorf("Failed to list services: %v", err)
		return err
	}
	var resultErr error
	for i := range services.Items {
		service := &services.Items[i]

		if service.Spec.Selector == nil {
			// services without a selector receive no endpoints from this controller;
			// these services will receive the endpoints that are created out-of-band via the REST API.
			continue
		}

		glog.V(5).Infof("About to update endpoints for service %s/%s", service.Namespace, service.Name)
		pods, err := e.client.Pods(service.Namespace).List(labels.Set(service.Spec.Selector).AsSelector())
		if err != nil {
			glog.Errorf("Error syncing service: %s/%s, skipping", service.Namespace, service.Name)
			resultErr = err
			continue
		}

		subsets := []api.EndpointSubset{}
		for i := range pods.Items {
			pod := &pods.Items[i]

			// TODO: Once v1beta1 and v1beta2 are EOL'ed, this can
			// assume that service.Spec.TargetPort is populated.
			_ = v1beta1.Dependency
			_ = v1beta2.Dependency
			// TODO: Add multiple-ports to Service and expose them here.
			portName := ""
			portProto := service.Spec.Protocol
			portNum, err := findPort(pod, service)
			if err != nil {
				glog.Errorf("Failed to find port for service %s/%s: %v", service.Namespace, service.Name, err)
				continue
			}
			// HACK(jdef): use HostIP instead of pod.CurrentState.PodIP for generic mesos compat
			if len(pod.Status.HostIP) == 0 {
				glog.Errorf("Failed to find a host IP for pod %s/%s", pod.Namespace, pod.Name)
				continue
			}

			inService := false
			for _, c := range pod.Status.Conditions {
				if c.Type == api.PodReady && c.Status == api.ConditionTrue {
					inService = true
					break
				}
			}
			if !inService {
				glog.V(5).Infof("Pod is out of service: %v/%v", pod.Namespace, pod.Name)
				continue
			}

			// HACK(jdef): use HostIP instead of pod.CurrentState.PodIP for generic mesos compat
			epp := api.EndpointPort{Name: portName, Port: portNum, Protocol: portProto}
			epa := api.EndpointAddress{IP: pod.Status.HostIP, TargetRef: &api.ObjectReference{
				Kind:            "Pod",
				Namespace:       pod.ObjectMeta.Namespace,
				Name:            pod.ObjectMeta.Name,
				UID:             pod.ObjectMeta.UID,
				ResourceVersion: pod.ObjectMeta.ResourceVersion,
			}}
			subsets = append(subsets, api.EndpointSubset{Addresses: []api.EndpointAddress{epa}, Ports: []api.EndpointPort{epp}})
		}
		subsets = endpoints.RepackSubsets(subsets)

		// See if there's actually an update here.
		currentEndpoints, err := e.dns.pollDNS(service.Name, service.Namespace)
		if err != nil {
			if errors.IsNotFound(err) {
				currentEndpoints = &api.Endpoints{
					ObjectMeta: api.ObjectMeta{
						Name: service.Name,
					},
				}
			} else {
				glog.Errorf("Error getting endpoints: %v", err)
				continue
			}
		}
		if reflect.DeepEqual(currentEndpoints.Subsets, subsets) {
			glog.V(5).Infof("endpoints are equal for %s/%s, skipping update", service.Namespace, service.Name)
			continue
		}
		newEndpoints := currentEndpoints
		newEndpoints.Subsets = subsets

		err = e.dns.pushToDNS(service.Namespace, newEndpoints)
		if err != nil {
			glog.Errorf("Error updating endpoints: %v", err)
			continue
		}
	}
	return resultErr
}
