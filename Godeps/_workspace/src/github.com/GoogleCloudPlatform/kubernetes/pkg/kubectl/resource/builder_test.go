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

package resource

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/ghodss/yaml"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/latest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/meta"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/resource"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
	watchjson "github.com/GoogleCloudPlatform/kubernetes/pkg/watch/json"
)

func stringBody(body string) io.ReadCloser {
	return ioutil.NopCloser(bytes.NewReader([]byte(body)))
}

func watchBody(events ...watch.Event) string {
	buf := &bytes.Buffer{}
	enc := watchjson.NewEncoder(buf, latest.Codec)
	for _, e := range events {
		enc.Encode(&e)
	}
	return buf.String()
}

func fakeClient() ClientMapper {
	return ClientMapperFunc(func(*meta.RESTMapping) (RESTClient, error) {
		return &client.FakeRESTClient{}, nil
	})
}

func fakeClientWith(t *testing.T, data map[string]string) ClientMapper {
	return ClientMapperFunc(func(*meta.RESTMapping) (RESTClient, error) {
		return &client.FakeRESTClient{
			Codec: latest.Codec,
			Client: client.HTTPClientFunc(func(req *http.Request) (*http.Response, error) {
				p := req.URL.Path
				q := req.URL.RawQuery
				if len(q) != 0 {
					p = p + "?" + q
				}
				body, ok := data[p]
				if !ok {
					t.Fatalf("unexpected request: %s (%s)\n%#v", p, req.URL, req)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       stringBody(body),
				}, nil
			}),
		}, nil
	})
}

func testData() (*api.PodList, *api.ServiceList) {
	pods := &api.PodList{
		ListMeta: api.ListMeta{
			ResourceVersion: "15",
		},
		Items: []api.Pod{
			{
				ObjectMeta: api.ObjectMeta{Name: "foo", Namespace: "test", ResourceVersion: "10"},
				Spec: api.PodSpec{
					RestartPolicy: api.RestartPolicyAlways,
					DNSPolicy:     api.DNSClusterFirst,
				},
			},
			{
				ObjectMeta: api.ObjectMeta{Name: "bar", Namespace: "test", ResourceVersion: "11"},
				Spec: api.PodSpec{
					RestartPolicy: api.RestartPolicyAlways,
					DNSPolicy:     api.DNSClusterFirst,
				},
			},
		},
	}
	svc := &api.ServiceList{
		ListMeta: api.ListMeta{
			ResourceVersion: "16",
		},
		Items: []api.Service{
			{
				ObjectMeta: api.ObjectMeta{Name: "baz", Namespace: "test", ResourceVersion: "12"},
			},
		},
	}
	return pods, svc
}

func streamTestData() (io.Reader, *api.PodList, *api.ServiceList) {
	pods, svc := testData()
	r, w := io.Pipe()
	go func() {
		defer w.Close()
		w.Write([]byte(runtime.EncodeOrDie(latest.Codec, pods)))
		w.Write([]byte(runtime.EncodeOrDie(latest.Codec, svc)))
	}()
	return r, pods, svc
}

func JSONToYAMLOrDie(in []byte) []byte {
	data, err := yaml.JSONToYAML(in)
	if err != nil {
		panic(err)
	}
	return data
}

func streamYAMLTestData() (io.Reader, *api.PodList, *api.ServiceList) {
	pods, svc := testData()
	r, w := io.Pipe()
	go func() {
		defer w.Close()
		w.Write(JSONToYAMLOrDie([]byte(runtime.EncodeOrDie(latest.Codec, pods))))
		w.Write([]byte("\n---\n"))
		w.Write(JSONToYAMLOrDie([]byte(runtime.EncodeOrDie(latest.Codec, svc))))
	}()
	return r, pods, svc
}

type testVisitor struct {
	InjectErr error
	Infos     []*Info
}

func (v *testVisitor) Handle(info *Info) error {
	v.Infos = append(v.Infos, info)
	return v.InjectErr
}

func (v *testVisitor) Objects() []runtime.Object {
	objects := []runtime.Object{}
	for i := range v.Infos {
		objects = append(objects, v.Infos[i].Object)
	}
	return objects
}

func TestPathBuilder(t *testing.T) {
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		FilenameParam("../../../examples/guestbook/redis-master-controller.json")

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || !singular || len(test.Infos) != 1 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}

	info := test.Infos[0]
	if info.Name != "redis-master-controller" || info.Namespace != "" || info.Object == nil {
		t.Errorf("unexpected info: %#v", info)
	}
}

func TestNodeBuilder(t *testing.T) {
	node := &api.Node{
		ObjectMeta: api.ObjectMeta{Name: "node1", Namespace: "should-not-have", ResourceVersion: "10"},
		Spec:       api.NodeSpec{},
		Status: api.NodeStatus{
			Capacity: api.ResourceList{
				api.ResourceCPU:    resource.MustParse("1000m"),
				api.ResourceMemory: resource.MustParse("1Mi"),
			},
		},
	}
	r, w := io.Pipe()
	go func() {
		defer w.Close()
		w.Write([]byte(runtime.EncodeOrDie(latest.Codec, node)))
	}()

	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		NamespaceParam("test").Stream(r, "STDIN")

	test := &testVisitor{}

	err := b.Do().Visit(test.Handle)
	if err != nil || len(test.Infos) != 1 {
		t.Fatalf("unexpected response: %v %#v", err, test.Infos)
	}
	info := test.Infos[0]
	if info.Name != "node1" || info.Namespace != "" || info.Object == nil {
		t.Errorf("unexpected info: %#v", info)
	}
}

func TestPathBuilderWithMultiple(t *testing.T) {
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		FilenameParam("../../../examples/guestbook/redis-master-controller.json").
		FilenameParam("../../../examples/guestbook/redis-master-controller.json").
		NamespaceParam("test").DefaultNamespace()

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || singular || len(test.Infos) != 2 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}

	info := test.Infos[1]
	if info.Name != "redis-master-controller" || info.Namespace != "test" || info.Object == nil {
		t.Errorf("unexpected info: %#v", info)
	}
}

func TestDirectoryBuilder(t *testing.T) {
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		FilenameParam("../../../examples/guestbook").
		NamespaceParam("test").DefaultNamespace()

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || singular || len(test.Infos) < 4 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}

	found := false
	for _, info := range test.Infos {
		if info.Name == "redis-master-controller" && info.Namespace == "test" && info.Object != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("unexpected responses: %#v", test.Infos)
	}
}

func TestURLBuilder(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(runtime.EncodeOrDie(latest.Codec, &api.Pod{ObjectMeta: api.ObjectMeta{Namespace: "foo", Name: "test"}})))
	}))
	defer s.Close()

	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		FilenameParam(s.URL).
		NamespaceParam("test")

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || !singular || len(test.Infos) != 1 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}
	info := test.Infos[0]
	if info.Name != "test" || info.Namespace != "foo" || info.Object == nil {
		t.Errorf("unexpected info: %#v", info)
	}
}

func TestURLBuilderRequireNamespace(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(runtime.EncodeOrDie(latest.Codec, &api.Pod{ObjectMeta: api.ObjectMeta{Namespace: "foo", Name: "test"}})))
	}))
	defer s.Close()

	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		FilenameParam(s.URL).
		NamespaceParam("test").RequireNamespace()

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err == nil || !singular || len(test.Infos) != 0 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}
}

func TestResourceByName(t *testing.T) {
	pods, _ := testData()
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClientWith(t, map[string]string{
		"/namespaces/test/pods/foo": runtime.EncodeOrDie(latest.Codec, &pods.Items[0]),
	})).
		NamespaceParam("test")

	test := &testVisitor{}
	singular := false

	if b.Do().Err() == nil {
		t.Errorf("unexpected non-error")
	}

	b.ResourceTypeOrNameArgs(true, "pods", "foo")

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || !singular || len(test.Infos) != 1 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}
	if !reflect.DeepEqual(&pods.Items[0], test.Objects()[0]) {
		t.Errorf("unexpected object: %#v", test.Objects())
	}

	mapping, err := b.Do().ResourceMapping()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mapping.Resource != "pods" {
		t.Errorf("unexpected resource mapping: %#v", mapping)
	}
}

func TestResourceByNameAndEmptySelector(t *testing.T) {
	pods, _ := testData()
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClientWith(t, map[string]string{
		"/namespaces/test/pods/foo": runtime.EncodeOrDie(latest.Codec, &pods.Items[0]),
	})).
		NamespaceParam("test").
		SelectorParam("").
		ResourceTypeOrNameArgs(true, "pods", "foo")

	singular := false
	infos, err := b.Do().IntoSingular(&singular).Infos()
	if err != nil || !singular || len(infos) != 1 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, infos)
	}
	if !reflect.DeepEqual(&pods.Items[0], infos[0].Object) {
		t.Errorf("unexpected object: %#v", infos[0])
	}

	mapping, err := b.Do().ResourceMapping()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mapping.Resource != "pods" {
		t.Errorf("unexpected resource mapping: %#v", mapping)
	}
}

func TestSelector(t *testing.T) {
	pods, svc := testData()
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClientWith(t, map[string]string{
		"/namespaces/test/pods?labels=a%3Db":     runtime.EncodeOrDie(latest.Codec, pods),
		"/namespaces/test/services?labels=a%3Db": runtime.EncodeOrDie(latest.Codec, svc),
	})).
		SelectorParam("a=b").
		NamespaceParam("test").
		Flatten()

	test := &testVisitor{}
	singular := false

	if b.Do().Err() == nil {
		t.Errorf("unexpected non-error")
	}

	b.ResourceTypeOrNameArgs(true, "pods,service")

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || singular || len(test.Infos) != 3 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}
	if !api.Semantic.DeepDerivative([]runtime.Object{&pods.Items[0], &pods.Items[1], &svc.Items[0]}, test.Objects()) {
		t.Errorf("unexpected visited objects: %#v", test.Objects())
	}

	if _, err := b.Do().ResourceMapping(); err == nil {
		t.Errorf("unexpected non-error")
	}
}

func TestSelectorRequiresKnownTypes(t *testing.T) {
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		SelectorParam("a=b").
		NamespaceParam("test").
		ResourceTypes("unknown")

	if b.Do().Err() == nil {
		t.Errorf("unexpected non-error")
	}
}

func TestSingleResourceType(t *testing.T) {
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		SelectorParam("a=b").
		SingleResourceType().
		ResourceTypeOrNameArgs(true, "pods,services")

	if b.Do().Err() == nil {
		t.Errorf("unexpected non-error")
	}
}

func TestResourceTuple(t *testing.T) {
	expectNoErr := func(err error) bool { return err == nil }
	expectErr := func(err error) bool { return err != nil }
	testCases := map[string]struct {
		args  []string
		errFn func(error) bool
	}{
		"valid": {
			args:  []string{"pods/foo"},
			errFn: expectNoErr,
		},
		"valid multiple with name indirection": {
			args:  []string{"pods/foo", "pod/bar"},
			errFn: expectNoErr,
		},
		"valid multiple with namespaced and non-namespaced types": {
			args:  []string{"minions/foo", "pod/bar"},
			errFn: expectNoErr,
		},
		"mixed arg types": {
			args:  []string{"pods/foo", "bar"},
			errFn: expectErr,
		},
		/*"missing resource": {
			args:  []string{"pods/foo2"},
			errFn: expectNoErr, // not an error because resources are lazily visited
		},*/
		"comma in resource": {
			args:  []string{",pods/foo"},
			errFn: expectErr,
		},
		"multiple types in resource": {
			args:  []string{"pods,services/foo"},
			errFn: expectErr,
		},
		"unknown resource type": {
			args:  []string{"unknown/foo"},
			errFn: expectErr,
		},
		"leading slash": {
			args:  []string{"/bar"},
			errFn: expectErr,
		},
		"trailing slash": {
			args:  []string{"bar/"},
			errFn: expectErr,
		},
	}
	for k, testCase := range testCases {
		pods, _ := testData()
		b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClientWith(t, map[string]string{
			"/namespaces/test/pods/foo": runtime.EncodeOrDie(latest.Codec, &pods.Items[0]),
			"/namespaces/test/pods/bar": runtime.EncodeOrDie(latest.Codec, &pods.Items[0]),
			"/nodes/foo":                runtime.EncodeOrDie(latest.Codec, &api.Node{ObjectMeta: api.ObjectMeta{Name: "foo"}}),
		})).
			NamespaceParam("test").DefaultNamespace().
			ResourceTypeOrNameArgs(true, testCase.args...)

		r := b.Do()

		if !testCase.errFn(r.Err()) {
			t.Errorf("%s: unexpected error: %v", k, r.Err())
		}
		if r.Err() != nil {
			continue
		}
		switch {
		case (r.singular && len(testCase.args) != 1),
			(!r.singular && len(testCase.args) == 1):
			t.Errorf("%s: result had unexpected singular value", k)
		}
		info, err := r.Infos()
		if err != nil {
			// test error
			continue
		}
		if len(info) != len(testCase.args) {
			t.Errorf("%s: unexpected number of infos returned: %#v", info)
		}
	}
}

func TestStream(t *testing.T) {
	r, pods, rc := streamTestData()
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		NamespaceParam("test").Stream(r, "STDIN").Flatten()

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || singular || len(test.Infos) != 3 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}
	if !api.Semantic.DeepDerivative([]runtime.Object{&pods.Items[0], &pods.Items[1], &rc.Items[0]}, test.Objects()) {
		t.Errorf("unexpected visited objects: %#v", test.Objects())
	}
}

func TestYAMLStream(t *testing.T) {
	r, pods, rc := streamYAMLTestData()
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		NamespaceParam("test").Stream(r, "STDIN").Flatten()

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || singular || len(test.Infos) != 3 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}
	if !api.Semantic.DeepDerivative([]runtime.Object{&pods.Items[0], &pods.Items[1], &rc.Items[0]}, test.Objects()) {
		t.Errorf("unexpected visited objects: %#v", test.Objects())
	}
}

func TestMultipleObject(t *testing.T) {
	r, pods, svc := streamTestData()
	obj, err := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		NamespaceParam("test").Stream(r, "STDIN").Flatten().
		Do().Object()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := &api.List{
		Items: []runtime.Object{
			&pods.Items[0],
			&pods.Items[1],
			&svc.Items[0],
		},
	}
	if !api.Semantic.DeepDerivative(expected, obj) {
		t.Errorf("unexpected visited objects: %#v", obj)
	}
}

func TestSingularObject(t *testing.T) {
	obj, err := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		NamespaceParam("test").DefaultNamespace().
		FilenameParam("../../../examples/guestbook/redis-master-controller.json").
		Flatten().
		Do().Object()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rc, ok := obj.(*api.ReplicationController)
	if !ok {
		t.Fatalf("unexpected object: %#v", obj)
	}
	if rc.Name != "redis-master-controller" || rc.Namespace != "test" {
		t.Errorf("unexpected controller: %#v", rc)
	}
}

func TestListObject(t *testing.T) {
	pods, _ := testData()
	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClientWith(t, map[string]string{
		"/namespaces/test/pods?labels=a%3Db": runtime.EncodeOrDie(latest.Codec, pods),
	})).
		SelectorParam("a=b").
		NamespaceParam("test").
		ResourceTypeOrNameArgs(true, "pods").
		Flatten()

	obj, err := b.Do().Object()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	list, ok := obj.(*api.List)
	if !ok {
		t.Fatalf("unexpected object: %#v", obj)
	}
	if list.ResourceVersion != pods.ResourceVersion || len(list.Items) != 2 {
		t.Errorf("unexpected list: %#v", list)
	}

	mapping, err := b.Do().ResourceMapping()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mapping.Resource != "pods" {
		t.Errorf("unexpected resource mapping: %#v", mapping)
	}
}

func TestListObjectWithDifferentVersions(t *testing.T) {
	pods, svc := testData()
	obj, err := NewBuilder(latest.RESTMapper, api.Scheme, fakeClientWith(t, map[string]string{
		"/namespaces/test/pods?labels=a%3Db":     runtime.EncodeOrDie(latest.Codec, pods),
		"/namespaces/test/services?labels=a%3Db": runtime.EncodeOrDie(latest.Codec, svc),
	})).
		SelectorParam("a=b").
		NamespaceParam("test").
		ResourceTypeOrNameArgs(true, "pods,services").
		Flatten().
		Do().Object()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	list, ok := obj.(*api.List)
	if !ok {
		t.Fatalf("unexpected object: %#v", obj)
	}
	// resource version differs between type lists, so it's not possible to get a single version.
	if list.ResourceVersion != "" || len(list.Items) != 3 {
		t.Errorf("unexpected list: %#v", list)
	}
}

func TestWatch(t *testing.T) {
	_, svc := testData()
	w, err := NewBuilder(latest.RESTMapper, api.Scheme, fakeClientWith(t, map[string]string{
		"/watch/namespaces/test/services/redis-master?resourceVersion=12": watchBody(watch.Event{
			Type:   watch.Added,
			Object: &svc.Items[0],
		}),
	})).
		NamespaceParam("test").DefaultNamespace().
		FilenameParam("../../../examples/guestbook/redis-master-service.json").Flatten().
		Do().Watch("12")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	defer w.Stop()
	ch := w.ResultChan()
	select {
	case obj := <-ch:
		if obj.Type != watch.Added {
			t.Fatalf("unexpected watch event", obj)
		}
		service, ok := obj.Object.(*api.Service)
		if !ok {
			t.Fatalf("unexpected object: %#v", obj)
		}
		if service.Name != "baz" || service.ResourceVersion != "12" {
			t.Errorf("unexpected service: %#v", service)
		}
	}
}

func TestWatchMultipleError(t *testing.T) {
	_, err := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		NamespaceParam("test").DefaultNamespace().
		FilenameParam("../../../examples/guestbook/redis-master-controller.json").Flatten().
		FilenameParam("../../../examples/guestbook/redis-master-controller.json").Flatten().
		Do().Watch("")

	if err == nil {
		t.Fatalf("unexpected non-error")
	}
}

func TestLatest(t *testing.T) {
	r, _, _ := streamTestData()
	newPod := &api.Pod{
		ObjectMeta: api.ObjectMeta{Name: "foo", Namespace: "test", ResourceVersion: "13"},
	}
	newPod2 := &api.Pod{
		ObjectMeta: api.ObjectMeta{Name: "bar", Namespace: "test", ResourceVersion: "14"},
	}
	newSvc := &api.Service{
		ObjectMeta: api.ObjectMeta{Name: "baz", Namespace: "test", ResourceVersion: "15"},
	}

	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClientWith(t, map[string]string{
		"/namespaces/test/pods/foo":     runtime.EncodeOrDie(latest.Codec, newPod),
		"/namespaces/test/pods/bar":     runtime.EncodeOrDie(latest.Codec, newPod2),
		"/namespaces/test/services/baz": runtime.EncodeOrDie(latest.Codec, newSvc),
	})).
		NamespaceParam("other").Stream(r, "STDIN").Flatten().Latest()

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || singular || len(test.Infos) != 3 {
		t.Fatalf("unexpected response: %v %t %#v", err, singular, test.Infos)
	}
	if !api.Semantic.DeepDerivative([]runtime.Object{newPod, newPod2, newSvc}, test.Objects()) {
		t.Errorf("unexpected visited objects: %#v", test.Objects())
	}
}

func TestIgnoreStreamErrors(t *testing.T) {
	pods, svc := testData()

	r, w := io.Pipe()
	go func() {
		defer w.Close()
		w.Write([]byte(`{}`))
		w.Write([]byte(runtime.EncodeOrDie(latest.Codec, &pods.Items[0])))
	}()

	r2, w2 := io.Pipe()
	go func() {
		defer w2.Close()
		w2.Write([]byte(`{}`))
		w2.Write([]byte(runtime.EncodeOrDie(latest.Codec, &svc.Items[0])))
	}()

	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		ContinueOnError(). // TODO: order seems bad, but allows clients to determine what they want...
		Stream(r, "1").Stream(r2, "2")

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err != nil || singular || len(test.Infos) != 2 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}

	if !api.Semantic.DeepDerivative([]runtime.Object{&pods.Items[0], &svc.Items[0]}, test.Objects()) {
		t.Errorf("unexpected visited objects: %#v", test.Objects())
	}
}

func TestReceiveMultipleErrors(t *testing.T) {
	pods, svc := testData()

	r, w := io.Pipe()
	go func() {
		defer w.Close()
		w.Write([]byte(`{}`))
		w.Write([]byte(runtime.EncodeOrDie(latest.Codec, &pods.Items[0])))
	}()

	r2, w2 := io.Pipe()
	go func() {
		defer w2.Close()
		w2.Write([]byte(`{}`))
		w2.Write([]byte(runtime.EncodeOrDie(latest.Codec, &svc.Items[0])))
	}()

	b := NewBuilder(latest.RESTMapper, api.Scheme, fakeClient()).
		Stream(r, "1").Stream(r2, "2").
		ContinueOnError()

	test := &testVisitor{}
	singular := false

	err := b.Do().IntoSingular(&singular).Visit(test.Handle)
	if err == nil || singular || len(test.Infos) != 0 {
		t.Fatalf("unexpected response: %v %f %#v", err, singular, test.Infos)
	}

	errs, ok := err.(errors.Aggregate)
	if !ok {
		t.Fatalf("unexpected error: %v", reflect.TypeOf(err))
	}
	if len(errs.Errors()) != 2 {
		t.Errorf("unexpected errors", errs)
	}
}
