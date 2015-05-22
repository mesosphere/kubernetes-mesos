package podtask

import (
	"fmt"
	"sort"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
)

const (
	PortMappingLabelKey = "k8s.mesosphere.io/portMapping"
	// FixedPorts represents a fixed port mapping strategy.
	FixedPorts PortMapper = "fixed"
	// WildcardPorts represents a fixed + wildcard port mapping strategy.
	WildcardPorts PortMapper = "wildcard"
)

// PortMapper represents a specifc port mapping strategy.
type PortMapper string

// NewPortMapper returns an appropriate PortMapper for a given Pod.
func NewPortMapper(pod *api.Pod) PortMapper {
	filter := map[string]string{PortMappingLabelKey: "fixed"}
	selector := labels.Set(filter).AsSelector()
	if selector.Matches(labels.Set(pod.Labels)) {
		return FixedPorts
	}
	return WildcardPorts
}

// PortMap computes PortMappings for the given Containers with the given offered port Ranges.
func (m PortMapper) PortMap(offered Ranges, pod string, cs ...api.Container) ([]PortMapping, error) {
	switch m {
	case WildcardPorts:
		return WildcardPortMap(offered, pod, cs...)
	case FixedPorts:
		fallthrough
	default:
		return FixedPortMap(offered, pod, cs...)
	}
}

// PortMapping represents a mapping from container ports to host ports for a
// given pod container.
type PortMapping struct {
	PodName        string
	ContainerIndex int
	PortIndex      int
	HostPort       uint64
}

// FixedPortMap maps container's HostPorts to the same Mesos offered ports.
// It ignores asked wildcard host ports (== 0).
func FixedPortMap(offered Ranges, pod string, cs ...api.Container) ([]PortMapping, error) {
	return portmap(offered, pod, cs, func(p *api.ContainerPort) bool {
		return p.HostPort != 0
	})
}

// WildcardPortMap is the same as FixedPorts, except that .HostPorts of 0
// are mapped to any offered port.
func WildcardPortMap(offered Ranges, pod string, cs ...api.Container) ([]PortMapping, error) {
	return portmap(offered, pod, cs, func(_ *api.ContainerPort) bool {
		return true
	})
}

// byHostPort is an auxiliary type used to sort []PortMappings by their
// HostPorts.
type byHostPort []PortMapping

func (pm byHostPort) Len() int           { return len(pm) }
func (pm byHostPort) Less(i, j int) bool { return pm[i].HostPort < pm[j].HostPort }
func (pm byHostPort) Swap(i, j int)      { pm[i], pm[j] = pm[j], pm[i] }

// portmap maps matched task's container's host ports to offered ports.
func portmap(offered Ranges, pod string, cs []api.Container, match func(*api.ContainerPort) bool) ([]PortMapping, error) {
	if len(cs) == 0 {
		return []PortMapping{}, nil
	}

	var wanted []PortMapping
	for i := range cs {
		for j, p := range cs[i].Ports {
			if match(&p) {
				wanted = append(wanted, PortMapping{
					PodName:        pod,
					ContainerIndex: i,
					PortIndex:      j,
					HostPort:       uint64(p.HostPort),
				})
			}
		}
	}
	// higher ports have highest priority, wildcard has the lowest
	sort.Sort(sort.Reverse(byHostPort(wanted)))

	taken := make(map[uint64]int, len(wanted))
	for i := range wanted {
		if offered.Size() < uint64(len(wanted)-len(taken)) {
			break // no more available ports in the offer
		} else if wanted[i].HostPort == 0 {
			wanted[i].HostPort = offered.Min()
		}
		m := wanted[i]
		if j, ok := taken[m.HostPort]; ok {
			return nil, &DuplicateHostPortError{m, wanted[j]}
		} else if offered, ok = offered.Partition(m.HostPort); ok {
			taken[m.HostPort] = i
		}
	}

	if len(taken) == len(wanted) {
		return wanted, nil
	}

	missing := make([]uint64, 0, len(wanted)-len(taken))
	for _, m := range wanted {
		if _, ok := taken[m.HostPort]; !ok {
			missing = append(missing, m.HostPort)
		}
	}
	return nil, &PortAllocationError{PodName: pod, Ports: missing}
}

// Errors
type (
	// PortAllocationError happens when a some ports can't be allocated from the
	// offered ports.
	PortAllocationError struct {
		PodName string
		Ports   []uint64
	}
	// DuplicateHostPortError happens when the same host port is used for more than
	// one container in a pod.
	DuplicateHostPortError struct{ m1, m2 PortMapping }
)

// Error implements the error interface.
func (err *PortAllocationError) Error() string {
	return fmt.Sprintf("Failed to allocate ports %v for pod %s", err.Ports, err.PodName)
}

// Error implements the error interface.
func (err *DuplicateHostPortError) Error() string {
	return fmt.Sprintf(
		"Host port %d wanted by containers (%s:%d) and (%s:%d)",
		err.m1.HostPort,
		err.m1.PodName,
		err.m1.ContainerIndex,
		err.m2.PodName,
		err.m2.ContainerIndex,
	)
}
