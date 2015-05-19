package podtask

import (
	"fmt"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	log "github.com/golang/glog"
)

const PortMappingLabelKey = "k8s.mesosphere.io/portMapping"

// PortMapper represents a specifc port mapping strategy.
type PortMapper string

// NewPortMapper returns an appropriate PortMapper for a given Pod.
func NewPortMapper(pod *api.Pod) PortMapper {
	filter := map[string]string{PortMappingLabelKey: "fixed"}
	selector := labels.Set(filter).AsSelector()
	if selector.Matches(labels.Set(pod.Labels)) {
		return "fixed"
	}
	return "wildcard"
}

// PortMappers contains all implementations of PortMapper keyed by their string
// value.
var PortMappers = map[string]func(*T, Ranges) ([]PortMapping, error){
	"fixed":    FixedPorts,
	"wildcard": WildcardPorts,
}

// PortMap computes PortMappings for a given Task with the given offered port Ranges.
func (m PortMapper) PortMap(t *T, offered Ranges) ([]PortMapping, error) {
	pm, ok := PortMappers[string(m)]
	if !ok {
		log.Warningf("illegal host-port mapping spec %q, defaulting to fixed", m)
		pm = FixedPorts
	}
	return pm(t, offered)
}

// PortMapping represents a mapping from container ports to host ports for a
// given pod container.
type PortMapping struct {
	*api.Pod
	*api.Container
	ContainerPort uint64
	HostPort      uint64
}

// FixedPorts maps a task's container's HostPorts to the same mesos offered ports.
// It ignores asked wildcard host ports (== 0).
func FixedPorts(task *T, offered Ranges) ([]PortMapping, error) {
	return ports(task, offered, func(p *api.ContainerPort) bool {
		return p.HostPort != 0
	})
}

// WildcardPorts is the same as FixedPorts, except that .HostPorts of 0
// are mapped to any offered port.
func WildcardPorts(task *T, offered Ranges) ([]PortMapping, error) {
	return ports(task, offered, func(_ *api.ContainerPort) bool { return true })
}

// ports maps matched task's container's host ports to offered ports.
func ports(task *T, offered Ranges, match func(*api.ContainerPort) bool) ([]PortMapping, error) {
	mapped := map[uint64]PortMapping{}
	missing := []uint64{}
	containers := task.Pod.Spec.Containers

	for i := range containers {
		for _, p := range containers[i].Ports {
			if !match(&p) {
				continue
			}

			m := PortMapping{
				Pod:           &task.Pod,
				Container:     &containers[i],
				ContainerPort: uint64(p.ContainerPort),
				HostPort:      uint64(p.HostPort),
			}

			if m2, ok := mapped[m.HostPort]; ok {
				return nil, &DuplicateHostPortError{m, m2}
			} else if offered, ok = offered.Partition(m.HostPort); !ok {
				missing = append(missing, m.HostPort)
			} else {
				mapped[m.HostPort] = m
			}
		}
	}

	if len(missing) > 0 {
		return nil, &PortAllocationError{Pod: &task.Pod, Ports: missing}
	}

	mappings := make([]PortMapping, 0, len(mapped))
	for _, m := range mapped {
		mappings = append(mappings, m)
	}

	return mappings, nil
}

// Errors
type (
	// PortAllocationError happens when a some ports can't be allocated from the
	// offered ports.
	PortAllocationError struct {
		*api.Pod
		Ports []uint64
	}
	// DuplicateHostPortError happens when the same host port is used for more than
	// one container in a pod.
	DuplicateHostPortError struct{ m1, m2 PortMapping }
)

// Error implements the error interface.
func (err *PortAllocationError) Error() string {
	return fmt.Sprintf("Failed to allocate ports %v for pod %s.", err.Ports, err.Pod.Name)
}

// Error implements the error interface.
func (err *DuplicateHostPortError) Error() string {
	return fmt.Sprintf(
		"Host port %d is defined for (%s:%s) and (%s:%s)",
		err.m1.HostPort,
		err.m1.Pod.Name,
		err.m1.Container.Name,
		err.m2.Pod.Name,
		err.m2.Container.Name,
	)
}
