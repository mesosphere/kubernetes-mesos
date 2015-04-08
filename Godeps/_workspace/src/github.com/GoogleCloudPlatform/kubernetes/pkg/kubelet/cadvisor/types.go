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

package cadvisor

import (
	cadvisorApi "github.com/google/cadvisor/info/v1"
	cadvisorApiV2 "github.com/google/cadvisor/info/v2"
)

// Interface is an abstract interface for testability.  It abstracts the interface to cAdvisor.
type Interface interface {
	DockerContainer(name string, req *cadvisorApi.ContainerInfoRequest) (cadvisorApi.ContainerInfo, error)
	ContainerInfo(name string, req *cadvisorApi.ContainerInfoRequest) (*cadvisorApi.ContainerInfo, error)
	MachineInfo() (*cadvisorApi.MachineInfo, error)

	// Returns usage information about the filesystem holding Docker images.
	DockerImagesFsInfo() (cadvisorApiV2.FsInfo, error)
}
