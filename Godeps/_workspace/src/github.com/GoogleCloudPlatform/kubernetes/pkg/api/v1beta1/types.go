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

package v1beta1

import (
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
	"github.com/fsouza/go-dockerclient"
)

// Common string formats
// ---------------------
// Many fields in this API have formatting requirements.  The commonly used
// formats are defined here.
//
// C_IDENTIFIER:  This is a string that conforms the definition of an "identifier"
//     in the C language.  This is captured by the following regex:
//         [A-Za-z_][A-Za-z0-9_]*
//     This defines the format, but not the length restriction, which should be
//     specified at the definition of any field of this type.
//
// DNS_LABEL:  This is a string, no more than 63 characters long, that conforms
//     to the definition of a "label" in RFCs 1035 and 1123.  This is captured
//     by the following regex:
//         [a-z0-9]([-a-z0-9]*[a-z0-9])?
//
//  DNS_SUBDOMAIN:  This is a string, no more than 253 characters long, that conforms
//      to the definition of a "subdomain" in RFCs 1035 and 1123.  This is captured
//      by the following regex:
//         [a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*
//     or more simply:
//         DNS_LABEL(\.DNS_LABEL)*

// ContainerManifest corresponds to the Container Manifest format, documented at:
// https://developers.google.com/compute/docs/containers/container_vms#container_manifest
// This is used as the representation of Kubernetes workloads.
type ContainerManifest struct {
	// Required: This must be a supported version string, such as "v1beta1".
	Version string `yaml:"version" json:"version"`
	// Required: This must be a DNS_SUBDOMAIN.
	// TODO: ID on Manifest is deprecated and will be removed in the future.
	ID         string      `yaml:"id" json:"id"`
	Volumes    []Volume    `yaml:"volumes" json:"volumes"`
	Containers []Container `yaml:"containers" json:"containers"`
}

// Volume represents a named volume in a pod that may be accessed by any containers in the pod.
type Volume struct {
	// Required: This must be a DNS_LABEL.  Each volume in a pod must have
	// a unique name.
	Name string `yaml:"name" json:"name"`
	// Source represents the location and type of a volume to mount.
	// This is optional for now. If not specified, the Volume is implied to be an EmptyDir.
	// This implied behavior is deprecated and will be removed in a future version.
	Source *VolumeSource `yaml:"source" json:"source"`
}

type VolumeSource struct {
	// Only one of the following sources may be specified
	// HostDirectory represents a pre-existing directory on the host machine that is directly
	// exposed to the container. This is generally used for system agents or other privileged
	// things that are allowed to see the host machine. Most containers will NOT need this.
	// TODO(jonesdl) We need to restrict who can use host directory mounts and
	// who can/can not mount host directories as read/write.
	HostDirectory *HostDirectory `yaml:"hostDir" json:"hostDir"`
	// EmptyDirectory represents a temporary directory that shares a pod's lifetime.
	EmptyDirectory *EmptyDirectory `yaml:"emptyDir" json:"emptyDir"`
}

// Bare host directory volume.
type HostDirectory struct {
	Path string `yaml:"path" json:"path"`
}

type EmptyDirectory struct{}

// Port represents a network port in a single container
type Port struct {
	// Optional: If specified, this must be a DNS_LABEL.  Each named port
	// in a pod must have a unique name.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Optional: Defaults to ContainerPort.  If specified, this must be a
	// valid port number, 0 < x < 65536.
	HostPort int `yaml:"hostPort,omitempty" json:"hostPort,omitempty"`
	// Required: This must be a valid port number, 0 < x < 65536.
	ContainerPort int `yaml:"containerPort" json:"containerPort"`
	// Optional: Defaults to "TCP".
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	// Optional: What host IP to bind the external port to.
	HostIP string `yaml:"hostIP,omitempty" json:"hostIP,omitempty"`
}

// VolumeMount describes a mounting of a Volume within a container
type VolumeMount struct {
	// Required: This must match the Name of a Volume [above].
	Name string `yaml:"name" json:"name"`
	// Optional: Defaults to false (read-write).
	ReadOnly bool `yaml:"readOnly,omitempty" json:"readOnly,omitempty"`
	// Required.
	// Exactly one of the following must be set.  If both are set, prefer MountPath.
	// DEPRECATED: Path will be removed in a future version of the API.
	MountPath string `yaml:"mountPath,omitempty" json:"mountPath,omitempty"`
	Path      string `yaml:"path,omitempty" json:"path,omitempty"`
	// One of: "LOCAL" (local volume) or "HOST" (external mount from the host). Default: LOCAL.
	// DEPRECATED: MountType will be removed in a future version of the API.
	MountType string `yaml:"mountType,omitempty" json:"mountType,omitempty"`
}

// EnvVar represents an environment variable present in a Container
type EnvVar struct {
	// Required: This must be a C_IDENTIFIER.
	// Exactly one of the following must be set.  If both are set, prefer Name.
	// DEPRECATED: EnvVar.Key will be removed in a future version of the API.
	Name string `yaml:"name" json:"name"`
	Key  string `yaml:"key,omitempty" json:"key,omitempty"`
	// Optional: defaults to "".
	Value string `yaml:"value,omitempty" json:"value,omitempty"`
}

// HTTPGetProbe describes a liveness probe based on HTTP Get requests.
type HTTPGetProbe struct {
	// Path to access on the http server
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
	// Name or number of the port to access on the container
	Port string `yaml:"port,omitempty" json:"port,omitempty"`
	// Host name to connect to.  Optional, default: "localhost"
	Host string `yaml:"host,omitempty" json:"host,omitempty"`
}

// LivenessProbe describes a liveness probe to be examined to the container.
type LivenessProbe struct {
	// Type of liveness probe.  Current legal values "http"
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
	// HTTPGetProbe parameters, required if Type == 'http'
	HTTPGet *HTTPGetProbe `yaml:"httpGet,omitempty" json:"httpGet,omitempty"`
	// Length of time before health checking is activated.  In seconds.
	InitialDelaySeconds int64 `yaml:"initialDelaySeconds,omitempty" json:"initialDelaySeconds,omitempty"`
}

// Container represents a single container that is expected to be run on the host.
type Container struct {
	// Required: This must be a DNS_LABEL.  Each container in a pod must
	// have a unique name.
	Name string `yaml:"name" json:"name"`
	// Required.
	Image string `yaml:"image" json:"image"`
	// Optional: Defaults to whatever is defined in the image.
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
	// Optional: Defaults to Docker's default.
	WorkingDir string   `yaml:"workingDir,omitempty" json:"workingDir,omitempty"`
	Ports      []Port   `yaml:"ports,omitempty" json:"ports,omitempty"`
	Env        []EnvVar `yaml:"env,omitempty" json:"env,omitempty"`
	// Optional: Defaults to unlimited.
	Memory int `yaml:"memory,omitempty" json:"memory,omitempty"`
	// Optional: Defaults to unlimited.
	CPU           int            `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	VolumeMounts  []VolumeMount  `yaml:"volumeMounts,omitempty" json:"volumeMounts,omitempty"`
	LivenessProbe *LivenessProbe `yaml:"livenessProbe,omitempty" json:"livenessProbe,omitempty"`
}

// Event is the representation of an event logged to etcd backends
type Event struct {
	Event     string             `json:"event,omitempty"`
	Manifest  *ContainerManifest `json:"manifest,omitempty"`
	Container *Container         `json:"container,omitempty"`
	Timestamp int64              `json:"timestamp"`
}

// The below types are used by kube_client and api_server.

// JSONBase is shared by all objects sent to, or returned from the client
type JSONBase struct {
	Kind              string `json:"kind,omitempty" yaml:"kind,omitempty"`
	ID                string `json:"id,omitempty" yaml:"id,omitempty"`
	CreationTimestamp string `json:"creationTimestamp,omitempty" yaml:"creationTimestamp,omitempty"`
	SelfLink          string `json:"selfLink,omitempty" yaml:"selfLink,omitempty"`
	ResourceVersion   uint64 `json:"resourceVersion,omitempty" yaml:"resourceVersion,omitempty"`
	APIVersion        string `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
}

// PodStatus represents a status of a pod.
type PodStatus string

// These are the valid statuses of pods.
const (
	PodRunning PodStatus = "Running"
	PodPending PodStatus = "Pending"
	PodStopped PodStatus = "Stopped"
)

// PodInfo contains one entry for every container with available info.
type PodInfo map[string]docker.Container

// PodState is the state of a pod, used as either input (desired state) or output (current state)
type PodState struct {
	Manifest ContainerManifest `json:"manifest,omitempty" yaml:"manifest,omitempty"`
	Status   PodStatus         `json:"status,omitempty" yaml:"status,omitempty"`
	Host     string            `json:"host,omitempty" yaml:"host,omitempty"`
	HostIP   string            `json:"hostIP,omitempty" yaml:"hostIP,omitempty"`
	PodIP    string            `json:"podIP,omitempty" yaml:"podIP,omitempty"`

	// The key of this map is the *name* of the container within the manifest; it has one
	// entry per container in the manifest. The value of this map is currently the output
	// of `docker inspect`. This output format is *not* final and should not be relied
	// upon.
	// TODO: Make real decisions about what our info should look like.
	Info PodInfo `json:"info,omitempty" yaml:"info,omitempty"`
}

// PodList is a list of Pods.
type PodList struct {
	JSONBase `json:",inline" yaml:",inline"`
	Items    []Pod `json:"items" yaml:"items,omitempty"`
}

// Pod is a collection of containers, used as either input (create, update) or as output (list, get)
type Pod struct {
	JSONBase     `json:",inline" yaml:",inline"`
	Labels       map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	DesiredState PodState          `json:"desiredState,omitempty" yaml:"desiredState,omitempty"`
	CurrentState PodState          `json:"currentState,omitempty" yaml:"currentState,omitempty"`
}

// ReplicationControllerState is the state of a replication controller, either input (create, update) or as output (list, get)
type ReplicationControllerState struct {
	Replicas        int               `json:"replicas" yaml:"replicas"`
	ReplicaSelector map[string]string `json:"replicaSelector,omitempty" yaml:"replicaSelector,omitempty"`
	PodTemplate     PodTemplate       `json:"podTemplate,omitempty" yaml:"podTemplate,omitempty"`
}

// ReplicationControllerList is a collection of replication controllers.
type ReplicationControllerList struct {
	JSONBase `json:",inline" yaml:",inline"`
	Items    []ReplicationController `json:"items,omitempty" yaml:"items,omitempty"`
}

// ReplicationController represents the configuration of a replication controller
type ReplicationController struct {
	JSONBase     `json:",inline" yaml:",inline"`
	DesiredState ReplicationControllerState `json:"desiredState,omitempty" yaml:"desiredState,omitempty"`
	Labels       map[string]string          `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// PodTemplate holds the information used for creating pods
type PodTemplate struct {
	DesiredState PodState          `json:"desiredState,omitempty" yaml:"desiredState,omitempty"`
	Labels       map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// ServiceList holds a list of services
type ServiceList struct {
	JSONBase `json:",inline" yaml:",inline"`
	Items    []Service `json:"items" yaml:"items"`
}

// Service is a named abstraction of software service (for example, mysql) consisting of local port
// (for example 3306) that the proxy listens on, and the selector that determines which pods
// will answer requests sent through the proxy.
type Service struct {
	JSONBase `json:",inline" yaml:",inline"`
	Port     int `json:"port,omitempty" yaml:"port,omitempty"`

	// This service's labels.
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`

	// This service will route traffic to pods having labels matching this selector.
	Selector                   map[string]string `json:"selector,omitempty" yaml:"selector,omitempty"`
	CreateExternalLoadBalancer bool              `json:"createExternalLoadBalancer,omitempty" yaml:"createExternalLoadBalancer,omitempty"`

	// ContainerPort is the name of the port on the container to direct traffic to.
	// Optional, if unspecified use the first port on the container.
	ContainerPort util.IntOrString `json:"containerPort,omitempty" yaml:"containerPort,omitempty"`
}

// Endpoints is a collection of endpoints that implement the actual service, for example:
// Name: "mysql", Endpoints: ["10.10.1.1:1909", "10.10.2.2:8834"]
type Endpoints struct {
	Name      string
	Endpoints []string
}

// Minion is a worker node in Kubernetenes.
// The name of the minion according to etcd is in JSONBase.ID.
type Minion struct {
	JSONBase `json:",inline" yaml:",inline"`
	// Queried from cloud provider, if available.
	HostIP string `json:"hostIP,omitempty" yaml:"hostIP,omitempty"`
}

// MinionList is a list of minions.
type MinionList struct {
	JSONBase `json:",inline" yaml:",inline"`
	Items    []Minion `json:"minions,omitempty" yaml:"minions,omitempty"`
}

// Status is a return value for calls that don't return other objects.
// Arguably, this could go in apiserver, but I'm including it here so clients needn't
// import both.
type Status struct {
	JSONBase `json:",inline" yaml:",inline"`
	// One of: "success", "failure", "working" (for operations not yet completed)
	// TODO: if "working", include an operation identifier so final status can be
	// checked.
	Status string `json:"status,omitempty" yaml:"status,omitempty"`
	// Details about the status. May be an error description or an
	// operation number for later polling.
	Details string `json:"details,omitempty" yaml:"details,omitempty"`
	// Suggested HTTP return code for this status, 0 if not set.
	Code int `json:"code,omitempty" yaml:"code,omitempty"`
}

// Values of Status.Status
const (
	StatusSuccess = "success"
	StatusFailure = "failure"
	StatusWorking = "working"
)

// ServerOp is an operation delivered to API clients.
type ServerOp struct {
	JSONBase `yaml:",inline" json:",inline"`
}

// ServerOpList is a list of operations, as delivered to API clients.
type ServerOpList struct {
	JSONBase `yaml:",inline" json:",inline"`
	Items    []ServerOp `yaml:"items,omitempty" json:"items,omitempty"`
}

// WatchEvent objects are streamed from the api server in response to a watch request.
type WatchEvent struct {
	// The type of the watch event; added, modified, or deleted.
	Type watch.EventType

	// For added or modified objects, this is the new object; for deleted objects,
	// it's the state of the object immediately prior to its deletion.
	Object APIObject
}

// APIObject has appropriate encoder and decoder functions, such that on the wire, it's
// stored as a []byte, but in memory, the contained object is accessable as an interface{}
// via the Get() function. Only objects having a JSONBase may be stored via APIObject.
// The purpose of this is to allow an API object of type known only at runtime to be
// embedded within other API objects.
type APIObject struct {
	Object interface{}
}
