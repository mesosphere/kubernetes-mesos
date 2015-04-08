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

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/resource"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/conversion"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	"github.com/davecgh/go-spew/spew"
)

// Conversion error conveniently packages up errors in conversions.
type ConversionError struct {
	In, Out interface{}
	Message string
}

// Return a helpful string about the error
func (c *ConversionError) Error() string {
	return spew.Sprintf(
		"Conversion error: %s. (in: %v(%+v) out: %v)",
		c.Message, reflect.TypeOf(c.In), c.In, reflect.TypeOf(c.Out),
	)
}

// Semantic can do semantic deep equality checks for api objects.
// Example: api.Semantic.DeepEqual(aPod, aPodWithNonNilButEmptyMaps) == true
var Semantic = conversion.EqualitiesOrDie(
	func(a, b resource.Quantity) bool {
		// Ignore formatting, only care that numeric value stayed the same.
		// TODO: if we decide it's important, after we drop v1beta1/2, we
		// could start comparing format.
		//
		// Uninitialized quantities are equivilent to 0 quantities.
		if a.Amount == nil && b.MilliValue() == 0 {
			return true
		}
		if b.Amount == nil && a.MilliValue() == 0 {
			return true
		}
		if a.Amount == nil || b.Amount == nil {
			return false
		}
		return a.Amount.Cmp(b.Amount) == 0
	},
	func(a, b util.Time) bool {
		return a.UTC() == b.UTC()
	},
	func(a, b labels.Selector) bool {
		return a.String() == b.String()
	},
	func(a, b fields.Selector) bool {
		return a.String() == b.String()
	},
)

var standardResources = util.NewStringSet(
	string(ResourceMemory),
	string(ResourceCPU),
	string(ResourcePods),
	string(ResourceQuotas),
	string(ResourceServices),
	string(ResourceReplicationControllers),
	string(ResourceStorage))

func IsStandardResourceName(str string) bool {
	return standardResources.Has(str)
}

// NewDeleteOptions returns a DeleteOptions indicating the resource should
// be deleted within the specified grace period. Use zero to indicate
// immediate deletion. If you would prefer to use the default grace period,
// use &api.DeleteOptions{} directly.
func NewDeleteOptions(grace int64) *DeleteOptions {
	return &DeleteOptions{GracePeriodSeconds: &grace}
}

// this function aims to check if the service portal IP is set or not
// the objective is not to perform validation here
func IsServiceIPSet(service *Service) bool {
	return service.Spec.PortalIP != PortalIPNone && service.Spec.PortalIP != ""
}

// this function aims to check if the service portal IP is requested or not
func IsServiceIPRequested(service *Service) bool {
	return service.Spec.PortalIP == ""
}

var standardFinalizers = util.NewStringSet(
	string(FinalizerKubernetes))

func IsStandardFinalizerName(str string) bool {
	return standardFinalizers.Has(str)
}
