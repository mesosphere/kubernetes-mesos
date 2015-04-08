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

package endpoints

import (
	"reflect"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/davecgh/go-spew/spew"
)

func TestPackSubsets(t *testing.T) {
	// The downside of table-driven tests is that some things have to live outside the table.
	fooObjRef := api.ObjectReference{Name: "foo"}
	barObjRef := api.ObjectReference{Name: "bar"}

	testCases := []struct {
		name   string
		given  []api.EndpointSubset
		expect []api.EndpointSubset
	}{
		{
			name:   "empty everything",
			given:  []api.EndpointSubset{{Addresses: []api.EndpointAddress{}, Ports: []api.EndpointPort{}}},
			expect: []api.EndpointSubset{},
		}, {
			name:   "empty addresses",
			given:  []api.EndpointSubset{{Addresses: []api.EndpointAddress{}, Ports: []api.EndpointPort{{Port: 111}}}},
			expect: []api.EndpointSubset{},
		}, {
			name:   "empty ports",
			given:  []api.EndpointSubset{{Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}}, Ports: []api.EndpointPort{}}},
			expect: []api.EndpointSubset{},
		}, {
			name: "one set, one ip, one port",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
		}, {
			name: "one set, two ips, one port",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}, {IP: "5.6.7.8"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}, {IP: "5.6.7.8"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
		}, {
			name: "one set, one ip, two ports",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}, {Port: 222}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}, {Port: 222}},
			}},
		}, {
			name: "one set, dup ips, one port",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}, {IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
		}, {
			name: "one set, dup ips with target-refs, one port",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{
					{IP: "1.2.3.4", TargetRef: &fooObjRef},
					{IP: "1.2.3.4", TargetRef: &barObjRef},
				},
				Ports: []api.EndpointPort{{Port: 111}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4", TargetRef: &fooObjRef}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
		}, {
			name: "one set, one ip, dup ports",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}, {Port: 111}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
		}, {
			name: "two sets, dup ip, dup port",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}, {
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
		}, {
			name: "two sets, dup ip, two ports",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}, {
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 222}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}, {Port: 222}},
			}},
		}, {
			name: "two sets, two ips, dup port",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}, {
				Addresses: []api.EndpointAddress{{IP: "5.6.7.8"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}, {IP: "5.6.7.8"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}},
		}, {
			name: "two sets, two ips, two ports",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}, {
				Addresses: []api.EndpointAddress{{IP: "5.6.7.8"}},
				Ports:     []api.EndpointPort{{Port: 222}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}, {
				Addresses: []api.EndpointAddress{{IP: "5.6.7.8"}},
				Ports:     []api.EndpointPort{{Port: 222}},
			}},
		}, {
			name: "four sets, three ips, three ports, jumbled",
			given: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}, {
				Addresses: []api.EndpointAddress{{IP: "1.2.3.5"}},
				Ports:     []api.EndpointPort{{Port: 222}},
			}, {
				Addresses: []api.EndpointAddress{{IP: "1.2.3.6"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}, {
				Addresses: []api.EndpointAddress{{IP: "1.2.3.5"}},
				Ports:     []api.EndpointPort{{Port: 333}},
			}},
			expect: []api.EndpointSubset{{
				Addresses: []api.EndpointAddress{{IP: "1.2.3.4"}, {IP: "1.2.3.6"}},
				Ports:     []api.EndpointPort{{Port: 111}},
			}, {
				Addresses: []api.EndpointAddress{{IP: "1.2.3.5"}},
				Ports:     []api.EndpointPort{{Port: 222}, {Port: 333}},
			}},
		},
	}

	for _, tc := range testCases {
		result := RepackSubsets(tc.given)
		if !reflect.DeepEqual(result, SortSubsets(tc.expect)) {
			t.Errorf("case %q: expected %s, got %s", tc.name, spew.Sprintf("%v", SortSubsets(tc.expect)), spew.Sprintf("%v", result))
		}
	}
}
