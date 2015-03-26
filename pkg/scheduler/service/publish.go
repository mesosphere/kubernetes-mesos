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
	"net"
	"strconv"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/master/ports"

	"github.com/golang/glog"
)

const (
	SCHEDULER_SERVICE_NAME = "k8sm-scheduler"
)

func (m *SchedulerServer) serviceWriterLoop(stop <-chan struct{}) {
	for {
		// Update service & endpoint records.
		// TODO(k8s): when it becomes possible to change this stuff,
		// stop polling and start watching.
		if err := m.createSchedulerServiceIfNeeded(SCHEDULER_SERVICE_NAME, ports.SchedulerPort); err != nil {
			glog.Errorf("Can't create scheduler service: %v", err)
		}

		if err := m.ensureEndpointsContain(SCHEDULER_SERVICE_NAME, net.JoinHostPort(m.Address.String(), strconv.Itoa(m.Port))); err != nil {
			glog.Errorf("Can't create scheduler endpoints: %v", err)
		}

		select {
		case <-stop:
			return
		case <-time.After(10 * time.Second):
		}
	}
}

// createSchedulerServiceIfNeeded will create the specified service if it
// doesn't already exist.
func (m *SchedulerServer) createSchedulerServiceIfNeeded(serviceName string, servicePort int) error {
	ctx := api.NewDefaultContext()
	if _, err := m.client.Services(api.NamespaceValue(ctx)).Get(serviceName); err == nil {
		// The service already exists.
		return nil
	}
	svc := &api.Service{
		ObjectMeta: api.ObjectMeta{
			Name:      serviceName,
			Namespace: api.NamespaceDefault,
			Labels:    map[string]string{"provider": "k8sm", "component": "scheduler"},
		},
		Spec: api.ServiceSpec{
			Port: servicePort,
			// maintained by this code, not by the pod selector
			Selector:        nil,
			Protocol:        api.ProtocolTCP,
			SessionAffinity: api.AffinityTypeNone,
		},
	}
	_, err := m.client.Services(api.NamespaceValue(ctx)).Create(svc)
	if err != nil && errors.IsAlreadyExists(err) {
		err = nil
	}
	return err
}

// ensureEndpointsContain sets the endpoints for the given service. Also removes
// excess endpoints (as determined by m.masterCount). Extra endpoints could appear
// in the list if, for example, the master starts running on a different machine,
// changing IP addresses.
func (m *SchedulerServer) ensureEndpointsContain(serviceName string, endpoint string) error {
	ctx := api.NewDefaultContext()
	e, err := m.client.Endpoints(api.NamespaceValue(ctx)).Get(serviceName)
	if err != nil {
		e = &api.Endpoints{
			ObjectMeta: api.ObjectMeta{
				Name:      serviceName,
				Namespace: api.NamespaceDefault,
			},
		}
		if _, err := m.client.Endpoints(api.NamespaceValue(ctx)).Create(e); err == nil {
			return nil
		}
	}
	found := false
	for i := range e.Endpoints {
		if e.Endpoints[i] == endpoint {
			found = true
			break
		}
	}
	if !found {
		e.Endpoints = append(e.Endpoints, endpoint)
	}
	//TODO(jdef) we have no good way to specify the expected master count, so just hardcode this to
	// 2 for now (minimal required for hot-failover).
	const MASTER_COUNT = 2
	if len(e.Endpoints) > MASTER_COUNT {
		// We append to the end and remove from the beginning, so this should
		// converge rapidly with all masters performing this operation.
		e.Endpoints = e.Endpoints[len(e.Endpoints)-MASTER_COUNT:]
	} else if found {
		// We didn't make any changes, no need to actually call update.
		return nil
	}
	_, err = m.client.Endpoints(api.NamespaceValue(ctx)).Update(e)
	return err
}
