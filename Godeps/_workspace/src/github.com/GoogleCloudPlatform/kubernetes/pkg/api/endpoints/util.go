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
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"hash"
	"sort"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

// RepackSubsets takes a slice of EndpointSubset objects, expands it to the full
// representation, and then repacks that into the canonical layout.  This
// ensures that code which operates on these objects can rely on the common
// form for things like comparison.  The result is a newly allocated slice.
func RepackSubsets(subsets []api.EndpointSubset) []api.EndpointSubset {
	// First map each unique port definition to the sets of hosts that
	// offer it.  The sets of hosts must be de-duped, using IP as the key.
	type ipString string
	allAddrs := map[ipString]*api.EndpointAddress{}
	portsToAddrs := map[api.EndpointPort]addressSet{}
	for i := range subsets {
		for j := range subsets[i].Ports {
			epp := &subsets[i].Ports[j]
			for k := range subsets[i].Addresses {
				epa := &subsets[i].Addresses[k]
				ipstr := ipString(epa.IP)
				// Accumulate the most "complete" address we can.
				if allAddrs[ipstr] == nil {
					// Make a copy so we don't write to the
					// input args of this function.
					p := &api.EndpointAddress{}
					*p = *epa
					allAddrs[ipstr] = p
				} else if allAddrs[ipstr].TargetRef == nil {
					allAddrs[ipstr].TargetRef = epa.TargetRef
				}
				// Remember that this port maps to this address.
				if _, found := portsToAddrs[*epp]; !found {
					portsToAddrs[*epp] = addressSet{}
				}
				portsToAddrs[*epp].Insert(allAddrs[ipstr])
			}
		}
	}

	// Next, map the sets of hosts to the sets of ports they offer.
	// Go does not allow maps or slices as keys to maps, so we have
	// to synthesize and artificial key and do a sort of 2-part
	// associative entity.
	type keyString string
	addrSets := map[keyString]addressSet{}
	addrSetsToPorts := map[keyString][]api.EndpointPort{}
	for epp, addrs := range portsToAddrs {
		key := keyString(hashAddresses(addrs))
		addrSets[key] = addrs
		addrSetsToPorts[key] = append(addrSetsToPorts[key], epp)
	}

	// Next, build the N-to-M association the API wants.
	final := []api.EndpointSubset{}
	for key, ports := range addrSetsToPorts {
		addrs := []api.EndpointAddress{}
		for k := range addrSets[key] {
			addrs = append(addrs, *k)
		}
		final = append(final, api.EndpointSubset{Addresses: addrs, Ports: ports})
	}

	// Finally, sort it.
	return SortSubsets(final)
}

type addressSet map[*api.EndpointAddress]struct{}

func (set addressSet) Insert(addr *api.EndpointAddress) {
	set[addr] = struct{}{}
}

func hashAddresses(addrs addressSet) string {
	// Flatten the list of addresses into a string so it can be used as a
	// map key.
	hasher := md5.New()
	util.DeepHashObject(hasher, addrs)
	return hex.EncodeToString(hasher.Sum(nil)[0:])
}

// SortSubsets sorts an array of EndpointSubset objects in place.  For ease of
// use it returns the input slice.
func SortSubsets(subsets []api.EndpointSubset) []api.EndpointSubset {
	for i := range subsets {
		ss := &subsets[i]
		sort.Sort(addrsByIP(ss.Addresses))
		sort.Sort(portsByHash(ss.Ports))
	}
	sort.Sort(subsetsByHash(subsets))
	return subsets
}

func hashObject(hasher hash.Hash, obj interface{}) []byte {
	util.DeepHashObject(hasher, obj)
	return hasher.Sum(nil)
}

type subsetsByHash []api.EndpointSubset

func (sl subsetsByHash) Len() int      { return len(sl) }
func (sl subsetsByHash) Swap(i, j int) { sl[i], sl[j] = sl[j], sl[i] }
func (sl subsetsByHash) Less(i, j int) bool {
	hasher := md5.New()
	h1 := hashObject(hasher, sl[i])
	h2 := hashObject(hasher, sl[j])
	return bytes.Compare(h1, h2) < 0
}

type addrsByIP []api.EndpointAddress

func (sl addrsByIP) Len() int      { return len(sl) }
func (sl addrsByIP) Swap(i, j int) { sl[i], sl[j] = sl[j], sl[i] }
func (sl addrsByIP) Less(i, j int) bool {
	return bytes.Compare([]byte(sl[i].IP), []byte(sl[j].IP)) < 0
}

type portsByHash []api.EndpointPort

func (sl portsByHash) Len() int      { return len(sl) }
func (sl portsByHash) Swap(i, j int) { sl[i], sl[j] = sl[j], sl[i] }
func (sl portsByHash) Less(i, j int) bool {
	hasher := md5.New()
	h1 := hashObject(hasher, sl[i])
	h2 := hashObject(hasher, sl[j])
	return bytes.Compare(h1, h2) < 0
}
