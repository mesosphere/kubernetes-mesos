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

package tools

import (
	"strconv"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	"github.com/coreos/go-etcd/etcd"
)

// APIObjectVersioner implements versioning and extracting etcd node information
// for objects that have an embedded ObjectMeta or ListMeta field.
type APIObjectVersioner struct{}

// UpdateObject implements EtcdVersioner
func (a APIObjectVersioner) UpdateObject(obj runtime.Object, node *etcd.Node) error {
	objectMeta, err := api.ObjectMetaFor(obj)
	if err != nil {
		return err
	}
	if ttl := node.Expiration; ttl != nil {
		objectMeta.DeletionTimestamp = &util.Time{*node.Expiration}
	}
	version := node.ModifiedIndex
	versionString := ""
	if version != 0 {
		versionString = strconv.FormatUint(version, 10)
	}
	objectMeta.ResourceVersion = versionString
	return nil
}

// UpdateList implements EtcdVersioner
func (a APIObjectVersioner) UpdateList(obj runtime.Object, resourceVersion uint64) error {
	listMeta, err := api.ListMetaFor(obj)
	if err != nil || listMeta == nil {
		return err
	}
	versionString := ""
	if resourceVersion != 0 {
		versionString = strconv.FormatUint(resourceVersion, 10)
	}
	listMeta.ResourceVersion = versionString
	return nil
}

// ObjectResourceVersion implements EtcdVersioner
func (a APIObjectVersioner) ObjectResourceVersion(obj runtime.Object) (uint64, error) {
	meta, err := api.ObjectMetaFor(obj)
	if err != nil {
		return 0, err
	}
	version := meta.ResourceVersion
	if len(version) == 0 {
		return 0, nil
	}
	return strconv.ParseUint(version, 10, 64)
}

// APIObjectVersioner implements EtcdVersioner
var _ EtcdVersioner = APIObjectVersioner{}
