package podtask

import (
	"fmt"
	"time"

	"code.google.com/p/go-uuid/uuid"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	log "github.com/golang/glog"
	mesos "github.com/mesos/mesos-go/mesosproto"
	mutil "github.com/mesos/mesos-go/mesosutil"
	"github.com/mesosphere/kubernetes-mesos/pkg/offers"
)

const (
	containerCpus = 0.25 // initial CPU allocated for executor
	containerMem  = 64   // initial MB of memory allocated for executor
)

type StateType int

const (
	StatePending StateType = iota
	StateRunning
	StateFinished
	StateUnknown
)

type FlagType string

const (
	Launched = FlagType("launched")
	Deleted  = FlagType("deleted")
)

// A struct that describes a pod task.
type T struct {
	ID          string
	Pod         *api.Pod
	TaskInfo    *mesos.TaskInfo
	Offer       offers.Perishable
	State       StateType
	Ports       []HostPortMapping
	Flags       map[FlagType]struct{}
	podKey      string
	CreateTime  time.Time
	UpdatedTime time.Time // time of the most recent StatusUpdate we've seen from the mesos master
	launchTime  time.Time
	bindTime    time.Time
	mapper      HostPortMappingFunc
}

func (t *T) HasAcceptedOffer() bool {
	return t.TaskInfo != nil && t.TaskInfo.TaskId != nil
}

func (t *T) GetOfferId() string {
	if t.Offer == nil {
		return ""
	}
	return t.Offer.Details().Id.GetValue()
}

// Fill the TaskInfo in the T, should be called during k8s scheduling,
// before binding.
func (t *T) FillFromDetails(details *mesos.Offer) error {
	if details == nil {
		//programming error
		panic("offer details are nil")
	}

	log.V(3).Infof("Recording offer(s) %v against pod %v", details.Id, t.Pod.Name)

	t.TaskInfo.TaskId = mutil.NewTaskID(t.ID)
	t.TaskInfo.SlaveId = details.GetSlaveId()
	t.TaskInfo.Resources = []*mesos.Resource{
		mutil.NewScalarResource("cpus", containerCpus),
		mutil.NewScalarResource("mem", containerMem),
	}
	if mapping, err := t.mapper(t, details); err != nil {
		t.ClearTaskInfo()
		return err
	} else {
		ports := []uint64{}
		for _, entry := range mapping {
			ports = append(ports, entry.OfferPort)
		}
		t.Ports = mapping
		if portsResource := rangeResource("ports", ports); portsResource != nil {
			t.TaskInfo.Resources = append(t.TaskInfo.Resources, portsResource)
		}
	}
	return nil
}

// Clear offer-related details from the task, should be called if/when an offer
// has already been assigned to a task but for some reason is no longer valid.
func (t *T) ClearTaskInfo() {
	log.V(3).Infof("Clearing offer(s) from pod %v", t.Pod.Name)
	t.Offer = nil
	t.TaskInfo.TaskId = nil
	t.TaskInfo.SlaveId = nil
	t.TaskInfo.Resources = nil
	t.TaskInfo.Data = nil
	t.Ports = nil
}

func (t *T) AcceptOffer(offer *mesos.Offer) bool {
	if offer == nil {
		return false
	}
	var (
		cpus float64 = 0
		mem  float64 = 0
	)
	for _, resource := range offer.Resources {
		if resource.GetName() == "cpus" {
			cpus = *resource.GetScalar().Value
		}

		if resource.GetName() == "mem" {
			mem = *resource.GetScalar().Value
		}
	}
	if _, err := t.mapper(t, offer); err != nil {
		log.V(3).Info(err)
		return false
	}
	if (cpus < containerCpus) || (mem < containerMem) {
		log.V(3).Infof("not enough resources: cpus: %f mem: %f", cpus, mem)
		return false
	}
	return true
}

func (t *T) Set(f FlagType) {
	t.Flags[f] = struct{}{}
	if Launched == f {
		//TODO(jdef) properly emit metric, or event type instead of just logging
		t.launchTime = time.Now()
		log.V(1).Infof("metric time_to_launch %v task %v pod %v", t.launchTime.Sub(t.CreateTime), t.ID, t.Pod.Name)
	}
}

func (t *T) Has(f FlagType) (exists bool) {
	_, exists = t.Flags[f]
	return
}

// create a duplicate task, one that refers to the same pod specification and
// executor as the current task. all other state is reset to "factory settings"
// (as if returned from New())
func (t *T) dup() (*T, error) {
	ctx := api.WithNamespace(api.NewDefaultContext(), t.Pod.Namespace)
	return New(ctx, t.Pod, t.TaskInfo.Executor)
}

func New(ctx api.Context, pod *api.Pod, executor *mesos.ExecutorInfo) (*T, error) {
	if pod == nil {
		return nil, fmt.Errorf("illegal argument: pod was nil")
	}
	if executor == nil {
		return nil, fmt.Errorf("illegal argument: executor was nil")
	}
	key, err := MakePodKey(ctx, pod.Name)
	if err != nil {
		return nil, err
	}
	taskId := uuid.NewUUID().String()
	task := &T{
		ID:       taskId,
		Pod:      pod,
		TaskInfo: newTaskInfo("Pod"),
		State:    StatePending,
		podKey:   key,
		mapper:   defaultHostPortMapping,
		Flags:    make(map[FlagType]struct{}),
	}
	task.TaskInfo.Executor = executor
	task.CreateTime = time.Now()
	return task, nil
}

type HostPortMapping struct {
	ContainerIdx int // index of the container in the pod spec
	PortIdx      int // index of the port in a container's port spec
	OfferPort    uint64
}

// abstracts the way that host ports are mapped to pod container ports
type HostPortMappingFunc func(t *T, offer *mesos.Offer) ([]HostPortMapping, error)

type PortAllocationError struct {
	PodId string
	Ports []uint64
}

func (err *PortAllocationError) Error() string {
	return fmt.Sprintf("Could not schedule pod %s: %d port(s) could not be allocated", err.PodId, len(err.Ports))
}

type DuplicateHostPortError struct {
	m1, m2 HostPortMapping
}

func (err *DuplicateHostPortError) Error() string {
	return fmt.Sprintf(
		"Host port %d is specified for container %d, pod %d and container %d, pod %d",
		err.m1.OfferPort, err.m1.ContainerIdx, err.m1.PortIdx, err.m2.ContainerIdx, err.m2.PortIdx)
}

// default k8s host port mapping implementation: hostPort == 0 means containerPort remains pod-private
func defaultHostPortMapping(t *T, offer *mesos.Offer) ([]HostPortMapping, error) {
	requiredPorts := make(map[uint64]HostPortMapping)
	mapping := []HostPortMapping{}
	if t.Pod == nil {
		// programming error
		panic("task.Pod is nil")
	}
	for i, container := range t.Pod.Spec.Containers {
		// strip all port==0 from this array; k8s already knows what to do with zero-
		// ports (it does not create 'port bindings' on the minion-host); we need to
		// remove the wildcards from this array since they don't consume host resources
		for pi, port := range container.Ports {
			if port.HostPort == 0 {
				continue // ignore
			}
			m := HostPortMapping{
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
	for _, resource := range offer.Resources {
		if resource.GetName() == "ports" {
			for _, r := range (*resource).GetRanges().Range {
				bp := r.GetBegin()
				ep := r.GetEnd()
				for port, _ := range requiredPorts {
					log.V(3).Infof("evaluating port range {%d:%d} %d", bp, ep, port)
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
			PodId: t.Pod.Name,
		}
		for p, _ := range requiredPorts {
			err.Ports = append(err.Ports, p)
		}
		return nil, err
	}
	return mapping, nil
}
