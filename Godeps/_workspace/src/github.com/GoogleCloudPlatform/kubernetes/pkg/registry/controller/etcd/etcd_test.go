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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/latest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/rest/resttest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	etcdgeneric "github.com/GoogleCloudPlatform/kubernetes/pkg/registry/generic/etcd"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/coreos/go-etcd/etcd"
)

const (
	PASS = iota
	FAIL
)

// newStorage creates a REST storage backed by etcd helpers
func newStorage(t *testing.T) (*REST, *tools.FakeEtcdClient) {
	fakeEtcdClient := tools.NewFakeEtcdClient(t)
	fakeEtcdClient.TestIndex = true
	h := tools.NewEtcdHelper(fakeEtcdClient, latest.Codec)
	storage := NewREST(h)
	return storage, fakeEtcdClient
}

// createController is a helper function that returns a controller with the updated resource version.
func createController(storage *REST, rc api.ReplicationController, t *testing.T) (api.ReplicationController, error) {
	ctx := api.WithNamespace(api.NewContext(), rc.Namespace)
	obj, err := storage.Create(ctx, &rc)
	if err != nil {
		t.Errorf("Failed to create controller, %v", err)
	}
	newRc := obj.(*api.ReplicationController)
	return *newRc, nil
}

var validPodTemplate = api.PodTemplate{
	Spec: api.PodTemplateSpec{
		ObjectMeta: api.ObjectMeta{
			Labels: map[string]string{"a": "b"},
		},
		Spec: api.PodSpec{
			Containers: []api.Container{
				{
					Name:            "test",
					Image:           "test_image",
					ImagePullPolicy: api.PullIfNotPresent,
				},
			},
			RestartPolicy: api.RestartPolicyAlways,
			DNSPolicy:     api.DNSClusterFirst,
		},
	},
}

var validControllerSpec = api.ReplicationControllerSpec{
	Selector: validPodTemplate.Spec.Labels,
	Template: &validPodTemplate.Spec,
}

var validController = api.ReplicationController{
	ObjectMeta: api.ObjectMeta{Name: "foo", Namespace: "default"},
	Spec:       validControllerSpec,
}

// makeControllerKey constructs etcd paths to controller items enforcing namespace rules.
func makeControllerKey(ctx api.Context, id string) (string, error) {
	return etcdgeneric.NamespaceKeyFunc(ctx, controllerPrefix, id)
}

// makeControllerListKey constructs etcd paths to the root of the resource,
// not a specific controller resource
func makeControllerListKey(ctx api.Context) string {
	return etcdgeneric.NamespaceKeyRootFunc(ctx, controllerPrefix)
}

func TestEtcdCreateController(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	_, err := storage.Create(ctx, &validController)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	key, _ := makeControllerKey(ctx, validController.Name)
	resp, err := fakeClient.Get(key, false, false)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}
	var ctrl api.ReplicationController
	err = latest.Codec.DecodeInto([]byte(resp.Node.Value), &ctrl)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if ctrl.Name != "foo" {
		t.Errorf("Unexpected controller: %#v %s", ctrl, resp.Node.Value)
	}
}

func TestEtcdCreateControllerAlreadyExisting(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	key, _ := makeControllerKey(ctx, validController.Name)
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, &validController), 0)

	_, err := storage.Create(ctx, &validController)
	if !errors.IsAlreadyExists(err) {
		t.Errorf("expected already exists err, got %#v", err)
	}
}

func TestEtcdCreateControllerValidates(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, _ := newStorage(t)
	emptyName := validController
	emptyName.Name = ""
	emptySelector := validController
	emptySelector.Spec.Selector = map[string]string{}
	failureCases := []api.ReplicationController{emptyName, emptySelector}
	for _, failureCase := range failureCases {
		c, err := storage.Create(ctx, &failureCase)
		if c != nil {
			t.Errorf("Expected nil channel")
		}
		if !errors.IsInvalid(err) {
			t.Errorf("Expected to get an invalid resource error, got %v", err)
		}
	}
}

func TestCreateControllerWithGeneratedName(t *testing.T) {
	storage, _ := newStorage(t)
	controller := &api.ReplicationController{
		ObjectMeta: api.ObjectMeta{
			Namespace:    api.NamespaceDefault,
			GenerateName: "rc-",
		},
		Spec: api.ReplicationControllerSpec{
			Replicas: 2,
			Selector: map[string]string{"a": "b"},
			Template: &validPodTemplate.Spec,
		},
	}

	ctx := api.NewDefaultContext()
	_, err := storage.Create(ctx, controller)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if controller.Name == "rc-" || !strings.HasPrefix(controller.Name, "rc-") {
		t.Errorf("unexpected name: %#v", controller)
	}
}

func TestCreateControllerWithConflictingNamespace(t *testing.T) {
	storage, _ := newStorage(t)
	controller := &api.ReplicationController{
		ObjectMeta: api.ObjectMeta{Name: "test", Namespace: "not-default"},
	}

	ctx := api.NewDefaultContext()
	channel, err := storage.Create(ctx, controller)
	if channel != nil {
		t.Error("Expected a nil channel, but we got a value")
	}
	errSubString := "namespace"
	if err == nil {
		t.Errorf("Expected an error, but we didn't get one")
	} else if !errors.IsBadRequest(err) ||
		strings.Index(err.Error(), errSubString) == -1 {
		t.Errorf("Expected a Bad Request error with the sub string '%s', got %v", errSubString, err)
	}
}

func TestEtcdGetController(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	key, _ := makeControllerKey(ctx, validController.Name)
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, &validController), 0)
	ctrl, err := storage.Get(ctx, validController.Name)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	controller, ok := ctrl.(*api.ReplicationController)
	if !ok {
		t.Errorf("Expected a controller, got %#v", ctrl)
	}
	if controller.Name != validController.Name {
		t.Errorf("Unexpected controller: %#v", controller)
	}
}

func TestEtcdControllerValidatesUpdate(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, _ := newStorage(t)

	updateController, err := createController(storage, validController, t)
	if err != nil {
		t.Errorf("Failed to create controller, cannot proceed with test.")
	}

	updaters := []func(rc api.ReplicationController) (runtime.Object, bool, error){
		func(rc api.ReplicationController) (runtime.Object, bool, error) {
			rc.UID = "newUID"
			return storage.Update(ctx, &rc)
		},
		func(rc api.ReplicationController) (runtime.Object, bool, error) {
			rc.Name = ""
			return storage.Update(ctx, &rc)
		},
		func(rc api.ReplicationController) (runtime.Object, bool, error) {
			rc.Spec.Selector = map[string]string{}
			return storage.Update(ctx, &rc)
		},
	}
	for _, u := range updaters {
		c, updated, err := u(updateController)
		if c != nil || updated {
			t.Errorf("Expected nil object and not created")
		}
		if !errors.IsInvalid(err) && !errors.IsBadRequest(err) {
			t.Errorf("Expected invalid or bad request error, got %v of type %T", err, err)
		}
	}
}

func TestEtcdControllerValidatesNamespaceOnUpdate(t *testing.T) {
	storage, _ := newStorage(t)
	ns := "newnamespace"

	// The update should fail if the namespace on the controller is set to something
	// other than the namespace on the given context, even if the namespace on the
	// controller is valid.
	updateController, err := createController(storage, validController, t)

	newNamespaceController := validController
	newNamespaceController.Namespace = ns
	_, err = createController(storage, newNamespaceController, t)

	c, updated, err := storage.Update(api.WithNamespace(api.NewContext(), ns), &updateController)
	if c != nil || updated {
		t.Errorf("Expected nil object and not created")
	}
	// TODO: Be more paranoid about the type of error and make sure it has the substring
	// "namespace" in it, once #5684 is fixed. Ideally this would be a NewBadRequest.
	if err == nil {
		t.Errorf("Expected an error, but we didn't get one")
	}
}

// TestEtcdGetControllerDifferentNamespace ensures same-name controllers in different namespaces do not clash
func TestEtcdGetControllerDifferentNamespace(t *testing.T) {
	storage, fakeClient := newStorage(t)

	otherNs := "other"
	ctx1 := api.NewDefaultContext()
	ctx2 := api.WithNamespace(api.NewContext(), otherNs)

	key1, _ := makeControllerKey(ctx1, validController.Name)
	key2, _ := makeControllerKey(ctx2, validController.Name)

	fakeClient.Set(key1, runtime.EncodeOrDie(latest.Codec, &validController), 0)
	otherNsController := validController
	otherNsController.Namespace = otherNs
	fakeClient.Set(key2, runtime.EncodeOrDie(latest.Codec, &otherNsController), 0)

	obj, err := storage.Get(ctx1, validController.Name)
	ctrl1, _ := obj.(*api.ReplicationController)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if ctrl1.Name != "foo" {
		t.Errorf("Unexpected controller: %#v", ctrl1)
	}
	if ctrl1.Namespace != "default" {
		t.Errorf("Unexpected controller: %#v", ctrl1)
	}

	obj, err = storage.Get(ctx2, validController.Name)
	ctrl2, _ := obj.(*api.ReplicationController)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if ctrl2.Name != "foo" {
		t.Errorf("Unexpected controller: %#v", ctrl2)
	}
	if ctrl2.Namespace != "other" {
		t.Errorf("Unexpected controller: %#v", ctrl2)
	}

}

func TestEtcdGetControllerNotFound(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	key, _ := makeControllerKey(ctx, validController.Name)
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: nil,
		},
		E: tools.EtcdErrorNotFound,
	}
	ctrl, err := storage.Get(ctx, validController.Name)
	if ctrl != nil {
		t.Errorf("Unexpected non-nil controller: %#v", ctrl)
	}
	if !errors.IsNotFound(err) {
		t.Errorf("Unexpected error returned: %#v", err)
	}
}

func TestEtcdUpdateController(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	key, _ := makeControllerKey(ctx, validController.Name)

	// set a key, then retrieve the current resource version and try updating it
	resp, _ := fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, &validController), 0)
	update := validController
	update.ResourceVersion = strconv.FormatUint(resp.Node.ModifiedIndex, 10)
	update.Spec.Replicas = validController.Spec.Replicas + 1
	_, created, err := storage.Update(ctx, &update)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if created {
		t.Errorf("expected an update but created flag was returned")
	}
	ctrl, err := storage.Get(ctx, validController.Name)
	updatedController, _ := ctrl.(*api.ReplicationController)
	if updatedController.Spec.Replicas != validController.Spec.Replicas+1 {
		t.Errorf("Unexpected controller: %#v", ctrl)
	}
}

func TestEtcdDeleteController(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	key, _ := makeControllerKey(ctx, validController.Name)
	fakeClient.Set(key, runtime.EncodeOrDie(latest.Codec, &validController), 0)
	obj, err := storage.Delete(ctx, validController.Name, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if status, ok := obj.(*api.Status); !ok {
		t.Errorf("Expected status of delete, got %#v", status)
	} else if status.Status != api.StatusSuccess {
		t.Errorf("Expected success, got %#v", status.Status)
	}
	if len(fakeClient.DeletedKeys) != 1 {
		t.Errorf("Expected 1 delete, found %#v", fakeClient.DeletedKeys)
	}
	if fakeClient.DeletedKeys[0] != key {
		t.Errorf("Unexpected key: %s, expected %s", fakeClient.DeletedKeys[0], key)
	}
}

func TestEtcdListControllers(t *testing.T) {
	storage, fakeClient := newStorage(t)
	ctx := api.NewDefaultContext()
	key := makeControllerListKey(ctx)
	controller := validController
	controller.Name = "bar"
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Nodes: []*etcd.Node{
					{
						Value: runtime.EncodeOrDie(latest.Codec, &validController),
					},
					{
						Value: runtime.EncodeOrDie(latest.Codec, &controller),
					},
				},
			},
		},
		E: nil,
	}
	objList, err := storage.List(ctx, labels.Everything(), fields.Everything())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	controllers, _ := objList.(*api.ReplicationControllerList)
	if len(controllers.Items) != 2 || controllers.Items[0].Name != validController.Name || controllers.Items[1].Name != controller.Name {
		t.Errorf("Unexpected controller list: %#v", controllers)
	}
}

func TestEtcdListControllersNotFound(t *testing.T) {
	storage, fakeClient := newStorage(t)
	ctx := api.NewDefaultContext()
	key := makeControllerListKey(ctx)
	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{},
		E: tools.EtcdErrorNotFound,
	}
	objList, err := storage.List(ctx, labels.Everything(), fields.Everything())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	controllers, _ := objList.(*api.ReplicationControllerList)
	if len(controllers.Items) != 0 {
		t.Errorf("Unexpected controller list: %#v", controllers)
	}
}

func TestEtcdListControllersLabelsMatch(t *testing.T) {
	storage, fakeClient := newStorage(t)
	ctx := api.NewDefaultContext()
	key := makeControllerListKey(ctx)

	controller := validController
	controller.Labels = map[string]string{"k": "v"}
	controller.Name = "bar"

	fakeClient.Data[key] = tools.EtcdResponseWithError{
		R: &etcd.Response{
			Node: &etcd.Node{
				Nodes: []*etcd.Node{
					{
						Value: runtime.EncodeOrDie(latest.Codec, &validController),
					},
					{
						Value: runtime.EncodeOrDie(latest.Codec, &controller),
					},
				},
			},
		},
		E: nil,
	}
	testLabels := labels.SelectorFromSet(labels.Set(controller.Labels))
	objList, err := storage.List(ctx, testLabels, fields.Everything())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	controllers, _ := objList.(*api.ReplicationControllerList)
	if len(controllers.Items) != 1 || controllers.Items[0].Name != controller.Name ||
		!testLabels.Matches(labels.Set(controllers.Items[0].Labels)) {
		t.Errorf("Unexpected controller list: %#v for query with labels %#v",
			controllers, testLabels)
	}
}

func TestEtcdWatchController(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	watching, err := storage.Watch(ctx,
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

func TestEtcdWatchControllersMatch(t *testing.T) {
	ctx := api.WithNamespace(api.NewDefaultContext(), validController.Namespace)
	storage, fakeClient := newStorage(t)
	fakeClient.ExpectNotFoundGet(etcdgeneric.NamespaceKeyRootFunc(ctx, "/registry/pods"))

	watching, err := storage.Watch(ctx,
		labels.SelectorFromSet(validController.Spec.Selector),
		fields.Everything(),
		"1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fakeClient.WaitForWatchCompletion()

	// The watcher above is waiting for these Labels, on receiving them it should
	// apply the ControllerStatus decorator, which lists pods, causing a query against
	// the /registry/pods endpoint of the etcd client.
	controller := &api.ReplicationController{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			Labels:    validController.Spec.Selector,
			Namespace: "default",
		},
	}
	controllerBytes, _ := latest.Codec.Encode(controller)
	fakeClient.WatchResponse <- &etcd.Response{
		Action: "create",
		Node: &etcd.Node{
			Value: string(controllerBytes),
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

func TestEtcdWatchControllersFields(t *testing.T) {
	ctx := api.WithNamespace(api.NewDefaultContext(), validController.Namespace)
	storage, fakeClient := newStorage(t)
	fakeClient.ExpectNotFoundGet(etcdgeneric.NamespaceKeyRootFunc(ctx, "/registry/pods"))

	testFieldMap := map[int][]fields.Set{
		PASS: {
			{"status.replicas": "0"},
			{"name": "foo"},
			{"status.replicas": "0", "name": "foo"},
		},
		FAIL: {
			{"status.replicas": "10"},
			{"name": "bar"},
			{"status.replicas": "10", "name": "foo"},
			{"status.replicas": "0", "name": "bar"},
		},
	}
	testEtcdActions := []string{
		tools.EtcdCreate,
		tools.EtcdSet,
		tools.EtcdCAS,
		tools.EtcdDelete}

	controller := &api.ReplicationController{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			Labels:    validController.Spec.Selector,
			Namespace: "default",
		},
		Status: api.ReplicationControllerStatus{
			Replicas: 0,
		},
	}
	controllerBytes, _ := latest.Codec.Encode(controller)

	for expectedResult, fieldSet := range testFieldMap {
		for _, field := range fieldSet {
			for _, action := range testEtcdActions {
				watching, err := storage.Watch(ctx,
					labels.Everything(),
					field.AsSelector(),
					"1",
				)
				var prevNode *etcd.Node = nil
				node := &etcd.Node{
					Value: string(controllerBytes),
				}
				if action == tools.EtcdDelete {
					prevNode = node
				}
				fakeClient.WaitForWatchCompletion()
				fakeClient.WatchResponse <- &etcd.Response{
					Action:   action,
					Node:     node,
					PrevNode: prevNode,
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				select {
				case r, ok := <-watching.ResultChan():
					if expectedResult == FAIL {
						t.Errorf("Unexpected result from channel %#v", r)
					}
					if !ok {
						t.Errorf("watching channel should be open")
					}
				case <-time.After(time.Millisecond * 100):
					if expectedResult == PASS {
						t.Error("unexpected timeout from result channel")
					}
				}
				watching.Stop()
			}
		}
	}
}

func TestEtcdWatchControllersNotMatch(t *testing.T) {
	ctx := api.NewDefaultContext()
	storage, fakeClient := newStorage(t)
	fakeClient.ExpectNotFoundGet(etcdgeneric.NamespaceKeyRootFunc(ctx, "/registry/pods"))

	watching, err := storage.Watch(ctx,
		labels.SelectorFromSet(labels.Set{"name": "foo"}),
		fields.Everything(),
		"1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fakeClient.WaitForWatchCompletion()

	controller := &api.ReplicationController{
		ObjectMeta: api.ObjectMeta{
			Name: "bar",
			Labels: map[string]string{
				"name": "bar",
			},
		},
	}
	controllerBytes, _ := latest.Codec.Encode(controller)
	fakeClient.WatchResponse <- &etcd.Response{
		Action: "create",
		Node: &etcd.Node{
			Value: string(controllerBytes),
		},
	}

	select {
	case <-watching.ResultChan():
		t.Error("unexpected result from result channel")
	case <-time.After(time.Millisecond * 100):
		// expected case
	}
}

func TestCreate(t *testing.T) {
	storage, fakeClient := newStorage(t)
	test := resttest.New(t, storage, fakeClient.SetError)
	test.TestCreate(
		// valid
		&api.ReplicationController{
			Spec: api.ReplicationControllerSpec{
				Replicas: 2,
				Selector: map[string]string{"a": "b"},
				Template: &validPodTemplate.Spec,
			},
		},
		// invalid
		&api.ReplicationController{
			Spec: api.ReplicationControllerSpec{
				Replicas: 2,
				Selector: map[string]string{},
				Template: &validPodTemplate.Spec,
			},
		},
	)
}

func TestDelete(t *testing.T) {
	storage, fakeClient := newStorage(t)
	test := resttest.New(t, storage, fakeClient.SetError)

	createFn := func() runtime.Object {
		rc := validController
		rc.ResourceVersion = "1"
		fakeClient.Data["/registry/controllers/default/foo"] = tools.EtcdResponseWithError{
			R: &etcd.Response{
				Node: &etcd.Node{
					Value:         runtime.EncodeOrDie(latest.Codec, &rc),
					ModifiedIndex: 1,
				},
			},
		}
		return &rc
	}
	gracefulSetFn := func() bool {
		// If the controller is still around after trying to delete either the delete
		// failed, or we're deleting it gracefully.
		if fakeClient.Data["/registry/controllers/default/foo"].R.Node != nil {
			return true
		}
		return false
	}

	test.TestDelete(createFn, gracefulSetFn)
}
