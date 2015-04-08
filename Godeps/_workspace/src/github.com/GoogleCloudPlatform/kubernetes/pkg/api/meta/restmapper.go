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

// TODO: move everything in this file to pkg/api/rest
package meta

import (
	"fmt"
	"strings"
)

// Implements RESTScope interface
type restScope struct {
	name             RESTScopeName
	paramName        string
	paramPath        bool
	paramDescription string
}

func (r *restScope) Name() RESTScopeName {
	return r.name
}
func (r *restScope) ParamName() string {
	return r.paramName
}
func (r *restScope) ParamPath() bool {
	return r.paramPath
}
func (r *restScope) ParamDescription() string {
	return r.paramDescription
}

var RESTScopeNamespaceLegacy = &restScope{
	name:             RESTScopeNameNamespace,
	paramName:        "namespace",
	paramPath:        false,
	paramDescription: "object name and auth scope, such as for teams and projects",
}

var RESTScopeNamespace = &restScope{
	name:             RESTScopeNameNamespace,
	paramName:        "namespaces",
	paramPath:        true,
	paramDescription: "object name and auth scope, such as for teams and projects",
}

var RESTScopeRoot = &restScope{
	name: RESTScopeNameRoot,
}

// typeMeta is used as a key for lookup in the mapping between REST path and
// API object.
type typeMeta struct {
	APIVersion string
	Kind       string
}

// DefaultRESTMapper exposes mappings between the types defined in a
// runtime.Scheme. It assumes that all types defined the provided scheme
// can be mapped with the provided MetadataAccessor and Codec interfaces.
//
// The resource name of a Kind is defined as the lowercase,
// English-plural version of the Kind string in v1beta3 and onwards,
// and as the camelCase version of the name in v1beta1 and v1beta2.
// When converting from resource to Kind, the singular version of the
// resource name is also accepted for convenience.
//
// TODO: Only accept plural for some operations for increased control?
// (`get pod bar` vs `get pods bar`)
type DefaultRESTMapper struct {
	mapping        map[string]typeMeta
	reverse        map[typeMeta]string
	scopes         map[typeMeta]RESTScope
	versions       []string
	interfacesFunc VersionInterfacesFunc
}

// VersionInterfacesFunc returns the appropriate codec, typer, and metadata accessor for a
// given api version, or false if no such api version exists.
type VersionInterfacesFunc func(apiVersion string) (*VersionInterfaces, bool)

// NewDefaultRESTMapper initializes a mapping between Kind and APIVersion
// to a resource name and back based on the objects in a runtime.Scheme
// and the Kubernetes API conventions. Takes a priority list of the versions to
// search when an object has no default version (set empty to return an error)
// and a function that retrieves the correct codec and metadata for a given version.
func NewDefaultRESTMapper(versions []string, f VersionInterfacesFunc) *DefaultRESTMapper {
	mapping := make(map[string]typeMeta)
	reverse := make(map[typeMeta]string)
	scopes := make(map[typeMeta]RESTScope)
	// TODO: verify name mappings work correctly when versions differ

	return &DefaultRESTMapper{
		mapping:        mapping,
		reverse:        reverse,
		scopes:         scopes,
		versions:       versions,
		interfacesFunc: f,
	}
}

func (m *DefaultRESTMapper) Add(scope RESTScope, kind string, version string, mixedCase bool) {
	plural, singular := kindToResource(kind, mixedCase)
	meta := typeMeta{APIVersion: version, Kind: kind}
	if _, ok := m.mapping[plural]; !ok {
		m.mapping[plural] = meta
		m.mapping[singular] = meta
		if strings.ToLower(plural) != plural {
			m.mapping[strings.ToLower(plural)] = meta
			m.mapping[strings.ToLower(singular)] = meta
		}
	}
	m.reverse[meta] = plural
	m.scopes[meta] = scope
}

// kindToResource converts Kind to a resource name.
func kindToResource(kind string, mixedCase bool) (plural, singular string) {
	if len(kind) == 0 {
		return
	}
	if mixedCase {
		// Legacy support for mixed case names
		singular = strings.ToLower(kind[:1]) + kind[1:]
	} else {
		singular = strings.ToLower(kind)
	}
	switch string(singular[len(singular)-1]) {
	case "s":
		plural = singular
	case "y":
		plural = strings.TrimSuffix(singular, "y") + "ies"
	default:
		plural = singular + "s"
	}
	return
}

// VersionAndKindForResource implements RESTMapper
func (m *DefaultRESTMapper) VersionAndKindForResource(resource string) (defaultVersion, kind string, err error) {
	meta, ok := m.mapping[resource]
	if !ok {
		return "", "", fmt.Errorf("no resource %q has been defined", resource)
	}
	return meta.APIVersion, meta.Kind, nil
}

// RESTMapping returns a struct representing the resource path and conversion interfaces a
// RESTClient should use to operate on the provided kind in order of versions. If a version search
// order is not provided, the search order provided to DefaultRESTMapper will be used to resolve which
// APIVersion should be used to access the named kind.
func (m *DefaultRESTMapper) RESTMapping(kind string, versions ...string) (*RESTMapping, error) {
	// Pick an appropriate version
	var version string
	hadVersion := false
	for _, v := range versions {
		if len(v) == 0 {
			continue
		}
		hadVersion = true
		if _, ok := m.reverse[typeMeta{APIVersion: v, Kind: kind}]; ok {
			version = v
			break
		}
	}
	// Use the default preferred versions
	if !hadVersion && len(version) == 0 {
		for _, v := range m.versions {
			if _, ok := m.reverse[typeMeta{APIVersion: v, Kind: kind}]; ok {
				version = v
				break
			}
		}
	}
	if len(version) == 0 {
		return nil, fmt.Errorf("no object named %q is registered", kind)
	}

	// Ensure we have a REST mapping
	resource, ok := m.reverse[typeMeta{APIVersion: version, Kind: kind}]
	if !ok {
		found := []string{}
		for _, v := range m.versions {
			if _, ok := m.reverse[typeMeta{APIVersion: v, Kind: kind}]; ok {
				found = append(found, v)
			}
		}
		if len(found) > 0 {
			return nil, fmt.Errorf("object with kind %q exists in versions %q, not %q", kind, strings.Join(found, ", "), version)
		}
		return nil, fmt.Errorf("the provided version %q and kind %q cannot be mapped to a supported object", version, kind)
	}

	// Ensure we have a REST scope
	scope, ok := m.scopes[typeMeta{APIVersion: version, Kind: kind}]
	if !ok {
		return nil, fmt.Errorf("the provided version %q and kind %q cannot be mapped to a supported scope", version, kind)
	}

	interfaces, ok := m.interfacesFunc(version)
	if !ok {
		return nil, fmt.Errorf("the provided version %q has no relevant versions", version)
	}

	return &RESTMapping{
		Resource:   resource,
		APIVersion: version,
		Kind:       kind,
		Scope:      scope,

		Codec:            interfaces.Codec,
		ObjectConvertor:  interfaces.ObjectConvertor,
		MetadataAccessor: interfaces.MetadataAccessor,
	}, nil
}
