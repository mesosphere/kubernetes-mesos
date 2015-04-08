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
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/resource"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/conversion"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

// Codec is the identity codec for this package - it can only convert itself
// to itself.
var Codec = runtime.CodecFor(Scheme, "")

func init() {
	Scheme.AddDefaultingFuncs(
		func(obj *ListOptions) {
			obj.LabelSelector = labels.Everything()
			obj.FieldSelector = fields.Everything()
		},
	)
	Scheme.AddConversionFuncs(
		func(in *util.Time, out *util.Time, s conversion.Scope) error {
			// Cannot deep copy these, because time.Time has unexported fields.
			*out = *in
			return nil
		},
		func(in *string, out *labels.Selector, s conversion.Scope) error {
			selector, err := labels.Parse(*in)
			if err != nil {
				return err
			}
			*out = selector
			return nil
		},
		func(in *string, out *fields.Selector, s conversion.Scope) error {
			selector, err := fields.ParseSelector(*in)
			if err != nil {
				return err
			}
			*out = selector
			return nil
		},
		func(in *labels.Selector, out *string, s conversion.Scope) error {
			if *in == nil {
				return nil
			}
			*out = (*in).String()
			return nil
		},
		func(in *fields.Selector, out *string, s conversion.Scope) error {
			if *in == nil {
				return nil
			}
			*out = (*in).String()
			return nil
		},
		func(in *resource.Quantity, out *resource.Quantity, s conversion.Scope) error {
			// Cannot deep copy these, because inf.Dec has unexported fields.
			*out = *in.Copy()
			return nil
		},
		// Convert ContainerManifest to Pod
		func(in *ContainerManifest, out *Pod, s conversion.Scope) error {
			out.Spec.Containers = in.Containers
			out.Spec.Volumes = in.Volumes
			out.Spec.RestartPolicy = in.RestartPolicy
			out.Spec.DNSPolicy = in.DNSPolicy
			out.Name = in.ID
			out.UID = in.UUID

			if in.ID != "" {
				out.SelfLink = "/api/v1beta1/pods/" + in.ID
			}

			return nil
		},
		func(in *Pod, out *ContainerManifest, s conversion.Scope) error {
			out.Containers = in.Spec.Containers
			out.Volumes = in.Spec.Volumes
			out.RestartPolicy = in.Spec.RestartPolicy
			out.DNSPolicy = in.Spec.DNSPolicy
			out.Version = "v1beta2"
			out.ID = in.Name
			out.UUID = in.UID
			return nil
		},

		// ContainerManifestList
		func(in *ContainerManifestList, out *PodList, s conversion.Scope) error {
			if err := s.Convert(&in.Items, &out.Items, 0); err != nil {
				return err
			}
			for i := range out.Items {
				item := &out.Items[i]
				item.ResourceVersion = in.ResourceVersion
			}
			return nil
		},
		func(in *PodList, out *ContainerManifestList, s conversion.Scope) error {
			if err := s.Convert(&in.Items, &out.Items, 0); err != nil {
				return err
			}
			out.ResourceVersion = in.ResourceVersion
			return nil
		},

		// Conversion between Manifest and PodSpec
		func(in *PodSpec, out *ContainerManifest, s conversion.Scope) error {
			if err := s.Convert(&in.Volumes, &out.Volumes, 0); err != nil {
				return err
			}
			if err := s.Convert(&in.Containers, &out.Containers, 0); err != nil {
				return err
			}
			if err := s.Convert(&in.RestartPolicy, &out.RestartPolicy, 0); err != nil {
				return err
			}
			out.DNSPolicy = in.DNSPolicy
			out.Version = "v1beta2"
			return nil
		},
		func(in *ContainerManifest, out *PodSpec, s conversion.Scope) error {
			if err := s.Convert(&in.Volumes, &out.Volumes, 0); err != nil {
				return err
			}
			if err := s.Convert(&in.Containers, &out.Containers, 0); err != nil {
				return err
			}
			if err := s.Convert(&in.RestartPolicy, &out.RestartPolicy, 0); err != nil {
				return err
			}
			out.DNSPolicy = in.DNSPolicy
			return nil
		},
	)
}
