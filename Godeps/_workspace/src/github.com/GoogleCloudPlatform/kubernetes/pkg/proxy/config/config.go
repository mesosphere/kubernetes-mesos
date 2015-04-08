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
	"sync"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/config"
	"github.com/golang/glog"
)

// Operation is a type of operation of services or endpoints.
type Operation int

// These are the available operation types.
const (
	SET Operation = iota
	ADD
	REMOVE
)

// ServiceUpdate describes an operation of services, sent on the channel.
// You can add or remove single services by sending an array of size one and Op == ADD|REMOVE.
// For setting the state of the system to a given state for this source configuration, set Services as desired and Op to SET,
// which will reset the system state to that specified in this operation for this source channel.
// To remove all services, set Services to empty array and Op to SET
type ServiceUpdate struct {
	Services []api.Service
	Op       Operation
}

// EndpointsUpdate describes an operation of endpoints, sent on the channel.
// You can add or remove single endpoints by sending an array of size one and Op == ADD|REMOVE.
// For setting the state of the system to a given state for this source configuration, set Endpoints as desired and Op to SET,
// which will reset the system state to that specified in this operation for this source channel.
// To remove all endpoints, set Endpoints to empty array and Op to SET
type EndpointsUpdate struct {
	Endpoints []api.Endpoints
	Op        Operation
}

// ServiceConfigHandler is an abstract interface of objects which receive update notifications for the set of services.
type ServiceConfigHandler interface {
	// OnUpdate gets called when a configuration has been changed by one of the sources.
	// This is the union of all the configuration sources.
	OnUpdate(services []api.Service)
}

// EndpointsConfigHandler is an abstract interface of objects which receive update notifications for the set of endpoints.
type EndpointsConfigHandler interface {
	// OnUpdate gets called when endpoints configuration is changed for a given
	// service on any of the configuration sources. An example is when a new
	// service comes up, or when containers come up or down for an existing service.
	OnUpdate(endpoints []api.Endpoints)
}

// EndpointsConfig tracks a set of endpoints configurations.
// It accepts "set", "add" and "remove" operations of endpoints via channels, and invokes registered handlers on change.
type EndpointsConfig struct {
	mux     *config.Mux
	bcaster *config.Broadcaster
	store   *endpointsStore
}

// NewEndpointsConfig creates a new EndpointsConfig.
// It immediately runs the created EndpointsConfig.
func NewEndpointsConfig() *EndpointsConfig {
	updates := make(chan struct{})
	store := &endpointsStore{updates: updates, endpoints: make(map[string]map[types.NamespacedName]api.Endpoints)}
	mux := config.NewMux(store)
	bcaster := config.NewBroadcaster()
	go watchForUpdates(bcaster, store, updates)
	return &EndpointsConfig{mux, bcaster, store}
}

func (c *EndpointsConfig) RegisterHandler(handler EndpointsConfigHandler) {
	c.bcaster.Add(config.ListenerFunc(func(instance interface{}) {
		handler.OnUpdate(instance.([]api.Endpoints))
	}))
}

func (c *EndpointsConfig) Channel(source string) chan EndpointsUpdate {
	ch := c.mux.Channel(source)
	endpointsCh := make(chan EndpointsUpdate)
	go func() {
		for update := range endpointsCh {
			ch <- update
		}
		close(ch)
	}()
	return endpointsCh
}

func (c *EndpointsConfig) Config() []api.Endpoints {
	return c.store.MergedState().([]api.Endpoints)
}

type endpointsStore struct {
	endpointLock sync.RWMutex
	endpoints    map[string]map[types.NamespacedName]api.Endpoints
	updates      chan<- struct{}
}

func (s *endpointsStore) Merge(source string, change interface{}) error {
	s.endpointLock.Lock()
	endpoints := s.endpoints[source]
	if endpoints == nil {
		endpoints = make(map[types.NamespacedName]api.Endpoints)
	}
	update := change.(EndpointsUpdate)
	switch update.Op {
	case ADD:
		glog.V(4).Infof("Adding new endpoint from source %s : %+v", source, update.Endpoints)
		for _, value := range update.Endpoints {
			name := types.NamespacedName{value.Namespace, value.Name}
			endpoints[name] = value
		}
	case REMOVE:
		glog.V(4).Infof("Removing an endpoint %+v", update)
		for _, value := range update.Endpoints {
			name := types.NamespacedName{value.Namespace, value.Name}
			delete(endpoints, name)
		}
	case SET:
		glog.V(4).Infof("Setting endpoints %+v", update)
		// Clear the old map entries by just creating a new map
		endpoints = make(map[types.NamespacedName]api.Endpoints)
		for _, value := range update.Endpoints {
			name := types.NamespacedName{value.Namespace, value.Name}
			endpoints[name] = value
		}
	default:
		glog.V(4).Infof("Received invalid update type: %v", update)
	}
	s.endpoints[source] = endpoints
	s.endpointLock.Unlock()
	if s.updates != nil {
		s.updates <- struct{}{}
	}
	return nil
}

func (s *endpointsStore) MergedState() interface{} {
	s.endpointLock.RLock()
	defer s.endpointLock.RUnlock()
	endpoints := make([]api.Endpoints, 0)
	for _, sourceEndpoints := range s.endpoints {
		for _, value := range sourceEndpoints {
			endpoints = append(endpoints, value)
		}
	}
	return endpoints
}

// ServiceConfig tracks a set of service configurations.
// It accepts "set", "add" and "remove" operations of services via channels, and invokes registered handlers on change.
type ServiceConfig struct {
	mux     *config.Mux
	bcaster *config.Broadcaster
	store   *serviceStore
}

// NewServiceConfig creates a new ServiceConfig.
// It immediately runs the created ServiceConfig.
func NewServiceConfig() *ServiceConfig {
	updates := make(chan struct{})
	store := &serviceStore{updates: updates, services: make(map[string]map[types.NamespacedName]api.Service)}
	mux := config.NewMux(store)
	bcaster := config.NewBroadcaster()
	go watchForUpdates(bcaster, store, updates)
	return &ServiceConfig{mux, bcaster, store}
}

func (c *ServiceConfig) RegisterHandler(handler ServiceConfigHandler) {
	c.bcaster.Add(config.ListenerFunc(func(instance interface{}) {
		handler.OnUpdate(instance.([]api.Service))
	}))
}

func (c *ServiceConfig) Channel(source string) chan ServiceUpdate {
	ch := c.mux.Channel(source)
	serviceCh := make(chan ServiceUpdate)
	go func() {
		for update := range serviceCh {
			ch <- update
		}
		close(ch)
	}()
	return serviceCh
}

func (c *ServiceConfig) Config() []api.Service {
	return c.store.MergedState().([]api.Service)
}

type serviceStore struct {
	serviceLock sync.RWMutex
	services    map[string]map[types.NamespacedName]api.Service
	updates     chan<- struct{}
}

func (s *serviceStore) Merge(source string, change interface{}) error {
	s.serviceLock.Lock()
	services := s.services[source]
	if services == nil {
		services = make(map[types.NamespacedName]api.Service)
	}
	update := change.(ServiceUpdate)
	switch update.Op {
	case ADD:
		glog.V(4).Infof("Adding new service from source %s : %+v", source, update.Services)
		for _, value := range update.Services {
			name := types.NamespacedName{value.Namespace, value.Name}
			services[name] = value
		}
	case REMOVE:
		glog.V(4).Infof("Removing a service %+v", update)
		for _, value := range update.Services {
			name := types.NamespacedName{value.Namespace, value.Name}
			delete(services, name)
		}
	case SET:
		glog.V(4).Infof("Setting services %+v", update)
		// Clear the old map entries by just creating a new map
		services = make(map[types.NamespacedName]api.Service)
		for _, value := range update.Services {
			name := types.NamespacedName{value.Namespace, value.Name}
			services[name] = value
		}
	default:
		glog.V(4).Infof("Received invalid update type: %v", update)
	}
	s.services[source] = services
	s.serviceLock.Unlock()
	if s.updates != nil {
		s.updates <- struct{}{}
	}
	return nil
}

func (s *serviceStore) MergedState() interface{} {
	s.serviceLock.RLock()
	defer s.serviceLock.RUnlock()
	services := make([]api.Service, 0)
	for _, sourceServices := range s.services {
		for _, value := range sourceServices {
			services = append(services, value)
		}
	}
	return services
}

// watchForUpdates invokes bcaster.Notify() with the latest version of an object
// when changes occur.
func watchForUpdates(bcaster *config.Broadcaster, accessor config.Accessor, updates <-chan struct{}) {
	for true {
		<-updates
		bcaster.Notify(accessor.MergedState())
	}
}
