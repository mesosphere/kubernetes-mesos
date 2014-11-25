package scheduler

import (
	"fmt"

	"code.google.com/p/go-uuid/uuid"
	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
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
	r := make([]*mesos.Value_Range, 0)
	for _, port := range ports {
		r = append(r, &mesos.Value_Range{Begin: &port, End: &port})
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
	if ports := rangeResource("ports", t.Ports()); ports != nil {
		t.TaskInfo.Resources = append(t.TaskInfo.Resources, ports)
	}
	var err error
	if t.TaskInfo.Data, err = yaml.Marshal(&t.Pod.DesiredState.Manifest); err != nil {
		return err
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
}

func (t *PodTask) Ports() []uint64 {
	ports := make([]uint64, 0)
	for _, container := range t.Pod.DesiredState.Manifest.Containers {
		// strip all port==0 from this array; k8s already knows what to do with zero-
		// ports (it does not create 'port bindings' on the minion-host); we need to
		// remove the wildcards from this array since they don't consume host resources
		for _, port := range container.Ports {
			// HostPort is int, not uint64.
			if port.HostPort != 0 {
				ports = append(ports, uint64(port.HostPort))
			}
		}
	}

	return ports
}

func (t *PodTask) AcceptOffer(offer *mesos.Offer) bool {
	var cpus float64 = 0
	var mem float64 = 0

	// Mimic set type
	requiredPorts := make(map[uint64]struct{})
	for _, port := range t.Ports() {
		requiredPorts[port] = struct{}{}
	}

	for _, resource := range offer.Resources {
		if resource.GetName() == "cpus" {
			cpus = *resource.GetScalar().Value
		}

		if resource.GetName() == "mem" {
			mem = *resource.GetScalar().Value
		}

		if resource.GetName() == "ports" {
			for _, r := range (*resource).GetRanges().Range {
				bp := r.GetBegin()
				ep := r.GetEnd()

				for port, _ := range requiredPorts {
					log.V(2).Infof("Evaluating port range {%d:%d} %d", bp, ep, port)

					if (bp <= port) && (port <= ep) {
						delete(requiredPorts, port)
					}
				}
			}
		}
	}

	unsatisfiedPorts := len(requiredPorts)
	if unsatisfiedPorts > 0 {
		log.V(2).Infof("Could not schedule pod %s: %d ports could not be allocated", t.Pod.ID, unsatisfiedPorts)
		return false
	}

	if (cpus < containerCpus) || (mem < containerMem) {
		log.V(2).Infof("Not enough resources: cpus: %f mem: %f", cpus, mem)
		return false
	}

	return true
}

func newPodTask(pod *api.Pod, executor *mesos.ExecutorInfo) (*PodTask, error) {
	taskId := uuid.NewUUID().String()
	task := &PodTask{
		ID:       taskId, // pod.JSONBase.ID,
		Pod:      pod,
		TaskInfo: new(mesos.TaskInfo),
		Launched: false,
	}
	task.TaskInfo.Name = proto.String("PodTask")
	task.TaskInfo.Executor = executor
	return task, nil
}
