package podtask

import (
	"fmt"
	"strings"
	"time"

	"code.google.com/p/go-uuid/uuid"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/gogo/protobuf/proto"
	log "github.com/golang/glog"
	mesos "github.com/mesos/mesos-go/mesosproto"
	mutil "github.com/mesos/mesos-go/mesosutil"
	"github.com/mesosphere/kubernetes-mesos/pkg/offers"
	annotation "github.com/mesosphere/kubernetes-mesos/pkg/scheduler/meta"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/metrics"
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
	Bound    = FlagType("bound")
	Deleted  = FlagType("deleted")
)

// A struct that describes a pod task.
type T struct {
	ID          string
	Pod         api.Pod
	Spec        Spec
	Offer       offers.Perishable // thread-safe
	State       StateType
	Flags       map[FlagType]struct{}
	CreateTime  time.Time
	UpdatedTime time.Time // time of the most recent StatusUpdate we've seen from the mesos master

	podStatus  api.PodStatus
	executor   *mesos.ExecutorInfo // readonly
	podKey     string
	launchTime time.Time
	bindTime   time.Time
	PortMapper
}

type Spec struct {
	SlaveID string
	CPU     float64
	Memory  float64
	PortMap []PortMapping
	Ports   []uint64
	Data    []byte
}

// mostly-clone this pod task. the clone will actually share the some fields:
//   - executor    // OK because it's read only
//   - Offer       // OK because it's guarantees safe concurrent access
func (t *T) Clone() *T {
	if t == nil {
		return nil
	}

	// shallow-copy
	clone := *t

	// deep copy
	(&t.Spec).copyTo(&clone.Spec)
	clone.Flags = map[FlagType]struct{}{}
	for k := range t.Flags {
		clone.Flags[k] = struct{}{}
	}
	return &clone
}

func (old *Spec) copyTo(new *Spec) {
	if len(old.PortMap) > 0 {
		new.PortMap = append(([]PortMapping)(nil), old.PortMap...)
	}
	if len(old.Ports) > 0 {
		new.Ports = append(([]uint64)(nil), old.Ports...)
	}
	if len(old.Data) > 0 {
		new.Data = append(([]byte)(nil), old.Data...)
	}
}

func (t *T) HasAcceptedOffer() bool {
	return t.Spec.SlaveID != ""
}

func (t *T) GetOfferId() string {
	if t.Offer == nil {
		return ""
	}
	return t.Offer.Details().Id.GetValue()
}

func generateTaskName(pod *api.Pod) string {
	ns := pod.Namespace
	if ns == "" {
		ns = api.NamespaceDefault
	}
	return fmt.Sprintf("%s.%s.pods", pod.Name, ns)
}

func (t *T) BuildTaskInfo() *mesos.TaskInfo {
	info := &mesos.TaskInfo{
		Name:     proto.String(generateTaskName(&t.Pod)),
		TaskId:   mutil.NewTaskID(t.ID),
		SlaveId:  mutil.NewSlaveID(t.Spec.SlaveID),
		Executor: t.executor,
		Data:     t.Spec.Data,
		Resources: []*mesos.Resource{
			mutil.NewScalarResource("cpus", t.Spec.CPU),
			mutil.NewScalarResource("mem", t.Spec.Memory),
		},
	}
	if rs := NewRanges(t.Spec.Ports...); rs.Size() > 0 {
		info.Resources = append(info.Resources, rs.resource("ports"))
	}
	return info
}

// Fill the Spec in the T, should be called during k8s scheduling,
// before binding.
func (t *T) FillFromDetails(details *mesos.Offer) error {
	if details == nil {
		//programming error
		panic("offer details are nil")
	}

	log.V(3).Infof("Recording offer(s) %v against pod %v", details.Id, t.Pod.Name)

	t.Spec = Spec{
		SlaveID: details.GetSlaveId().GetValue(),
		CPU:     containerCpus,
		Memory:  containerMem,
	}

	mapping, err := t.PortMap(NewPortRanges(details), t.Pod.Name, t.Pod.Spec.Containers...)
	if err != nil {
		t.Reset()
		return err
	}

	ports := []uint64{}
	for _, entry := range mapping {
		ports = append(ports, entry.HostPort)
	}
	t.Spec.PortMap = mapping
	t.Spec.Ports = ports

	// hostname needs of the executor needs to match that of the offer, otherwise
	// the kubelet node status checker/updater is very unhappy
	const HOSTNAME_OVERRIDE_FLAG = "--hostname_override="
	hostname := details.GetHostname() // required field, non-empty
	hostnameOverride := HOSTNAME_OVERRIDE_FLAG + hostname

	argv := t.executor.Command.Arguments
	overwrite := false
	for i, arg := range argv {
		if strings.HasPrefix(arg, HOSTNAME_OVERRIDE_FLAG) {
			overwrite = true
			argv[i] = hostnameOverride
			break
		}
	}
	if !overwrite {
		t.executor.Command.Arguments = append(argv, hostnameOverride)
	}
	return nil
}

// Clear offer-related details from the task, should be called if/when an offer
// has already been assigned to a task but for some reason is no longer valid.
func (t *T) Reset() {
	log.V(3).Infof("Clearing offer(s) from pod %v", t.Pod.Name)
	t.Offer = nil
	t.Spec = Spec{}
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
	if _, err := t.PortMap(NewPortRanges(offer), t.Pod.Name, t.Pod.Spec.Containers...); err != nil {
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
		t.launchTime = time.Now()
		queueWaitTime := t.launchTime.Sub(t.CreateTime)
		metrics.QueueWaitTime.Observe(metrics.InMicroseconds(queueWaitTime))
	}
}

func (t *T) Has(f FlagType) (exists bool) {
	_, exists = t.Flags[f]
	return
}

func New(ctx api.Context, id string, pod api.Pod, executor *mesos.ExecutorInfo) (*T, error) {
	if executor == nil {
		return nil, fmt.Errorf("illegal argument: executor was nil")
	}
	key, err := MakePodKey(ctx, pod.Name)
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = "pod." + uuid.NewUUID().String()
	}
	task := &T{
		ID:         id,
		Pod:        pod,
		State:      StatePending,
		podKey:     key,
		PortMapper: NewPortMapper(&pod),
		Flags:      make(map[FlagType]struct{}),
		executor:   proto.Clone(executor).(*mesos.ExecutorInfo),
	}
	task.CreateTime = time.Now()
	return task, nil
}

func (t *T) SaveRecoveryInfo(dict map[string]string) {
	dict[annotation.TaskIdKey] = t.ID
	dict[annotation.SlaveIdKey] = t.Spec.SlaveID
	dict[annotation.OfferIdKey] = t.Offer.Details().Id.GetValue()
	dict[annotation.ExecutorIdKey] = t.executor.ExecutorId.GetValue()
}

// reconstruct a task from metadata stashed in a pod entry. there are limited pod states that
// support reconstruction. if we expect to be able to reconstruct state but encounter errors
// in the process then those errors are returned. if the pod is in a seemingly valid state but
// otherwise does not support task reconstruction return false. if we're able to reconstruct
// state then return a reconstructed task and true.
//
// at this time task reconstruction is only supported for pods that have been annotated with
// binding metadata, which implies that they've previously been associated with a task and
// that mesos knows about it.
//
// assumes that the pod data comes from the k8s registry and reflects the desired state.
//
func RecoverFrom(pod api.Pod) (*T, bool, error) {
	// we only expect annotations if pod has been bound, which implies that it has already
	// been scheduled and launched
	if pod.Status.Host == "" && len(pod.Annotations) == 0 {
		log.V(1).Infof("skipping recovery for unbound pod %v/%v", pod.Namespace, pod.Name)
		return nil, false, nil
	}

	// only process pods that are not in a terminal state
	switch pod.Status.Phase {
	case api.PodPending, api.PodRunning, api.PodUnknown: // continue
	default:
		log.V(1).Infof("skipping recovery for terminal pod %v/%v", pod.Namespace, pod.Name)
		return nil, false, nil
	}

	ctx := api.WithNamespace(api.NewDefaultContext(), pod.Namespace)
	key, err := MakePodKey(ctx, pod.Name)
	if err != nil {
		return nil, false, err
	}

	//TODO(jdef) recover ports (and other resource requirements?) from the pod spec as well

	now := time.Now()
	t := &T{
		Pod:        pod,
		CreateTime: now,
		podKey:     key,
		State:      StatePending, // possibly running? mesos will tell us during reconciliation
		Flags:      make(map[FlagType]struct{}),
		PortMapper: NewPortMapper(&pod),
		launchTime: now,
		bindTime:   now,
	}
	var (
		offerId  string
		hostname string
	)
	for _, k := range []string{
		annotation.BindingHostKey,
		annotation.TaskIdKey,
		annotation.SlaveIdKey,
		annotation.OfferIdKey,
		annotation.ExecutorIdKey,
	} {
		v, found := pod.Annotations[k]
		if !found {
			return nil, false, fmt.Errorf("incomplete metadata: missing value for pod annotation: %v", k)
		}
		switch k {
		case annotation.BindingHostKey:
			hostname = v
		case annotation.SlaveIdKey:
			t.Spec.SlaveID = v
		case annotation.OfferIdKey:
			offerId = v
		case annotation.TaskIdKey:
			t.ID = v
		case annotation.ExecutorIdKey:
			// this is nowhere near sufficient to re-launch a task, but we really just
			// want this for tracking
			t.executor = &mesos.ExecutorInfo{ExecutorId: mutil.NewExecutorID(v)}
		}
	}
	t.Offer = offers.Expired(offerId, hostname, 0)
	t.Flags[Launched] = struct{}{}
	t.Flags[Bound] = struct{}{}
	return t, true, nil
}
