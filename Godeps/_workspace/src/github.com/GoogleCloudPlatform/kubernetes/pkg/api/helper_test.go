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

package api

import (
	"reflect"
	"testing"
)

func runTest(t *testing.T, source interface{}) {
	name := reflect.TypeOf(source).Name()
	data, err := Encode(source)
	if err != nil {
		t.Errorf("%v: %v (%#v)", name, err, source)
		return
	}
	obj2, err := Decode(data)
	if err != nil {
		t.Errorf("%v: %v", name, err)
		return
	}
	if !reflect.DeepEqual(source, obj2) {
		t.Errorf("1: %v: wanted %#v, got %#v", name, source, obj2)
		return
	}
	obj3 := reflect.New(reflect.TypeOf(source).Elem()).Interface()
	err = DecodeInto(data, obj3)
	if err != nil {
		t.Errorf("2: %v: %v", name, err)
		return
	}
	if !reflect.DeepEqual(source, obj3) {
		t.Errorf("3: %v: wanted %#v, got %#v", name, source, obj3)
		return
	}
}

func TestTypes(t *testing.T) {
	// TODO: auto-fill all fields.
	table := []interface{}{
		&Pod{
			JSONBase: JSONBase{
				ID: "mylittlepod",
			},
			Labels: map[string]string{
				"name": "pinky",
			},
		},
		&Service{},
		&ServiceList{
			Items: []Service{
				{
					Labels: map[string]string{
						"foo": "bar",
					},
				}, {
					Labels: map[string]string{
						"foo": "baz",
					},
				},
			},
		},
		&ReplicationControllerList{},
		&ReplicationController{},
		&PodList{},
	}
	for _, item := range table {
		runTest(t, item)
	}
}

func TestNonPtr(t *testing.T) {
	pod := Pod{
		Labels: map[string]string{"name": "foo"},
	}
	obj := interface{}(pod)
	data, err := Encode(obj)
	obj2, err2 := Decode(data)
	if err != nil || err2 != nil {
		t.Errorf("Failure: %v %v", err2, err2)
	}
	if _, ok := obj2.(*Pod); !ok {
		t.Errorf("Got wrong type")
	}
	if !reflect.DeepEqual(obj2, &pod) {
		t.Errorf("Expected:\n %#v,\n Got:\n %#v", &pod, obj2)
	}
}

func TestPtr(t *testing.T) {
	pod := Pod{
		Labels: map[string]string{"name": "foo"},
	}
	obj := interface{}(&pod)
	data, err := Encode(obj)
	obj2, err2 := Decode(data)
	if err != nil || err2 != nil {
		t.Errorf("Failure: %v %v", err2, err2)
	}
	if _, ok := obj2.(*Pod); !ok {
		t.Errorf("Got wrong type")
	}
	if !reflect.DeepEqual(obj2, &pod) {
		t.Errorf("Expected:\n %#v,\n Got:\n %#v", &pod, obj2)
	}
}

func TestBadJSONRejection(t *testing.T) {
	badJSONMissingKind := []byte(`{ }`)
	if _, err := Decode(badJSONMissingKind); err == nil {
		t.Errorf("Did not reject despite lack of kind field: %s", badJSONMissingKind)
	}
	badJSONUnknownType := []byte(`{"kind": "bar"}`)
	if _, err1 := Decode(badJSONUnknownType); err1 == nil {
		t.Errorf("Did not reject despite use of unknown type: %s", badJSONUnknownType)
	}
	/*badJSONKindMismatch := []byte(`{"kind": "Pod"}`)
	if err2 := DecodeInto(badJSONKindMismatch, &Minion{}); err2 == nil {
		t.Errorf("Kind is set but doesn't match the object type: %s", badJSONKindMismatch)
	}*/
}
