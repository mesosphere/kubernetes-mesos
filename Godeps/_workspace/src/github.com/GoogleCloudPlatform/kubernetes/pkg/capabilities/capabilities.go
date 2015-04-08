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

package capabilities

import (
	"sync"
)

// Capabilities defines the set of capabilities available within the system.
// For now these are global.  Eventually they may be per-user
type Capabilities struct {
	AllowPrivileged bool

	// List of pod sources for which using host network is allowed.
	HostNetworkSources []string
}

var once sync.Once
var capabilities *Capabilities

// Initialize the capability set.  This can only be done once per binary, subsequent calls are ignored.
func Initialize(c Capabilities) {
	// Only do this once
	once.Do(func() {
		capabilities = &c
	})
}

// SetCapabilitiesForTests.  Convenience method for testing.  This should only be called from tests.
func SetForTests(c Capabilities) {
	capabilities = &c
}

// Returns a read-only copy of the system capabilities.
func Get() Capabilities {
	if capabilities == nil {
		Initialize(Capabilities{
			AllowPrivileged:    false,
			HostNetworkSources: []string{},
		})
	}
	return *capabilities
}
