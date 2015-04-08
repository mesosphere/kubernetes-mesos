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

package nfs

import (
	"os"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/volume"
	"github.com/golang/glog"
)

// This is the primary entrypoint for volume plugins.
func ProbeVolumePlugins() []volume.VolumePlugin {
	return []volume.VolumePlugin{&nfsPlugin{nil, newNFSMounter()}}
}

type nfsPlugin struct {
	host    volume.VolumeHost
	mounter nfsMountInterface
}

var _ volume.VolumePlugin = &nfsPlugin{}

const (
	nfsPluginName = "kubernetes.io/nfs"
)

func (plugin *nfsPlugin) Init(host volume.VolumeHost) {
	plugin.host = host
}

func (plugin *nfsPlugin) Name() string {
	return nfsPluginName
}

func (plugin *nfsPlugin) CanSupport(spec *api.Volume) bool {
	if spec.VolumeSource.NFS != nil {
		return true
	}
	return false
}

func (plugin *nfsPlugin) GetAccessModes() []api.AccessModeType {
	return []api.AccessModeType{
		api.ReadWriteOnce,
		api.ReadOnlyMany,
		api.ReadWriteMany,
	}
}

func (plugin *nfsPlugin) NewBuilder(spec *api.Volume, podRef *api.ObjectReference) (volume.Builder, error) {
	return plugin.newBuilderInternal(spec, podRef, plugin.mounter)
}

func (plugin *nfsPlugin) newBuilderInternal(spec *api.Volume, podRef *api.ObjectReference, mounter nfsMountInterface) (volume.Builder, error) {
	return &nfs{
		volName:    spec.Name,
		server:     spec.VolumeSource.NFS.Server,
		exportPath: spec.VolumeSource.NFS.Path,
		readOnly:   spec.VolumeSource.NFS.ReadOnly,
		mounter:    mounter,
		podRef:     podRef,
		plugin:     plugin,
	}, nil
}

func (plugin *nfsPlugin) NewCleaner(volName string, podUID types.UID) (volume.Cleaner, error) {
	return plugin.newCleanerInternal(volName, podUID, plugin.mounter)
}

func (plugin *nfsPlugin) newCleanerInternal(volName string, podUID types.UID, mounter nfsMountInterface) (volume.Cleaner, error) {
	return &nfs{
		volName:    volName,
		server:     "",
		exportPath: "",
		readOnly:   false,
		mounter:    mounter,
		podRef:     &api.ObjectReference{UID: podUID},
		plugin:     plugin,
	}, nil
}

// NFS volumes represent a bare host file or directory mount of an NFS export.
type nfs struct {
	volName    string
	podRef     *api.ObjectReference
	server     string
	exportPath string
	readOnly   bool
	mounter    nfsMountInterface
	plugin     *nfsPlugin
}

// SetUp attaches the disk and bind mounts to the volume path.
func (nfsVolume *nfs) SetUp() error {
	return nfsVolume.SetUpAt(nfsVolume.GetPath())
}

func (nfsVolume *nfs) SetUpAt(dir string) error {
	mountpoint, err := nfsVolume.mounter.IsMountPoint(dir)
	glog.V(4).Infof("NFS mount set up: %s %v %v", dir, mountpoint, err)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if mountpoint {
		return nil
	}
	exportDir := nfsVolume.exportPath
	os.MkdirAll(dir, 0750)
	err = nfsVolume.mounter.Mount(nfsVolume.server, exportDir, dir, nfsVolume.readOnly)
	if err != nil {
		mountpoint, mntErr := nfsVolume.mounter.IsMountPoint(dir)
		if mntErr != nil {
			glog.Errorf("IsMountpoint check failed: %v", mntErr)
			return err
		}
		if mountpoint {
			if mntErr = nfsVolume.mounter.Unmount(dir); mntErr != nil {
				glog.Errorf("Failed to unmount: %v", mntErr)
				return err
			}
			mountpoint, mntErr := nfsVolume.mounter.IsMountPoint(dir)
			if mntErr != nil {
				glog.Errorf("IsMountpoint check failed: %v", mntErr)
				return err
			}
			if mountpoint {
				// This is very odd, we don't expect it.  We'll try again next sync loop.
				glog.Errorf("%s is still mounted, despite call to unmount().  Will try again next sync loop.", dir)
				return err
			}
		}
		os.Remove(dir)
		return err
	}
	return nil
}

func (nfsVolume *nfs) GetPath() string {
	name := nfsPluginName
	return nfsVolume.plugin.host.GetPodVolumeDir(nfsVolume.podRef.UID, util.EscapeQualifiedNameForDisk(name), nfsVolume.volName)
}

func (nfsVolume *nfs) TearDown() error {
	return nfsVolume.TearDownAt(nfsVolume.GetPath())
}

func (nfsVolume *nfs) TearDownAt(dir string) error {
	mountpoint, err := nfsVolume.mounter.IsMountPoint(dir)
	if err != nil {
		glog.Errorf("Error checking IsMountPoint: %v", err)
		return err
	}
	if !mountpoint {
		return os.Remove(dir)
	}

	if err := nfsVolume.mounter.Unmount(dir); err != nil {
		glog.Errorf("Unmounting failed: %v", err)
		return err
	}
	mountpoint, mntErr := nfsVolume.mounter.IsMountPoint(dir)
	if mntErr != nil {
		glog.Errorf("IsMountpoint check failed: %v", mntErr)
		return mntErr
	}
	if !mountpoint {
		if err := os.Remove(dir); err != nil {
			return err
		}
	}

	return nil
}
