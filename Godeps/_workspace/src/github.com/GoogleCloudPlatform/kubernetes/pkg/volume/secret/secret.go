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

package secret

import (
	"fmt"
	"io/ioutil"
	"path"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/volume"
	"github.com/golang/glog"
)

// ProbeVolumePlugin is the entry point for plugin detection in a package.
func ProbeVolumePlugins() []volume.VolumePlugin {
	return []volume.VolumePlugin{&secretPlugin{}}
}

const (
	secretPluginName = "kubernetes.io/secret"
)

// secretPlugin implements the VolumePlugin interface.
type secretPlugin struct {
	host volume.VolumeHost
}

func (plugin *secretPlugin) Init(host volume.VolumeHost) {
	plugin.host = host
}

func (plugin *secretPlugin) Name() string {
	return secretPluginName
}

func (plugin *secretPlugin) CanSupport(spec *api.Volume) bool {
	if spec.Secret != nil {
		return true
	}

	return false
}

func (plugin *secretPlugin) NewBuilder(spec *api.Volume, podRef *api.ObjectReference) (volume.Builder, error) {
	return plugin.newBuilderInternal(spec, podRef)
}

func (plugin *secretPlugin) newBuilderInternal(spec *api.Volume, podRef *api.ObjectReference) (volume.Builder, error) {
	return &secretVolume{spec.Name, *podRef, plugin, spec.Secret.SecretName}, nil
}

func (plugin *secretPlugin) NewCleaner(volName string, podUID types.UID) (volume.Cleaner, error) {
	return plugin.newCleanerInternal(volName, podUID)
}

func (plugin *secretPlugin) newCleanerInternal(volName string, podUID types.UID) (volume.Cleaner, error) {
	return &secretVolume{volName, api.ObjectReference{UID: podUID}, plugin, ""}, nil
}

// secretVolume handles retrieving secrets from the API server
// and placing them into the volume on the host.
type secretVolume struct {
	volName    string
	podRef     api.ObjectReference
	plugin     *secretPlugin
	secretName string
}

func (sv *secretVolume) SetUp() error {
	return sv.SetUpAt(sv.GetPath())
}

// This is the spec for the volume that this plugin wraps.
var wrappedVolumeSpec = &api.Volume{
	Name:         "not-used",
	VolumeSource: api.VolumeSource{EmptyDir: &api.EmptyDirVolumeSource{Medium: api.StorageTypeMemory}},
}

func (sv *secretVolume) SetUpAt(dir string) error {
	glog.V(3).Infof("Setting up volume %v for pod %v at %v", sv.volName, sv.podRef.UID, dir)

	// Wrap EmptyDir, let it do the setup.
	wrapped, err := sv.plugin.host.NewWrapperBuilder(wrappedVolumeSpec, &sv.podRef)
	if err != nil {
		return err
	}
	if err := wrapped.SetUpAt(dir); err != nil {
		return err
	}

	kubeClient := sv.plugin.host.GetKubeClient()
	if kubeClient == nil {
		return fmt.Errorf("Cannot setup secret volume %v because kube client is not configured", sv)
	}

	secret, err := kubeClient.Secrets(sv.podRef.Namespace).Get(sv.secretName)
	if err != nil {
		glog.Errorf("Couldn't get secret %v/%v", sv.podRef.Namespace, sv.secretName)
		return err
	} else {
		totalBytes := totalSecretBytes(secret)
		glog.V(3).Infof("Received secret %v/%v containing (%v) pieces of data, %v total bytes",
			sv.podRef.Namespace,
			sv.secretName,
			len(secret.Data),
			totalBytes)
	}

	for name, data := range secret.Data {
		hostFilePath := path.Join(dir, name)
		glog.V(3).Infof("Writing secret data %v/%v/%v (%v bytes) to host file %v", sv.podRef.Namespace, sv.secretName, len(data), hostFilePath)
		err := ioutil.WriteFile(hostFilePath, data, 0777)
		if err != nil {
			glog.Errorf("Error writing secret data to host path: %v, %v", hostFilePath, err)
			return err
		}
	}

	return nil
}

func totalSecretBytes(secret *api.Secret) int {
	totalSize := 0
	for _, bytes := range secret.Data {
		totalSize += len(bytes)
	}

	return totalSize
}

func (sv *secretVolume) GetPath() string {
	return sv.plugin.host.GetPodVolumeDir(sv.podRef.UID, util.EscapeQualifiedNameForDisk(secretPluginName), sv.volName)
}

func (sv *secretVolume) TearDown() error {
	return sv.TearDownAt(sv.GetPath())
}

func (sv *secretVolume) TearDownAt(dir string) error {
	glog.V(3).Infof("Tearing down volume %v for pod %v at %v", sv.volName, sv.podRef.UID, dir)

	// Wrap EmptyDir, let it do the teardown.
	wrapped, err := sv.plugin.host.NewWrapperCleaner(wrappedVolumeSpec, sv.podRef.UID)
	if err != nil {
		return err
	}
	return wrapped.TearDownAt(dir)
}
