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

package etcd

import (
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/latest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/rest/resttest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	"github.com/coreos/go-etcd/etcd"
)

func newHelper(t *testing.T) (*tools.FakeEtcdClient, tools.EtcdHelper) {
	fakeEtcdClient := tools.NewFakeEtcdClient(t)
	fakeEtcdClient.TestIndex = true
	helper := tools.NewEtcdHelper(fakeEtcdClient, latest.Codec)
	return fakeEtcdClient, helper
}

func newStorage(t *testing.T) (*REST, *tools.FakeEtcdClient) {
	fakeEtcdClient, h := newHelper(t)
	storage := NewStorage(h)
	return storage, fakeEtcdClient
}

func validNewEndpoints() *api.Endpoints {
	return &api.Endpoints{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			Namespace: api.NamespaceDefault,
		},
		Subsets: []api.EndpointSubset{{
			Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
			Ports:     []api.EndpointPort{{Port: 80, Protocol: "TCP"}},
		}},
	}
}

func validChangedEndpoints() *api.Endpoints {
	endpoints := validNewEndpoints()
	endpoints.ResourceVersion = "1"
	endpoints.Subsets = []api.EndpointSubset{{
		Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}, {IP: "5.6.7.8"}},
		Ports:     []api.EndpointPort{{Port: 80, Protocol: "TCP"}},
	}}
	return endpoints
}

func TestCreate(t *testing.T) {
	storage, fakeEtcdClient := newStorage(t)
	test := resttest.New(t, storage, fakeEtcdClient.SetError)
	endpoints := validNewEndpoints()
	endpoints.ObjectMeta = api.ObjectMeta{}
	test.TestCreate(
		// valid
		endpoints,
		// invalid
		&api.Endpoints{
			ObjectMeta: api.ObjectMeta{Name: "_-a123-a_"},
		},
	)
}

func TestDelete(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeEtcdClient := newStorage(t)
	test := resttest.New(t, storage, fakeEtcdClient.SetError)

	endpoints := validChangedEndpoints()
	key, _ := storage.KeyFunc(ctx, endpoints.Name)
	createFn := func() runtime.Object {
		fakeEtcdClient.Data[key] = tools.EtcdResponseWithError{
			R: &etcd.Response{
				Node: &etcd.Node{
					Value:         runtime.EncodeOrDie(latest.Codec, endpoints),
					ModifiedIndex: 1,
				},
			},
		}
		return endpoints
	}
	gracefulSetFn := func() bool {
		if fakeEtcdClient.Data[key].R.Node == nil {
			return false
		}
		return fakeEtcdClient.Data[key].R.Node.TTL == 30
	}
	test.TestDeleteNoGraceful(createFn, gracefulSetFn)
}

func TestEtcdListEndpoints(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	key := storage.KeyRootFunc(ctx)
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Nodes: []*etcd.Node{
					{
						Value: runtime.EncodeOrDie(latest.Codec, &api.Endpoints{
							ObjectMeta: api.ObjectMeta{Name: "foo"},
							Subsets: []api.EndpointSubset{{
								Addresses: []api.EndpointAddress{{IP: "127.0.0.1"}},
								Ports:     []api.EndpointPort{{Port: 8345, Protocol: "TCP"}},
							}},
						}),
					},
					{
						Value: runtime.EncodeOrDie(latest.Codec, &api.Endpoints{
							ObjectMeta: api.ObjectMeta{Name: "bar"},
						}),
					},
				},
			},
		},
		E: nil,
	}

	endpointsObj, err := storage.List(ctx, labels.Everything(), fields.Everything())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	endpoints := endpointsObj.(*api.EndpointsList)

	if len(endpoints.Items) != 2 || endpoints.Items[0].Name != "foo" || endpoints.Items[1].Name != "bar" {
		t.Errorf("Unexpected endpoints list: %#v", endpoints)
	}
}

func TestEtcdGetEndpoints(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	endpoints := validNewEndpoints()
	name := endpoints.Name
	key, _ := storage.KeyFunc(ctx, name)
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, endpoints), 0)

	response, err := fakeClient.Get(key, false, false)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	var endpointsOut api.Endpoints
	err = latest.Codec.DecodeInto([]byte(response.Node.Value), &endpointsOut)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	obj, err := storage.Get(ctx, name)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	got := obj.(*api.Endpoints)

	endpoints.ObjectMeta.ResourceVersion = got.ObjectMeta.ResourceVersion
	if e, a := endpoints, got; !api.Semantic.DeepEqual(*e, *a) {
		t.Errorf("Unexpected endpoints: %#v, expected %#v", e, a)
	}
}

func TestListEmptyEndpointsList(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	fakeClient.ChangeIndex = 1
	key := storage.KeyRootFunc(ctx)
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{},
		E: fakeClient.NewError(tools.EtcdErrorCodeNotFound),
	}

	endpoints, err := storage.List(ctx, labels.Everything(), fields.Everything())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(endpoints.(*api.EndpointsList).Items) != 0 {
		t.Errorf("Unexpected non-zero pod list: %#v", endpoints)
	}
	if endpoints.(*api.EndpointsList).ResourceVersion != "1" {
		t.Errorf("Unexpected resource version: %#v", endpoints)
	}
}

func TestListEndpointsList(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	fakeClient.ChangeIndex = 1
	key := storage.KeyRootFunc(ctx)
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Nodes: []*etcd.Node{
					{
						Value: runtime.EncodeOrDie(latest.Codec, &api.Endpoints{
							ObjectMeta: api.ObjectMeta{Name: "foo"},
						}),
					},
					{
						Value: runtime.EncodeOrDie(latest.Codec, &api.Endpoints{
							ObjectMeta: api.ObjectMeta{Name: "bar"},
						}),
					},
				},
			},
		},
	}

	endpointsObj, err := storage.List(ctx, labels.Everything(), fields.Everything())
	endpoints := endpointsObj.(*api.EndpointsList)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(endpoints.Items) != 2 {
		t.Errorf("Unexpected endpoints list: %#v", endpoints)
	}
	if endpoints.Items[0].Name != "foo" {
		t.Errorf("Unexpected endpoints: %#v", endpoints.Items[0])
	}
	if endpoints.Items[1].Name != "bar" {
		t.Errorf("Unexpected endpoints: %#v", endpoints.Items[1])
	}
}

func TestEndpointsDecode(t *testing.T) {
	storage, _ := newStorage(t)
	expected := validNewEndpoints()
	body, err := latest.Codec.Encode(expected)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actual := storage.New()
	if err := latest.Codec.DecodeInto(body, actual); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !api.Semantic.DeepEqual(expected, actual) {
		t.Errorf("mismatch: %s", util.ObjectDiff(expected, actual))
	}
}

func TestEtcdUpdateEndpoints(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	endpoints := validChangedEndpoints()

	key, _ := storage.KeyFunc(ctx, "foo")
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, validNewEndpoints()), 0)

	_, _, err := storage.Update(ctx, endpoints)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	response, err := fakeClient.Get(key, false, false)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	var endpointsOut api.Endpoints
	err = latest.Codec.DecodeInto([]byte(response.Node.Value), &endpointsOut)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	endpoints.ObjectMeta.ResourceVersion = endpointsOut.ObjectMeta.ResourceVersion
	if !api.Semantic.DeepEqual(endpoints, &endpointsOut) {
		t.Errorf("Unexpected endpoints: %#v, expected %#v", &endpointsOut, endpoints)
	}
}

func TestDeleteEndpoints(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	endpoints := validNewEndpoints()
	name := endpoints.Name
	key, _ := storage.KeyFunc(ctx, name)
	fakeClient.ChangeIndex = 1
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Value:         runtime.EncodeOrDie(latest.Codec, endpoints),
				ModifiedIndex: 1,
				CreatedIndex:  1,
			},
		},
	}
	_, err := storage.Delete(ctx, name, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
