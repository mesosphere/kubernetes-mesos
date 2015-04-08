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

package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/rest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
	"golang.org/x/net/websocket"
)

// watchJSON defines the expected JSON wire equivalent of watch.Event
type watchJSON struct {
	Type   watch.EventType `json:"type,omitempty"`
	Object json.RawMessage `json:"object,omitempty"`
}

var watchTestTable = []struct {
	t   watch.EventType
	obj runtime.Object
}{
	{watch.Added, &Simple{ObjectMeta: api.ObjectMeta{Name: "foo"}}},
	{watch.Modified, &Simple{ObjectMeta: api.ObjectMeta{Name: "bar"}}},
	{watch.Deleted, &Simple{ObjectMeta: api.ObjectMeta{Name: "bar"}}},
}

func TestWatchWebsocket(t *testing.T) {
	simpleStorage := &SimpleRESTStorage{}
	_ = rest.Watcher(simpleStorage) // Give compile error if this doesn't work.
	handler := handle(map[string]rest.Storage{"simples": simpleStorage})
	server := httptest.NewServer(handler)
	defer server.Close()

	dest, _ := url.Parse(server.URL)
	dest.Scheme = "ws" // Required by websocket, though the server never sees it.
	dest.Path = "/api/version/watch/simples"
	dest.RawQuery = ""

	ws, err := websocket.Dial(dest.String(), "", "http://localhost")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	try := func(action watch.EventType, object runtime.Object) {
		// Send
		simpleStorage.fakeWatch.Action(action, object)
		// Test receive
		var got watchJSON
		err := websocket.JSON.Receive(ws, &got)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if got.Type != action {
			t.Errorf("Unexpected type: %v", got.Type)
		}
		gotObj, err := codec.Decode(got.Object)
		if err != nil {
			t.Fatalf("Decode error: %v", err)
		}
		if _, err := api.GetReference(gotObj); err != nil {
			t.Errorf("Unable to construct reference: %v", err)
		}
		if e, a := object, gotObj; !reflect.DeepEqual(e, a) {
			t.Errorf("Expected %#v, got %#v", e, a)
		}
	}

	for _, item := range watchTestTable {
		try(item.t, item.obj)
	}
	simpleStorage.fakeWatch.Stop()

	var got watchJSON
	err = websocket.JSON.Receive(ws, &got)
	if err == nil {
		t.Errorf("Unexpected non-error")
	}
}

func TestWatchHTTP(t *testing.T) {
	simpleStorage := &SimpleRESTStorage{}
	handler := handle(map[string]rest.Storage{"simples": simpleStorage})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := http.Client{}

	dest, _ := url.Parse(server.URL)
	dest.Path = "/api/version/watch/simples"
	dest.RawQuery = ""

	request, err := http.NewRequest("GET", dest.String(), nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Errorf("Unexpected response %#v", response)
	}

	decoder := json.NewDecoder(response.Body)

	for i, item := range watchTestTable {
		// Send
		simpleStorage.fakeWatch.Action(item.t, item.obj)
		// Test receive
		var got watchJSON
		err := decoder.Decode(&got)
		if err != nil {
			t.Fatalf("%d: Unexpected error: %v", i, err)
		}
		if got.Type != item.t {
			t.Errorf("%d: Unexpected type: %v", i, got.Type)
		}
		t.Logf("obj: %v", string(got.Object))
		gotObj, err := codec.Decode(got.Object)
		if err != nil {
			t.Fatalf("Decode error: %v", err)
		}
		t.Logf("obj: %#v", gotObj)
		if _, err := api.GetReference(gotObj); err != nil {
			t.Errorf("Unable to construct reference: %v", err)
		}
		if e, a := item.obj, gotObj; !reflect.DeepEqual(e, a) {
			t.Errorf("Expected %#v, got %#v", e, a)
		}
	}
	simpleStorage.fakeWatch.Stop()

	var got watchJSON
	err = decoder.Decode(&got)
	if err == nil {
		t.Errorf("Unexpected non-error")
	}
}

func TestWatchParamParsing(t *testing.T) {
	simpleStorage := &SimpleRESTStorage{}
	handler := handle(map[string]rest.Storage{"simples": simpleStorage})
	server := httptest.NewServer(handler)
	defer server.Close()

	dest, _ := url.Parse(server.URL)
	dest.Path = "/api/" + testVersion + "/watch/simples"

	table := []struct {
		rawQuery        string
		resourceVersion string
		labelSelector   string
		fieldSelector   string
		namespace       string
	}{
		{
			rawQuery:        "resourceVersion=1234",
			resourceVersion: "1234",
			labelSelector:   "",
			fieldSelector:   "",
			namespace:       api.NamespaceAll,
		}, {
			rawQuery:        "namespace=default&resourceVersion=314159&fields=Host%3D&labels=name%3Dfoo",
			resourceVersion: "314159",
			labelSelector:   "name=foo",
			fieldSelector:   "Host=",
			namespace:       api.NamespaceDefault,
		}, {
			rawQuery:        "namespace=watchother&fields=id%3dfoo&resourceVersion=1492",
			resourceVersion: "1492",
			labelSelector:   "",
			fieldSelector:   "id=foo",
			namespace:       "watchother",
		}, {
			rawQuery:        "",
			resourceVersion: "",
			labelSelector:   "",
			fieldSelector:   "",
			namespace:       api.NamespaceAll,
		},
	}

	for _, item := range table {
		simpleStorage.requestedLabelSelector = labels.Everything()
		simpleStorage.requestedFieldSelector = fields.Everything()
		simpleStorage.requestedResourceVersion = "5" // Prove this is set in all cases
		simpleStorage.requestedResourceNamespace = ""
		dest.RawQuery = item.rawQuery
		resp, err := http.Get(dest.String())
		if err != nil {
			t.Errorf("%v: unexpected error: %v", item.rawQuery, err)
			continue
		}
		resp.Body.Close()
		if e, a := item.namespace, simpleStorage.requestedResourceNamespace; e != a {
			t.Errorf("%v: expected %v, got %v", item.rawQuery, e, a)
		}
		if e, a := item.resourceVersion, simpleStorage.requestedResourceVersion; e != a {
			t.Errorf("%v: expected %v, got %v", item.rawQuery, e, a)
		}
		if e, a := item.labelSelector, simpleStorage.requestedLabelSelector.String(); e != a {
			t.Errorf("%v: expected %v, got %v", item.rawQuery, e, a)
		}
		if e, a := item.fieldSelector, simpleStorage.requestedFieldSelector.String(); e != a {
			t.Errorf("%v: expected %v, got %v", item.rawQuery, e, a)
		}
	}
}

func TestWatchProtocolSelection(t *testing.T) {
	simpleStorage := &SimpleRESTStorage{}
	handler := handle(map[string]rest.Storage{"simples": simpleStorage})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer server.CloseClientConnections()
	client := http.Client{}

	dest, _ := url.Parse(server.URL)
	dest.Path = "/api/version/watch/simples"
	dest.RawQuery = ""

	table := []struct {
		isWebsocket bool
		connHeader  string
	}{
		{true, "Upgrade"},
		{true, "keep-alive, Upgrade"},
		{true, "upgrade"},
		{false, "keep-alive"},
	}

	for _, item := range table {
		request, err := http.NewRequest("GET", dest.String(), nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		request.Header.Set("Connection", item.connHeader)
		request.Header.Set("Upgrade", "websocket")

		response, err := client.Do(request)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// The requests recognized as websocket requests based on connection
		// and upgrade headers will not also have the necessary Sec-Websocket-*
		// headers so it is expected to throw a 400
		if item.isWebsocket && response.StatusCode != http.StatusBadRequest {
			t.Errorf("Unexpected response %#v", response)
		}

		if !item.isWebsocket && response.StatusCode != http.StatusOK {
			t.Errorf("Unexpected response %#v", response)
		}
	}
}
