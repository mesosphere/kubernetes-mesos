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

package kubectl

import (
	"reflect"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

func TestGenerateService(t *testing.T) {
	tests := []struct {
		params   map[string]string
		expected api.Service
	}{
		{
			params: map[string]string{
				"selector":       "foo=bar,baz=blah",
				"name":           "test",
				"port":           "80",
				"protocol":       "TCP",
				"container-port": "1234",
			},
			expected: api.Service{
				ObjectMeta: api.ObjectMeta{
					Name: "test",
				},
				Spec: api.ServiceSpec{
					Selector: map[string]string{
						"foo": "bar",
						"baz": "blah",
					},
					Port:       80,
					Protocol:   "TCP",
					TargetPort: util.NewIntOrStringFromInt(1234),
				},
			},
		},
		{
			params: map[string]string{
				"selector":       "foo=bar,baz=blah",
				"name":           "test",
				"port":           "80",
				"protocol":       "UDP",
				"container-port": "foobar",
			},
			expected: api.Service{
				ObjectMeta: api.ObjectMeta{
					Name: "test",
				},
				Spec: api.ServiceSpec{
					Selector: map[string]string{
						"foo": "bar",
						"baz": "blah",
					},
					Port:       80,
					Protocol:   "UDP",
					TargetPort: util.NewIntOrStringFromString("foobar"),
				},
			},
		},
		{
			params: map[string]string{
				"selector":       "foo=bar,baz=blah",
				"labels":         "key1=value1,key2=value2",
				"name":           "test",
				"port":           "80",
				"protocol":       "TCP",
				"container-port": "1234",
			},
			expected: api.Service{
				ObjectMeta: api.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"key1": "value1",
						"key2": "value2",
					},
				},
				Spec: api.ServiceSpec{
					Selector: map[string]string{
						"foo": "bar",
						"baz": "blah",
					},
					Port:       80,
					Protocol:   "TCP",
					TargetPort: util.NewIntOrStringFromInt(1234),
				},
			},
		},
		{
			params: map[string]string{
				"selector":       "foo=bar,baz=blah",
				"name":           "test",
				"port":           "80",
				"protocol":       "UDP",
				"container-port": "foobar",
				"public-ip":      "1.2.3.4",
			},
			expected: api.Service{
				ObjectMeta: api.ObjectMeta{
					Name: "test",
				},
				Spec: api.ServiceSpec{
					Selector: map[string]string{
						"foo": "bar",
						"baz": "blah",
					},
					Port:       80,
					Protocol:   "UDP",
					PublicIPs:  []string{"1.2.3.4"},
					TargetPort: util.NewIntOrStringFromString("foobar"),
				},
			},
		},
		{
			params: map[string]string{
				"selector":                      "foo=bar,baz=blah",
				"name":                          "test",
				"port":                          "80",
				"protocol":                      "UDP",
				"container-port":                "foobar",
				"public-ip":                     "1.2.3.4",
				"create-external-load-balancer": "true",
			},
			expected: api.Service{
				ObjectMeta: api.ObjectMeta{
					Name: "test",
				},
				Spec: api.ServiceSpec{
					Selector: map[string]string{
						"foo": "bar",
						"baz": "blah",
					},
					Port:                       80,
					Protocol:                   "UDP",
					PublicIPs:                  []string{"1.2.3.4"},
					TargetPort:                 util.NewIntOrStringFromString("foobar"),
					CreateExternalLoadBalancer: true,
				},
			},
		},
	}
	generator := ServiceGenerator{}
	for _, test := range tests {
		obj, err := generator.Generate(test.params)
		if !reflect.DeepEqual(obj, &test.expected) {
			t.Errorf("expected:\n%#v\ngot\n%#v\n", &test.expected, obj)
		}
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	}
}
