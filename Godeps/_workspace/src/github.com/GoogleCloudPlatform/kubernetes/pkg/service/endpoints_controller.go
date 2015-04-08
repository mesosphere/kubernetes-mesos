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
	"fmt"
	"reflect"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/endpoints"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/v1beta1"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/v1beta2"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	"github.com/golang/glog"
)

// EndpointController manages selector-based service endpoints.
type EndpointController struct {
	client *client.Client
}

// NewEndpointController returns a new *EndpointController.
func NewEndpointController(client *client.Client) *EndpointController {
	return &EndpointController{
		client: client,
	}
}

// SyncServiceEndpoints syncs endpoints for services with selectors.
func (e *EndpointController) SyncServiceEndpoints() error {
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
			if len(pod.Status.PodIP) == 0 {
				glog.Errorf("Failed to find an IP for pod %s/%s", pod.Namespace, pod.Name)
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

			epp := api.EndpointPort{Name: portName, Port: portNum, Protocol: portProto}
			epa := api.EndpointAddress{IP: pod.Status.PodIP, TargetRef: &api.ObjectReference{
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
		currentEndpoints, err := e.client.Endpoints(service.Namespace).Get(service.Name)
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

		if len(currentEndpoints.ResourceVersion) == 0 {
			// No previous endpoints, create them
			_, err = e.client.Endpoints(service.Namespace).Create(newEndpoints)
		} else {
			// Pre-existing
			_, err = e.client.Endpoints(service.Namespace).Update(newEndpoints)
		}
		if err != nil {
			glog.Errorf("Error updating endpoints: %v", err)
			continue
		}
	}
	return resultErr
}

func findDefaultPort(pod *api.Pod, servicePort int, proto api.Protocol) int {
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.Protocol == proto {
				return port.ContainerPort
			}
		}
	}
	return servicePort
}

// findPort locates the container port for the given manifest and portName.
// If the targetPort is a non-zero number, use that.  If the targetPort is 0 or
// not specified, use the first defined port with the same protocol.  If no port
// is defined, use the service's port.  If the targetPort is an empty string use
// the first defined port with the same protocol.  If no port is defined, use
// the service's port.  If the targetPort is a non-empty string, look that
// string up in all named ports in all containers in the target pod.  If no
// match is found, fail.
func findPort(pod *api.Pod, service *api.Service) (int, error) {
	portName := service.Spec.TargetPort
	switch portName.Kind {
	case util.IntstrString:
		if len(portName.StrVal) == 0 {
			return findDefaultPort(pod, service.Spec.Port, service.Spec.Protocol), nil
		}
		name := portName.StrVal
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == name && port.Protocol == service.Spec.Protocol {
					return port.ContainerPort, nil
				}
			}
		}
	case util.IntstrInt:
		if portName.IntVal == 0 {
			return findDefaultPort(pod, service.Spec.Port, service.Spec.Protocol), nil
		}
		return portName.IntVal, nil
	}

	return 0, fmt.Errorf("no suitable port for manifest: %s", pod.UID)
}
