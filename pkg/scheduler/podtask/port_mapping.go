package podtask

import (
	"fmt"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	log "github.com/golang/glog"
	mesos "github.com/mesos/mesos-go/mesosproto"
)

const PortMappingLabelKey = "k8s.mesosphere.io/portMapping"

// PortMapper represents a specifc port mapping strategy.
type PortMapper string

// PortMappers contains all implementations of PortMapper keyed by their string
// value.
var PortMappers = map[string]func(*T, *mesos.Offer) ([]PortMapping, error){
	"fixed":    FixedHostPorts,
	"wildcard": WildcardHostPorts,
}

// PortMap computes PortMappings for a given Task with the given Offer.
func (m PortMapper) PortMap(t *T, offer *mesos.Offer) ([]PortMapping, error) {
	pm, ok := PortMappers[string(m)]
	if !ok {
		log.Warningf("illegal host-port mapping spec %q, defaulting to fixed", m)
		pm = FixedHostPorts
	}
	return pm(t, offer)
}

// PortMapping represents a mapping of host ports to pod container ports.
type PortMapping struct {
	ContainerIdx int // index of the container in the pod spec
	PortIdx      int // index of the port in a container's port spec
	OfferPort    uint64
}

// NewPortMapper returns an appropriate PortMapper for a given Pod.
func NewPortMapper(pod *api.Pod) PortMapper {
	filter := map[string]string{PortMappingLabelKey: "fixed"}
	selector := labels.Set(filter).AsSelector()
	if selector.Matches(labels.Set(pod.Labels)) {
		return "fixed"
	}
	return "wildcard"
}

// FixedHostPorts maps a Container.HostPort to the same exact offered host
// port. It ignores .HostPort = 0.
func FixedHostPorts(t *T, offer *mesos.Offer) ([]PortMapping, error) {
	requiredPorts := make(map[uint64]PortMapping)
	mapping := []PortMapping{}
	for i, container := range t.Pod.Spec.Containers {
		// strip all port==0 from this array; k8s already knows what to do with zero-
		// ports (it does not create 'port bindings' on the minion-host); we need to
		// remove the wildcards from this array since they don't consume host resources
		for pi, port := range container.Ports {
			if port.HostPort == 0 {
				continue // ignore
			}
			m := PortMapping{
				ContainerIdx: i,
				PortIdx:      pi,
				OfferPort:    uint64(port.HostPort),
			}
			if entry, inuse := requiredPorts[uint64(port.HostPort)]; inuse {
				return nil, &DuplicateHostPortError{entry, m}
			}
			requiredPorts[uint64(port.HostPort)] = m
		}
	}
	foreachRange(offer, "ports", func(bp, ep uint64) {
		for port, _ := range requiredPorts {
			log.V(3).Infof("evaluating port range {%d:%d} %d", bp, ep, port)
			if (bp <= port) && (port <= ep) {
				mapping = append(mapping, requiredPorts[port])
				delete(requiredPorts, port)
			}
		}
	})
	unsatisfiedPorts := len(requiredPorts)
	if unsatisfiedPorts > 0 {
		err := &PortAllocationError{
			PodId: t.Pod.Name,
		}
		for p, _ := range requiredPorts {
			err.Ports = append(err.Ports, p)
		}
		return nil, err
	}
	return mapping, nil
}

// WildcardHostPorts is the same as FixedHostPorts, except that .HostPort of 0
// are mapped to any offered port.
func WildcardHostPorts(t *T, offer *mesos.Offer) ([]PortMapping, error) {
	mapping, err := FixedHostPorts(t, offer)
	if err != nil {
		return nil, err
	}
	taken := make(map[uint64]struct{})
	for _, entry := range mapping {
		taken[entry.OfferPort] = struct{}{}
	}
	wildports := []PortMapping{}
	for i, container := range t.Pod.Spec.Containers {
		for pi, port := range container.Ports {
			if port.HostPort == 0 {
				wildports = append(wildports, PortMapping{
					ContainerIdx: i,
					PortIdx:      pi,
				})
			}
		}
	}
	remaining := len(wildports)
	foreachRange(offer, "ports", func(bp, ep uint64) {
		log.V(3).Infof("Searching for wildcard port in range {%d:%d}", bp, ep)
		for _, entry := range wildports {
			if entry.OfferPort != 0 {
				continue
			}
			for port := bp; port <= ep && remaining > 0; port++ {
				if _, inuse := taken[port]; inuse {
					continue
				}
				entry.OfferPort = port
				mapping = append(mapping, entry)
				remaining--
				taken[port] = struct{}{}
				break
			}
		}
	})
	if remaining > 0 {
		err := &PortAllocationError{
			PodId: t.Pod.Name,
		}
		// it doesn't make sense to include a port list here because they were all zero (wildcards)
		return nil, err
	}
	return mapping, nil
}

type PortAllocationError struct {
	PodId string
	Ports []uint64
}

func (err *PortAllocationError) Error() string {
	return fmt.Sprintf("Could not schedule pod %s: %d port(s) could not be allocated", err.PodId, len(err.Ports))
}

type DuplicateHostPortError struct {
	m1, m2 PortMapping
}

func (err *DuplicateHostPortError) Error() string {
	return fmt.Sprintf(
		"Host port %d is specified for container %d, pod %d and container %d, pod %d",
		err.m1.OfferPort, err.m1.ContainerIdx, err.m1.PortIdx, err.m2.ContainerIdx, err.m2.PortIdx)
}
