package scheduler

import (
	"fmt"

	"code.google.com/p/go-uuid/uuid"
	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	"gopkg.in/v1/yaml"
)

const (
	containerCpus = 0.25 // initial CPU allocated for executor
	containerMem  = 64   // initial MB of memory allocated for executor
)

// A struct that describes a pod task.
type PodTask struct {
	ID       string
	Pod      *api.Pod
	TaskInfo *mesos.TaskInfo
	Launched bool
	Offer    PerishableOffer
	mapper   hostPortMappingFunc
	ports    []hostPortMapping
}

type hostPortMapping struct {
	cindex    int // containerIndex
	pindex    int // portIndex
	offerPort uint64
}

// TODO(jdef): A replicated pod may end up with different host ports across different hosts when
// host ports are chosen randomly from offers. Will this cause problems accessing a related service?
type hostPortMappingFunc func(t *PodTask, offer *mesos.Offer) ([]hostPortMapping, error)

type PortAllocationError struct {
	PodId string
	Ports []uint64
}

func (err *PortAllocationError) Error() string {
	return fmt.Sprintf("Could not schedule pod %s: %d port(s) could not be allocated", err.PodId, len(err.Ports))
}

type DuplicateHostPortError struct {
	m1, m2 hostPortMapping
}

func (err *DuplicateHostPortError) Error() string {
	return fmt.Sprintf(
		"Host port %d is specified for container %d, pod %d and container %d, pod %d",
		err.m1.offerPort, err.m1.cindex, err.m1.pindex, err.m2.cindex, err.m2.pindex)
}

// wildcard k8s host port mapping implementation: hostPort == 0 gets mapped to any available offer port
func wildcardHostPortMapping(t *PodTask, offer *mesos.Offer) ([]hostPortMapping, error) {

	mapping, err := defaultHostPortMapping(t, offer)
	if err != nil {
		return nil, err
	}
	taken := make(map[uint64]struct{})
	for _, entry := range mapping {
		taken[entry.offerPort] = struct{}{}
	}
	wildports := []hostPortMapping{}
	for i, container := range t.Pod.DesiredState.Manifest.Containers {
		for pi, port := range container.Ports {
			if port.HostPort == 0 {
				wildports = append(wildports, hostPortMapping{
					cindex: i,
					pindex: pi,
				})
			}
		}
	}
	remaining := len(wildports)
	for _, resource := range offer.Resources {
		if resource.GetName() == "ports" {
			for _, r := range (*resource).GetRanges().Range {
				bp := r.GetBegin()
				ep := r.GetEnd()
				log.V(3).Infof("Searching for wildcard port in range {%d:%d}", bp, ep)
				for _, entry := range wildports {
					if entry.offerPort != 0 {
						continue
					}
					for port := bp; port <= ep && remaining > 0; port++ {
						if _, inuse := taken[port]; inuse {
							continue
						}
						entry.offerPort = port
						mapping = append(mapping, entry)
						remaining--
						taken[port] = struct{}{}
						break
					}
				}
			}
		}
	}
	if remaining > 0 {
		err := &PortAllocationError{
			PodId: t.Pod.ID,
		}
		// it doesn't make sense to include a port list here because they were all zero (wildcards)
		return nil, err
	}
	return mapping, nil
}

// default k8s host port mapping implementation: hostPort == 0 means containerPort remains pod-private
func defaultHostPortMapping(t *PodTask, offer *mesos.Offer) ([]hostPortMapping, error) {
	requiredPorts := make(map[uint64]hostPortMapping)
	mapping := []hostPortMapping{}
	for i, container := range t.Pod.DesiredState.Manifest.Containers {
		// strip all port==0 from this array; k8s already knows what to do with zero-
		// ports (it does not create 'port bindings' on the minion-host); we need to
		// remove the wildcards from this array since they don't consume host resources
		for pi, port := range container.Ports {
			if port.HostPort == 0 {
				continue // ignore
			}
			m := hostPortMapping{
				cindex:    i,
				pindex:    pi,
				offerPort: uint64(port.HostPort),
			}
			if entry, inuse := requiredPorts[uint64(port.HostPort)]; inuse {
				return nil, &DuplicateHostPortError{entry, m}
			}
			requiredPorts[uint64(port.HostPort)] = m
		}
	}
	for _, resource := range offer.Resources {
		if resource.GetName() == "ports" {
			for _, r := range (*resource).GetRanges().Range {
				bp := r.GetBegin()
				ep := r.GetEnd()
				for port, _ := range requiredPorts {
					log.V(3).Infof("Evaluating port range {%d:%d} %d", bp, ep, port)
					if (bp <= port) && (port <= ep) {
						mapping = append(mapping, requiredPorts[port])
						delete(requiredPorts, port)
					}
				}
			}
		}
	}
	unsatisfiedPorts := len(requiredPorts)
	if unsatisfiedPorts > 0 {
		err := &PortAllocationError{
			PodId: t.Pod.ID,
		}
		for p, _ := range requiredPorts {
			err.Ports = append(err.Ports, p)
		}
		return nil, err
	}
	return mapping, nil
}

func rangeResource(name string, ports []uint64) *mesos.Resource {
	if len(ports) == 0 {
		// pod may consist of a container that doesn't expose any ports on the host
		return nil
	}
	return &mesos.Resource{
		Name:   proto.String(name),
		Type:   mesos.Value_RANGES.Enum(),
		Ranges: NewRanges(ports),
	}
}

// func NewRange(begin uint64, end uint64) *mesos.Value_Ranges {
func NewRanges(ports []uint64) *mesos.Value_Ranges {
	r := []*mesos.Value_Range{}
	for _, port := range ports {
		// this is subtle: since we're using pointers to the port we must not
		// use the loop variable, otherwise the begin and end of all the ranges
		// we construct will end up being exactly the same. so copy it to x
		x := port
		r = append(r, &mesos.Value_Range{Begin: &x, End: &x})
	}
	return &mesos.Value_Ranges{Range: r}
}

func (t *PodTask) hasAcceptedOffer() bool {
	return t.TaskInfo.TaskId != nil
}

func (t *PodTask) GetOfferId() string {
	if t.Offer == nil {
		return ""
	}
	return t.Offer.Details().Id.GetValue()
}

// Fill the TaskInfo in the PodTask, should be called during k8s scheduling,
// before binding.
func (t *PodTask) FillTaskInfo(offer PerishableOffer) error {
	if offer == nil || offer.Details() == nil {
		return fmt.Errorf("Nil offer for task %v", t)
	}
	details := offer.Details()
	if details == nil {
		return fmt.Errorf("Illegal offer for task %v: %v", t, offer)
	}
	if t.Offer != nil && t.Offer != offer {
		return fmt.Errorf("Offer assignment must be idempotent with task %v: %v", t, offer)
	}
	t.Offer = offer
	log.V(3).Infof("Recording offer(s) %v against pod %v", details.Id, t.Pod.ID)

	t.TaskInfo.TaskId = &mesos.TaskID{Value: proto.String(t.ID)}
	t.TaskInfo.SlaveId = details.GetSlaveId()
	t.TaskInfo.Resources = []*mesos.Resource{
		mesos.ScalarResource("cpus", containerCpus),
		mesos.ScalarResource("mem", containerMem),
	}
	if mapping, err := t.mapper(t, details); err != nil {
		t.ClearTaskInfo()
		return err
	} else {
		ports := []uint64{}
		for _, entry := range mapping {
			ports = append(ports, entry.offerPort)
		}
		t.ports = mapping
		if portsResource := rangeResource("ports", ports); portsResource != nil {
			t.TaskInfo.Resources = append(t.TaskInfo.Resources, portsResource)
		}
	}
	if err := t.UpdateDesiredState(&t.Pod.DesiredState.Manifest); err != nil {
		t.ClearTaskInfo()
		return err
	}
	return nil
}

func (t *PodTask) UpdateDesiredState(manifest *api.ContainerManifest) error {
	t.Pod.DesiredState.Manifest = *manifest

	// avoid potentially contaminating the desired state with specialized
	// port assignments just in case this task is re-scheduled later and
	// we need access to the original manifest

	// TODO(jdef): will this confuse some recification loop that wants to
	// ensure that desired state == running state?

	if len(t.ports) > 0 {
		m := *manifest
		manifest = &m

		// include updated port mappings in task data
		for _, entry := range t.ports {
			port := &(manifest.Containers[entry.cindex].Ports[entry.pindex])
			port.HostPort = int(entry.offerPort)
		}
	}
	if data, err := yaml.Marshal(manifest); err != nil {
		return err
	} else {
		t.TaskInfo.Data = data
	}
	return nil
}

// Clear offer-related details from the task, should be called if/when an offer
// has already been assigned to a task but for some reason is no longer valid.
func (t *PodTask) ClearTaskInfo() {
	log.V(3).Infof("Clearing offer(s) from pod %v", t.Pod.ID)
	t.Offer = nil
	t.TaskInfo.TaskId = nil
	t.TaskInfo.SlaveId = nil
	t.TaskInfo.Resources = nil
	t.TaskInfo.Data = nil
	t.ports = nil
}

func (t *PodTask) AcceptOffer(offer *mesos.Offer) bool {

	if offer == nil {
		return false
	}

	var cpus float64 = 0
	var mem float64 = 0

	for _, resource := range offer.Resources {
		if resource.GetName() == "cpus" {
			cpus = *resource.GetScalar().Value
		}

		if resource.GetName() == "mem" {
			mem = *resource.GetScalar().Value
		}
	}

	if _, err := t.mapper(t, offer); err != nil {
		log.V(2).Info(err)
		return false
	}

	if (cpus < containerCpus) || (mem < containerMem) {
		log.V(2).Infof("Not enough resources: cpus: %f mem: %f", cpus, mem)
		return false
	}

	return true
}

func newPodTask(pod *api.Pod, executor *mesos.ExecutorInfo) (*PodTask, error) {
	if pod == nil {
		return nil, fmt.Errorf("Illegal argument: pod was nil")
	}
	if executor == nil {
		return nil, fmt.Errorf("Illegal argument: executor was nil")
	}
	taskId := uuid.NewUUID().String()
	task := &PodTask{
		ID:       taskId, // pod.JSONBase.ID,
		Pod:      pod,
		TaskInfo: new(mesos.TaskInfo),
		mapper:   hostPortMappingFuncFor(pod),
	}
	task.TaskInfo.Name = proto.String("PodTask")
	task.TaskInfo.Executor = executor
	return task, nil
}

// The label "k8sm:expose=*" on the pod will determine the pod-mapping alg.
// If unset, then hostPort==0 retains default k8s behavior -- ignored, and the related
// container port remains private to the pod.
// If set, then hostPort==0 indicates that a hostPort should be selected from among the
// port resources available in the offer; applies to all the pod's containers.
func hostPortMappingFuncFor(pod *api.Pod) hostPortMappingFunc {

	// TODO(jdef): a better design might be this:
	//   k8sm:expose  := '*' | ct-port-list
	//   ct-port-list := ct-port | ct-port ',' ct-port-list
	//   ct-port      := ct-port-number | ct-port-name
	//
	// The above approach provides a short, simple syntax for exposing all wildcard
	// host ports. If more control is desired then users can list specific ct-ports
	// to expose.
	//
	filter := map[string]string{
		"k8sm:expose": "*",
	}
	selector := labels.Set(filter).AsSelector()
	if selector.Matches(labels.Set(pod.Labels)) {
		return wildcardHostPortMapping
	}
	return defaultHostPortMapping
}
