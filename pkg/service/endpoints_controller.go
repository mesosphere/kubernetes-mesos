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
	"net"
	"strconv"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	"github.com/golang/glog"
)

type EndpointController interface {
	SyncServiceEndpoints() error
}

// EndpointController manages service endpoints.
type endpointController struct {
	client *client.Client
}

// NewEndpointController returns a new *EndpointController.
func NewEndpointController(client *client.Client) EndpointController {
	return &endpointController{
		client: client,
	}
}

// SyncServiceEndpoints syncs service endpoints.
func (e *endpointController) SyncServiceEndpoints() error {
	services, err := e.client.Services(api.NamespaceAll).List(labels.Everything())
	if err != nil {
		glog.Errorf("Failed to list services: %v", err)
		return err
	}
	var resultErr error
	for _, service := range services.Items {
		if service.Name == "kubernetes" || service.Name == "kubernetes-ro" {
			// HACK(k8s): This is a temporary hack for supporting the master services
			// until we actually start running apiserver in a pod.
			continue
		}
		glog.Infof("About to update endpoints for service %v", service.Name)
		pods, err := e.client.Pods(service.Namespace).List(labels.Set(service.Spec.Selector).AsSelector())
		if err != nil {
			glog.Errorf("Error syncing service: %#v, skipping.", service)
			resultErr = err
			continue
		}
		endpoints := []string{}
		for _, pod := range pods.Items {
			// HACK(jdef): looks up a HostPort in the container, either by port-name or matching HostPort
			port, err := findPort(&pod, service.Spec.ContainerPort)
			if err != nil {
				glog.Errorf("Failed to find port for service: %+v, %v", service, err)
				continue
			}
			// HACK(jdef): use HostIP instead of pod.CurrentState.PodIP for generic mesos compat
			if len(pod.Status.HostIP) == 0 {
				glog.Errorf("Failed to find a host IP for pod: %+v", pod)
				continue
			}
			endpoints = append(endpoints, net.JoinHostPort(pod.Status.HostIP, strconv.Itoa(port)))
		}
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
		newEndpoints := &api.Endpoints{}
		*newEndpoints = *currentEndpoints
		newEndpoints.Endpoints = endpoints

		if len(currentEndpoints.ResourceVersion) == 0 {
			// No previous endpoints, create them
			_, err = e.client.Endpoints(service.Namespace).Create(newEndpoints)
		} else {
			// Pre-existing
			if endpointsEqual(currentEndpoints, endpoints) {
				glog.V(2).Infof("endpoints are equal for %s, skipping update", service.Name)
				continue
			}
			_, err = e.client.Endpoints(service.Namespace).Update(newEndpoints)
		}
		if err != nil {
			glog.Errorf("Error updating endpoints: %v", err)
			continue
		}
	}
	return resultErr
}

func containsEndpoint(endpoints *api.Endpoints, endpoint string) bool {
	if endpoints == nil {
		return false
	}
	for ix := range endpoints.Endpoints {
		if endpoints.Endpoints[ix] == endpoint {
			return true
		}
	}
	return false
}

func endpointsEqual(e *api.Endpoints, endpoints []string) bool {
	if len(e.Endpoints) != len(endpoints) {
		return false
	}
	for _, endpoint := range endpoints {
		if !containsEndpoint(e, endpoint) {
			return false
		}
	}
	return true
}

// findPort locates the Host port for the given manifest and portName.
// HACK(jdef): return the HostPort instead of the ContainerPort for generic mesos compat.
func findPort(pod *api.Pod, portName util.IntOrString) (int, error) {
	firstHostPort := 0
	if len(pod.Spec.Containers) > 0 && len(pod.Spec.Containers[0].Ports) > 0 {
		firstHostPort = pod.Spec.Containers[0].Ports[0].HostPort
	}

	switch portName.Kind {
	case util.IntstrString:
		if len(portName.StrVal) == 0 {
			if firstHostPort != 0 {
				return firstHostPort, nil
			}
			break
		}
		name := portName.StrVal
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == name {
					return port.HostPort, nil
				}
			}
		}
		return -1, fmt.Errorf("no suitable port %s for manifest: %s", name, pod.UID)
	case util.IntstrInt:
		if portName.IntVal == 0 {
			if firstHostPort != 0 {
				return firstHostPort, nil
			}
			break
		}
		// HACK(jdef): slightly different semantics from upstream here:
		// we ensure that if the user spec'd a port in the service that
		// it actually maps to a host-port declared in the pod. upstream
		// doesn't check this and happily returns the port spec'd in the
		// service.
		p := portName.IntVal
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.HostPort == p {
					return p, nil
				}
			}
		}
		return -1, fmt.Errorf("no suitable port %d for manifest: %s", p, pod.UID)
	}
	// should never get this far..
	return -1, fmt.Errorf("no suitable port for manifest: %s", pod.UID)
}
