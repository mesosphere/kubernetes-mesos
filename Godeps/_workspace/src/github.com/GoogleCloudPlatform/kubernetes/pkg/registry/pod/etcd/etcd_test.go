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

package etcd

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	etcderrors "github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors/etcd"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/latest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/rest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/rest/resttest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/registry/pod"
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

func newStorage(t *testing.T) (*REST, *BindingREST, *StatusREST, *tools.FakeEtcdClient, tools.EtcdHelper) {
	fakeEtcdClient, h := newHelper(t)
	storage, bindingStorage, statusStorage := NewStorage(h)
	return storage, bindingStorage, statusStorage, fakeEtcdClient, h
}

func validNewPod() *api.Pod {
	return &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			Namespace: api.NamespaceDefault,
		},
		Spec: api.PodSpec{
			RestartPolicy: api.RestartPolicyAlways,
			DNSPolicy:     api.DNSClusterFirst,
			Containers: []api.Container{
				{
					Name:            "foo",
					Image:           "test",
					ImagePullPolicy: api.PullAlways,

					TerminationMessagePath: api.TerminationMessagePathDefault,
				},
			},
		},
	}
}

func validChangedPod() *api.Pod {
	pod := validNewPod()
	pod.ResourceVersion = "1"
	pod.Labels = map[string]string{
		"foo": "bar",
	}
	return pod
}

func TestStorage(t *testing.T) {
	storage, _, _, _, _ := newStorage(t)
	pod.NewRegistry(storage)
}

func TestCreate(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	storage, _, _ := NewStorage(helper)
	test := resttest.New(t, storage, fakeEtcdClient.SetError)
	pod := validNewPod()
	pod.ObjectMeta = api.ObjectMeta{}
	test.TestCreate(
		// valid
		pod,
		// invalid
		&api.Pod{
			Spec: api.PodSpec{
				Containers: []api.Container{},
			},
		},
	)
}

func TestDelete(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	storage, _, _ := NewStorage(helper)
	test := resttest.New(t, storage, fakeEtcdClient.SetError)

	createFn := func() runtime.Object {
		pod := validChangedPod()
		fakeEtcdClient.Data["/registry/pods/default/foo"] = tools.EtcdResponseWithError{
			R: &etcd.Response{
				Node: &etcd.Node{
					Value:         runtime.EncodeOrDie(latest.Codec, pod),
					ModifiedIndex: 1,
				},
			},
		}
		return pod
	}
	gracefulSetFn := func() bool {
		if fakeEtcdClient.Data["/registry/pods/default/foo"].R.Node == nil {
			return false
		}
		return fakeEtcdClient.Data["/registry/pods/default/foo"].R.Node.TTL == 30
	}
	test.TestDelete(createFn, gracefulSetFn)
}

func expectPod(t *testing.T, out runtime.Object) (*api.Pod, bool) {
	pod, ok := out.(*api.Pod)
	if !ok || pod == nil {
		t.Errorf("Expected an api.Pod object, was %#v", out)
		return nil, false
	}
	return pod, true
}

func TestCreateRegistryError(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	fakeEtcdClient.Err = fmt.Errorf("test error")
	storage, _, _ := NewStorage(helper)

	pod := validNewPod()
	_, err := storage.Create(api.NewDefaultContext(), pod)
	if err != fakeEtcdClient.Err {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateSetsFields(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	storage, _, _ := NewStorage(helper)
	pod := validNewPod()
	_, err := storage.Create(api.NewDefaultContext(), pod)
	if err != fakeEtcdClient.Err {
		t.Fatalf("unexpected error: %v", err)
	}

	actual := &api.Pod{}
	if err := helper.ExtractObj("/registry/pods/default/foo", actual, false); err != nil {
		t.Fatalf("unexpected extraction error: %v", err)
	}
	if actual.Name != pod.Name {
		t.Errorf("unexpected pod: %#v", actual)
	}
	if len(actual.UID) == 0 {
		t.Errorf("expected pod UID to be set: %#v", actual)
	}
}

func TestListError(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	fakeEtcdClient.Err = fmt.Errorf("test error")
	storage, _, _ := NewStorage(helper)
	pods, err := storage.List(api.NewDefaultContext(), labels.Everything(), fields.Everything())
	if err != fakeEtcdClient.Err {
		t.Fatalf("Expected %#v, Got %#v", fakeEtcdClient.Err, err)
	}
	if pods != nil {
		t.Errorf("Unexpected non-nil pod list: %#v", pods)
	}
}

func TestListEmptyPodList(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	fakeEtcdClient.ChangeIndex = 1
	fakeEtcdClient.Data["/registry/pods"] = tools.EtcdResponseWithError{
		R: &etcd.Response{},
		E: fakeEtcdClient.NewError(tools.EtcdErrorCodeNotFound),
	}

	storage, _, _ := NewStorage(helper)
	pods, err := storage.List(api.NewContext(), labels.Everything(), fields.Everything())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pods.(*api.PodList).Items) != 0 {
		t.Errorf("Unexpected non-zero pod list: %#v", pods)
	}
	if pods.(*api.PodList).ResourceVersion != "1" {
		t.Errorf("Unexpected resource version: %#v", pods)
	}
}

func TestListPodList(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	fakeEtcdClient.Data["/registry/pods/default"] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Nodes: []*etcd.Node{
					{
						Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
							ObjectMeta: api.ObjectMeta{Name: "foo"},
							Status:     api.PodStatus{Phase: api.PodRunning, Host: "machine"},
						}),
					},
					{
						Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
							ObjectMeta: api.ObjectMeta{Name: "bar"},
							Status:     api.PodStatus{Host: "machine"},
						}),
					},
				},
			},
		},
	}
	storage, _, _ := NewStorage(helper)

	podsObj, err := storage.List(api.NewDefaultContext(), labels.Everything(), fields.Everything())
	pods := podsObj.(*api.PodList)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pods.Items) != 2 {
		t.Errorf("Unexpected pod list: %#v", pods)
	}
	if pods.Items[0].Name != "foo" || pods.Items[0].Status.Phase != api.PodRunning || pods.Items[0].Status.Host != "machine" {
		t.Errorf("Unexpected pod: %#v", pods.Items[0])
	}
	if pods.Items[1].Name != "bar" || pods.Items[1].Status.Host != "machine" {
		t.Errorf("Unexpected pod: %#v", pods.Items[1])
	}
}

func TestListPodListSelection(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	fakeEtcdClient.Data["/registry/pods/default"] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Nodes: []*etcd.Node{
					{Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
						ObjectMeta: api.ObjectMeta{Name: "foo"},
					})},
					{Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
						ObjectMeta: api.ObjectMeta{Name: "bar"},
						Spec:       api.PodSpec{Host: "barhost"},
					})},
					{Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
						ObjectMeta: api.ObjectMeta{Name: "baz"},
						Status:     api.PodStatus{Phase: api.PodFailed},
					})},
					{Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
						ObjectMeta: api.ObjectMeta{
							Name:   "qux",
							Labels: map[string]string{"label": "qux"},
						},
					})},
					{Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
						ObjectMeta: api.ObjectMeta{Name: "zot"},
					})},
				},
			},
		},
	}
	storage, _, _ := NewStorage(helper)

	ctx := api.NewDefaultContext()

	table := []struct {
		label, field string
		expectedIDs  util.StringSet
	}{
		{
			expectedIDs: util.NewStringSet("foo", "bar", "baz", "qux", "zot"),
		}, {
			field:       "name=zot",
			expectedIDs: util.NewStringSet("zot"),
		}, {
			label:       "label=qux",
			expectedIDs: util.NewStringSet("qux"),
		}, {
			field:       "status.phase=Failed",
			expectedIDs: util.NewStringSet("baz"),
		}, {
			field:       "spec.host=barhost",
			expectedIDs: util.NewStringSet("bar"),
		}, {
			field:       "spec.host=",
			expectedIDs: util.NewStringSet("foo", "baz", "qux", "zot"),
		}, {
			field:       "spec.host!=",
			expectedIDs: util.NewStringSet("bar"),
		},
	}

	for index, item := range table {
		label, err := labels.Parse(item.label)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			continue
		}
		field, err := fields.ParseSelector(item.field)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			continue
		}
		podsObj, err := storage.List(ctx, label, field)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		pods := podsObj.(*api.PodList)

		set := util.NewStringSet()
		for i := range pods.Items {
			set.Insert(pods.Items[i].Name)
		}
		if e, a := len(item.expectedIDs), len(set); e != a {
			t.Errorf("%v: Expected %v, got %v", index, item.expectedIDs, set)
		}
		/*for _, pod := range pods.Items {
			if !item.expectedIDs.Has(pod.Name) {
				t.Errorf("%v: Unexpected pod %v", index, pod.Name)
			}
			t.Logf("%v: Got pod Name: %v", index, pod.Name)
		}*/
	}
}

func TestPodDecode(t *testing.T) {
	storage, _, _ := NewStorage(tools.EtcdHelper{})
	expected := validNewPod()
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

func TestGet(t *testing.T) {
	expect := validNewPod()
	expect.Status.Phase = api.PodRunning
	expect.Status.Host = "machine"

	fakeEtcdClient, helper := newHelper(t)
	fakeEtcdClient.Data["/registry/pods/test/foo"] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Value: runtime.EncodeOrDie(latest.Codec, expect),
			},
		},
	}
	storage, _, _ := NewStorage(helper)

	obj, err := storage.Get(api.WithNamespace(api.NewContext(), "test"), "foo")
	pod := obj.(*api.Pod)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if e, a := expect, pod; !api.Semantic.DeepEqual(e, a) {
		t.Errorf("Unexpected pod: %s", util.ObjectDiff(e, a))
	}
}

// TODO: remove, this is covered by RESTTest.TestCreate
func TestPodStorageValidatesCreate(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	fakeEtcdClient.Err = fmt.Errorf("test error")
	storage, _, _ := NewStorage(helper)

	pod := validNewPod()
	pod.Labels = map[string]string{
		"invalid/label/to/cause/validation/failure": "bar",
	}
	c, err := storage.Create(api.NewDefaultContext(), pod)
	if c != nil {
		t.Errorf("Expected nil object")
	}
	if !errors.IsInvalid(err) {
		t.Errorf("Expected to get an invalid resource error, got %v", err)
	}
}

// TODO: remove, this is covered by RESTTest.TestCreate
func TestCreatePod(t *testing.T) {
	_, helper := newHelper(t)
	storage, _, _ := NewStorage(helper)

	pod := validNewPod()
	obj, err := storage.Create(api.NewDefaultContext(), pod)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obj == nil {
		t.Fatalf("unexpected object: %#v", obj)
	}
	actual := &api.Pod{}
	if err := helper.ExtractObj("/registry/pods/default/foo", actual, false); err != nil {
		t.Fatalf("unexpected extraction error: %v", err)
	}
	if !api.HasObjectMetaSystemFieldValues(&actual.ObjectMeta) {
		t.Errorf("Expected ObjectMeta field values were populated: %#v", actual)
	}
}

// TODO: remove, this is covered by RESTTest.TestCreate
func TestCreateWithConflictingNamespace(t *testing.T) {
	_, helper := newHelper(t)
	storage, _, _ := NewStorage(helper)

	pod := validNewPod()
	pod.Namespace = "not-default"

	obj, err := storage.Create(api.NewDefaultContext(), pod)
	if obj != nil {
		t.Error("Expected a nil obj, but we got a value")
	}
	if err == nil {
		t.Errorf("Expected an error, but we didn't get one")
	} else if strings.Contains(err.Error(), "Controller.Namespace does not match the provided context") {
		t.Errorf("Expected 'Pod.Namespace does not match the provided context' error, got '%v'", err.Error())
	}
}

func TestUpdateWithConflictingNamespace(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	fakeEtcdClient.Data["/registry/pods/default/foo"] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
					ObjectMeta: api.ObjectMeta{Name: "foo", Namespace: "default"},
					Status:     api.PodStatus{Host: "machine"},
				}),
				ModifiedIndex: 1,
			},
		},
	}
	storage, _, _ := NewStorage(helper)

	pod := validChangedPod()
	pod.Namespace = "not-default"

	obj, created, err := storage.Update(api.NewDefaultContext(), pod)
	if obj != nil || created {
		t.Error("Expected a nil channel, but we got a value or created")
	}
	if err == nil {
		t.Errorf("Expected an error, but we didn't get one")
	} else if strings.Index(err.Error(), "the namespace of the provided object does not match the namespace sent on the request") == -1 {
		t.Errorf("Expected 'Pod.Namespace does not match the provided context' error, got '%v'", err.Error())
	}
}

func TestResourceLocation(t *testing.T) {
	expectedIP := "1.2.3.4"
	testCases := []struct {
		pod      api.Pod
		query    string
		location string
	}{
		{
			pod: api.Pod{
				ObjectMeta: api.ObjectMeta{Name: "foo"},
				Status:     api.PodStatus{PodIP: expectedIP},
			},
			query:    "foo",
			location: expectedIP,
		},
		{
			pod: api.Pod{
				ObjectMeta: api.ObjectMeta{Name: "foo"},
				Status:     api.PodStatus{PodIP: expectedIP},
			},
			query:    "foo:12345",
			location: expectedIP + ":12345",
		},
		{
			pod: api.Pod{
				ObjectMeta: api.ObjectMeta{Name: "foo"},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{Name: "ctr"},
					},
				},
				Status: api.PodStatus{PodIP: expectedIP},
			},
			query:    "foo",
			location: expectedIP,
		},
		{
			pod: api.Pod{
				ObjectMeta: api.ObjectMeta{Name: "foo"},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{Name: "ctr", Ports: []api.ContainerPort{{ContainerPort: 9376}}},
					},
				},
				Status: api.PodStatus{PodIP: expectedIP},
			},
			query:    "foo",
			location: expectedIP + ":9376",
		},
		{
			pod: api.Pod{
				ObjectMeta: api.ObjectMeta{Name: "foo"},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{Name: "ctr", Ports: []api.ContainerPort{{ContainerPort: 9376}}},
					},
				},
				Status: api.PodStatus{PodIP: expectedIP},
			},
			query:    "foo:12345",
			location: expectedIP + ":12345",
		},
		{
			pod: api.Pod{
				ObjectMeta: api.ObjectMeta{Name: "foo"},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{Name: "ctr1"},
						{Name: "ctr2", Ports: []api.ContainerPort{{ContainerPort: 9376}}},
					},
				},
				Status: api.PodStatus{PodIP: expectedIP},
			},
			query:    "foo",
			location: expectedIP + ":9376",
		},
		{
			pod: api.Pod{
				ObjectMeta: api.ObjectMeta{Name: "foo"},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{Name: "ctr1", Ports: []api.ContainerPort{{ContainerPort: 9376}}},
						{Name: "ctr2", Ports: []api.ContainerPort{{ContainerPort: 1234}}},
					},
				},
				Status: api.PodStatus{PodIP: expectedIP},
			},
			query:    "foo",
			location: expectedIP + ":9376",
		},
	}

	for _, tc := range testCases {
		fakeEtcdClient, helper := newHelper(t)
		fakeEtcdClient.Data["/registry/pods/default/foo"] = tools.EtcdResponseWithError{
			R: &etcd.Response{
				Node: &etcd.Node{
					Value: runtime.EncodeOrDie(latest.Codec, &tc.pod),
				},
			},
		}
		storage, _, _ := NewStorage(helper)

		redirector := rest.Redirector(storage)
		location, _, err := redirector.ResourceLocation(api.NewDefaultContext(), tc.query)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if location == nil {
			t.Errorf("Unexpected nil: %v", location)
		}

		if location.Scheme != "" {
			t.Errorf("Expected '%v', but got '%v'", "", location.Scheme)
		}
		if location.Host != tc.location {
			t.Errorf("Expected %v, but got %v", tc.location, location.Host)
		}
	}
}

func TestDeletePod(t *testing.T) {
	fakeEtcdClient, helper := newHelper(t)
	fakeEtcdClient.ChangeIndex = 1
	fakeEtcdClient.Data["/registry/pods/default/foo"] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
					ObjectMeta: api.ObjectMeta{
						Name:      "foo",
						Namespace: api.NamespaceDefault,
					},
					Status: api.PodStatus{Host: "machine"},
				}),
				ModifiedIndex: 1,
				CreatedIndex:  1,
			},
		},
	}
	storage, _, _ := NewStorage(helper)

	_, err := storage.Delete(api.NewDefaultContext(), "foo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestEtcdGetDifferentNamespace ensures same-name pods in different namespaces do not clash
func TestEtcdGetDifferentNamespace(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)

	ctx1 := api.NewDefaultContext()
	ctx2 := api.WithNamespace(api.NewContext(), "other")

	key1, _ := registry.KeyFunc(ctx1, "foo")
	key2, _ := registry.KeyFunc(ctx2, "foo")

	fakeClient.Set(key1, runtime.EncodeOrDie(latest.Codec, &api.Pod{ObjectMeta: api.ObjectMeta{Namespace: "default", Name: "foo"}}), 0)
	fakeClient.Set(key2, runtime.EncodeOrDie(latest.Codec, &api.Pod{ObjectMeta: api.ObjectMeta{Namespace: "other", Name: "foo"}}), 0)

	obj, err := registry.Get(ctx1, "foo")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	pod1 := obj.(*api.Pod)
	if pod1.Name != "foo" {
		t.Errorf("Unexpected pod: %#v", pod1)
	}
	if pod1.Namespace != "default" {
		t.Errorf("Unexpected pod: %#v", pod1)
	}

	obj, err = registry.Get(ctx2, "foo")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	pod2 := obj.(*api.Pod)
	if pod2.Name != "foo" {
		t.Errorf("Unexpected pod: %#v", pod2)
	}
	if pod2.Namespace != "other" {
		t.Errorf("Unexpected pod: %#v", pod2)
	}

}

func TestEtcdGet(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, &api.Pod{ObjectMeta: api.ObjectMeta{Name: "foo"}}), 0)
	obj, err := registry.Get(ctx, "foo")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	pod := obj.(*api.Pod)
	if pod.Name != "foo" {
		t.Errorf("Unexpected pod: %#v", pod)
	}
}

func TestEtcdGetNotFound(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: nil,
		},
		E: tools.EtcdErrorNotFound,
	}
	_, err := registry.Get(ctx, "foo")
	if !errors.IsNotFound(err) {
		t.Errorf("Unexpected error returned: %#v", err)
	}
}

func TestEtcdCreate(t *testing.T) {
	registry, bindingRegistry, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true
	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: nil,
		},
		E: tools.EtcdErrorNotFound,
	}
	_, err := registry.Create(ctx, validNewPod())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Suddenly, a wild scheduler appears:
	_, err = bindingRegistry.Create(ctx, &api.Binding{
		ObjectMeta: api.ObjectMeta{Namespace: api.NamespaceDefault, Name: "foo"},
		Target:     api.ObjectReference{Name: "machine"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, err := fakeClient.Get(key, false, false)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	var pod api.Pod
	err = latest.Codec.DecodeInto([]byte(resp.Node.Value), &pod)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if pod.Name != "foo" {
		t.Errorf("Unexpected pod: %#v %s", pod, resp.Node.Value)
	}

}

// Ensure that when scheduler creates a binding for a pod that has already been deleted
// by the API server, API server returns not-found error.
func TestEtcdCreateBindingNoPod(t *testing.T) {
	registry, bindingRegistry, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true

	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: nil,
		},
		E: tools.EtcdErrorNotFound,
	}
	// Assume that a pod has undergone the following:
	// - Create (apiserver)
	// - Schedule (scheduler)
	// - Delete (apiserver)
	_, err := bindingRegistry.Create(ctx, &api.Binding{
		ObjectMeta: api.ObjectMeta{Namespace: api.NamespaceDefault, Name: "foo"},
		Target:     api.ObjectReference{Name: "machine"},
	})
	if err == nil {
		t.Fatalf("Expected not-found-error but got nothing")
	}
	if !errors.IsNotFound(etcderrors.InterpretGetError(err, "Pod", "foo")) {
		t.Fatalf("Unexpected error returned: %#v", err)
	}

	_, err = registry.Get(ctx, "foo")
	if err == nil {
		t.Fatalf("Expected not-found-error but got nothing")
	}
	if !errors.IsNotFound(etcderrors.InterpretGetError(err, "Pod", "foo")) {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestEtcdCreateFailsWithoutNamespace(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	fakeClient.TestIndex = true
	pod := validNewPod()
	pod.Namespace = ""
	_, err := registry.Create(api.NewContext(), pod)
	// Accept "namespace" or "Namespace".
	if err == nil || !strings.Contains(err.Error(), "amespace") {
		t.Fatalf("expected error that namespace was missing from context, got: %v", err)
	}
}

func TestEtcdCreateAlreadyExisting(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{ObjectMeta: api.ObjectMeta{Name: "foo"}}),
			},
		},
		E: nil,
	}
	_, err := registry.Create(ctx, validNewPod())
	if !errors.IsAlreadyExists(err) {
		t.Errorf("Unexpected error returned: %#v", err)
	}
}

func TestEtcdCreateWithContainersNotFound(t *testing.T) {
	registry, bindingRegistry, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true
	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: nil,
		},
		E: tools.EtcdErrorNotFound,
	}
	_, err := registry.Create(ctx, validNewPod())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Suddenly, a wild scheduler appears:
	_, err = bindingRegistry.Create(ctx, &api.Binding{
		ObjectMeta: api.ObjectMeta{
			Namespace:   api.NamespaceDefault,
			Name:        "foo",
			Annotations: map[string]string{"label1": "value1"},
		},
		Target: api.ObjectReference{Name: "machine"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, err := fakeClient.Get(key, false, false)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	var pod api.Pod
	err = latest.Codec.DecodeInto([]byte(resp.Node.Value), &pod)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if pod.Name != "foo" {
		t.Errorf("Unexpected pod: %#v %s", pod, resp.Node.Value)
	}
	if !(pod.Annotations != nil && pod.Annotations["label1"] == "value1") {
		t.Fatalf("Pod annotations don't match the expected: %v", pod.Annotations)
	}
}

func TestEtcdCreateWithConflict(t *testing.T) {
	registry, bindingRegistry, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true
	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: nil,
		},
		E: tools.EtcdErrorNotFound,
	}

	_, err := registry.Create(ctx, validNewPod())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Suddenly, a wild scheduler appears:
	binding := api.Binding{
		ObjectMeta: api.ObjectMeta{
			Namespace:   api.NamespaceDefault,
			Name:        "foo",
			Annotations: map[string]string{"label1": "value1"},
		},
		Target: api.ObjectReference{Name: "machine"},
	}
	_, err = bindingRegistry.Create(ctx, &binding)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = bindingRegistry.Create(ctx, &binding)
	if err == nil || !errors.IsConflict(err) {
		t.Fatalf("expected resource conflict error, not: %v", err)
	}
}

func TestEtcdCreateWithExistingContainers(t *testing.T) {
	registry, bindingRegistry, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true
	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: nil,
		},
		E: tools.EtcdErrorNotFound,
	}
	_, err := registry.Create(ctx, validNewPod())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Suddenly, a wild scheduler appears:
	_, err = bindingRegistry.Create(ctx, &api.Binding{
		ObjectMeta: api.ObjectMeta{Namespace: api.NamespaceDefault, Name: "foo"},
		Target:     api.ObjectReference{Name: "machine"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, err := fakeClient.Get(key, false, false)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	var pod api.Pod
	err = latest.Codec.DecodeInto([]byte(resp.Node.Value), &pod)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if pod.Name != "foo" {
		t.Errorf("Unexpected pod: %#v %s", pod, resp.Node.Value)
	}
}

func TestEtcdCreateBinding(t *testing.T) {
	registry, bindingRegistry, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true

	testCases := map[string]struct {
		binding api.Binding
		errOK   func(error) bool
	}{
		"noName": {
			binding: api.Binding{
				ObjectMeta: api.ObjectMeta{Namespace: api.NamespaceDefault, Name: "foo"},
				Target:     api.ObjectReference{},
			},
			errOK: func(err error) bool { return errors.IsInvalid(err) },
		},
		"badKind": {
			binding: api.Binding{
				ObjectMeta: api.ObjectMeta{Namespace: api.NamespaceDefault, Name: "foo"},
				Target:     api.ObjectReference{Name: "machine1", Kind: "unknown"},
			},
			errOK: func(err error) bool { return errors.IsInvalid(err) },
		},
		"emptyKind": {
			binding: api.Binding{
				ObjectMeta: api.ObjectMeta{Namespace: api.NamespaceDefault, Name: "foo"},
				Target:     api.ObjectReference{Name: "machine2"},
			},
			errOK: func(err error) bool { return err == nil },
		},
		"kindNode": {
			binding: api.Binding{
				ObjectMeta: api.ObjectMeta{Namespace: api.NamespaceDefault, Name: "foo"},
				Target:     api.ObjectReference{Name: "machine3", Kind: "Node"},
			},
			errOK: func(err error) bool { return err == nil },
		},
		"kindMinion": {
			binding: api.Binding{
				ObjectMeta: api.ObjectMeta{Namespace: api.NamespaceDefault, Name: "foo"},
				Target:     api.ObjectReference{Name: "machine4", Kind: "Minion"},
			},
			errOK: func(err error) bool { return err == nil },
		},
	}
	for k, test := range testCases {
		key, _ := registry.KeyFunc(ctx, "foo")
		fakeClient.Data[key] = tools.EtcdResponseWithError{
			R: &etcd.Response{
				Node: nil,
			},
			E: tools.EtcdErrorNotFound,
		}
		if _, err := registry.Create(ctx, validNewPod()); err != nil {
			t.Fatalf("%s: unexpected error: %v", k, err)
		}
		if _, err := bindingRegistry.Create(ctx, &test.binding); !test.errOK(err) {
			t.Errorf("%s: unexpected error: %v", k, err)
		} else if err == nil {
			// If bind succeeded, verify Host field in pod's Spec.
			pod, err := registry.Get(ctx, validNewPod().ObjectMeta.Name)
			if err != nil {
				t.Errorf("%s: unexpected error: %v", k, err)
			} else if pod.(*api.Pod).Spec.Host != test.binding.Target.Name {
				t.Errorf("%s: expected: %v, got: %v", k, pod.(*api.Pod).Spec.Host, test.binding.Target.Name)
			}
		}
	}
}

func TestEtcdUpdateNotFound(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true

	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{},
		E: tools.EtcdErrorNotFound,
	}

	podIn := api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:            "foo",
			ResourceVersion: "1",
			Labels: map[string]string{
				"foo": "bar",
			},
		},
	}
	_, _, err := registry.Update(ctx, &podIn)
	if err == nil {
		t.Errorf("unexpected non-error")
	}
}

func TestEtcdUpdateNotScheduled(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true

	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, validNewPod()), 1)

	podIn := validChangedPod()
	_, _, err := registry.Update(ctx, podIn)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	response, err := fakeClient.Get(key, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	podOut := &api.Pod{}
	latest.Codec.DecodeInto([]byte(response.Node.Value), podOut)
	if !api.Semantic.DeepEqual(podOut, podIn) {
		t.Errorf("objects differ: %v", util.ObjectDiff(podOut, podIn))
	}
}

func TestEtcdUpdateScheduled(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true

	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			Namespace: api.NamespaceDefault,
		},
		Spec: api.PodSpec{
			//			Host: "machine",
			Containers: []api.Container{
				{
					Image: "foo:v1",
				},
			},
		},
		Status: api.PodStatus{
			Host: "machine",
		},
	}), 1)

	podIn := api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:            "foo",
			ResourceVersion: "1",
			Labels: map[string]string{
				"foo": "bar",
			},
		},
		Spec: api.PodSpec{
			Containers: []api.Container{
				{
					Image:                  "foo:v2",
					ImagePullPolicy:        api.PullIfNotPresent,
					TerminationMessagePath: api.TerminationMessagePathDefault,
				},
			},
			RestartPolicy: api.RestartPolicyAlways,
			DNSPolicy:     api.DNSClusterFirst,
		},
		Status: api.PodStatus{
			Host: "machine",
		},
	}
	_, _, err := registry.Update(ctx, &podIn)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	response, err := fakeClient.Get(key, false, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	var podOut api.Pod
	latest.Codec.DecodeInto([]byte(response.Node.Value), &podOut)
	if !api.Semantic.DeepEqual(podOut, podIn) {
		t.Errorf("expected: %#v, got: %#v", podOut, podIn)
	}

}

func TestEtcdUpdateStatus(t *testing.T) {
	registry, _, status, fakeClient, helper := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true

	key, _ := registry.KeyFunc(ctx, "foo")
	podStart := api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			Namespace: api.NamespaceDefault,
		},
		Spec: api.PodSpec{
			Containers: []api.Container{
				{
					Image: "foo:v1",
				},
			},
		},
		Status: api.PodStatus{
			Host: "machine",
		},
	}
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, &podStart), 1)

	podIn := api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:            "foo",
			ResourceVersion: "1",
			Labels: map[string]string{
				"foo": "bar",
			},
		},
		// should be ignored
		Spec: api.PodSpec{
			Containers: []api.Container{
				{
					Image:                  "foo:v2",
					ImagePullPolicy:        api.PullIfNotPresent,
					TerminationMessagePath: api.TerminationMessagePathDefault,
				},
			},
		},
		Status: api.PodStatus{
			Host:    "machine",
			Phase:   api.PodRunning,
			PodIP:   "127.0.0.1",
			Message: "is now scheduled",
		},
	}

	expected := podStart
	expected.ResourceVersion = "2"
	expected.Spec.RestartPolicy = api.RestartPolicyAlways
	expected.Spec.DNSPolicy = api.DNSClusterFirst
	expected.Spec.Containers[0].ImagePullPolicy = api.PullIfNotPresent
	expected.Spec.Containers[0].TerminationMessagePath = api.TerminationMessagePathDefault
	expected.Labels = podIn.Labels
	expected.Status = podIn.Status

	_, _, err := status.Update(ctx, &podIn)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	var podOut api.Pod
	if err := helper.ExtractObj(key, &podOut, false); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !api.Semantic.DeepEqual(expected, podOut) {
		t.Errorf("unexpected object: %s", util.ObjectDiff(expected, podOut))
	}
}

func TestEtcdDeletePod(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true

	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, &api.Pod{
		ObjectMeta: api.ObjectMeta{Name: "foo"},
		Status:     api.PodStatus{Host: "machine"},
	}), 0)
	_, err := registry.Delete(ctx, "foo", api.NewDeleteOptions(0))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(fakeClient.DeletedKeys) != 1 {
		t.Errorf("Expected 1 delete, found %#v", fakeClient.DeletedKeys)
	} else if fakeClient.DeletedKeys[0] != key {
		t.Errorf("Unexpected key: %s, expected %s", fakeClient.DeletedKeys[0], key)
	}
}

func TestEtcdDeletePodMultipleContainers(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	fakeClient.TestIndex = true
	key, _ := registry.KeyFunc(ctx, "foo")
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, &api.Pod{
		ObjectMeta: api.ObjectMeta{Name: "foo"},
		Status:     api.PodStatus{Host: "machine"},
	}), 0)
	_, err := registry.Delete(ctx, "foo", api.NewDeleteOptions(0))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(fakeClient.DeletedKeys) != 1 {
		t.Errorf("Expected 1 delete, found %#v", fakeClient.DeletedKeys)
	}
	if fakeClient.DeletedKeys[0] != key {
		t.Errorf("Unexpected key: %s, expected %s", fakeClient.DeletedKeys[0], key)
	}
}

func TestEtcdEmptyList(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	key := registry.KeyRootFunc(ctx)
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Nodes: []*etcd.Node{},
			},
		},
		E: nil,
	}

	obj, err := registry.List(ctx, labels.Everything(), fields.Everything())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	pods := obj.(*api.PodList)
	if len(pods.Items) != 0 {
		t.Errorf("Unexpected pod list: %#v", pods)
	}
}

func TestEtcdListNotFound(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	key := registry.KeyRootFunc(ctx)
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{},
		E: tools.EtcdErrorNotFound,
	}
	obj, err := registry.List(ctx, labels.Everything(), fields.Everything())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	pods := obj.(*api.PodList)
	if len(pods.Items) != 0 {
		t.Errorf("Unexpected pod list: %#v", pods)
	}
}

func TestEtcdList(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	key := registry.KeyRootFunc(ctx)
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Nodes: []*etcd.Node{
					{
						Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
							ObjectMeta: api.ObjectMeta{Name: "foo"},
							Status:     api.PodStatus{Host: "machine"},
						}),
					},
					{
						Value: runtime.EncodeOrDie(latest.Codec, &api.Pod{
							ObjectMeta: api.ObjectMeta{Name: "bar"},
							Status:     api.PodStatus{Host: "machine"},
						}),
					},
				},
			},
		},
		E: nil,
	}
	obj, err := registry.List(ctx, labels.Everything(), fields.Everything())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	pods := obj.(*api.PodList)

	if len(pods.Items) != 2 || pods.Items[0].Name != "foo" || pods.Items[1].Name != "bar" {
		t.Errorf("Unexpected pod list: %#v", pods)
	}
	if pods.Items[0].Status.Host != "machine" ||
		pods.Items[1].Status.Host != "machine" {
		t.Errorf("Failed to populate host name.")
	}
}

func TestEtcdWatchPods(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	watching, err := registry.Watch(ctx,
		labels.Everything(),
		fields.Everything(),
		"1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fakeClient.WaitForWatchCompletion()

	select {
	case _, ok := <-watching.ResultChan():
		if !ok {
			t.Errorf("watching channel should be open")
		}
	default:
	}
	fakeClient.WatchInjectError <- nil
	if _, ok := <-watching.ResultChan(); ok {
		t.Errorf("watching channel should be closed")
	}
	watching.Stop()
}

func TestEtcdWatchPodsMatch(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	watching, err := registry.Watch(ctx,
		labels.SelectorFromSet(labels.Set{"name": "foo"}),
		fields.Everything(),
		"1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fakeClient.WaitForWatchCompletion()

	pod := &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name: "foo",
			Labels: map[string]string{
				"name": "foo",
			},
		},
	}
	podBytes, _ := latest.Codec.Encode(pod)
	fakeClient.WatchResponse <- &etcd.Response{
		Action: "create",
		Node: &etcd.Node{
			Value: string(podBytes),
		},
	}
	select {
	case _, ok := <-watching.ResultChan():
		if !ok {
			t.Errorf("watching channel should be open")
		}
	case <-time.After(time.Millisecond * 100):
		t.Error("unexpected timeout from result channel")
	}
	watching.Stop()
}

func TestEtcdWatchPodsNotMatch(t *testing.T) {
	registry, _, _, fakeClient, _ := newStorage(t)
	ctx := api.NewDefaultContext()
	watching, err := registry.Watch(ctx,
		labels.SelectorFromSet(labels.Set{"name": "foo"}),
		fields.Everything(),
		"1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fakeClient.WaitForWatchCompletion()

	pod := &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name: "bar",
			Labels: map[string]string{
				"name": "bar",
			},
		},
	}
	podBytes, _ := latest.Codec.Encode(pod)
	fakeClient.WatchResponse <- &etcd.Response{
		Action: "create",
		Node: &etcd.Node{
			Value: string(podBytes),
		},
	}

	select {
	case <-watching.ResultChan():
		t.Error("unexpected result from result channel")
	case <-time.After(time.Millisecond * 100):
		// expected case
	}
}
