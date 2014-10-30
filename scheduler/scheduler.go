package scheduler

import (
	"container/ring"
	"encoding/json"
	"fmt"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"code.google.com/p/go-uuid/uuid"
	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	algorithm "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
	plugin "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/scheduler"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	"gopkg.in/v1/yaml"
)

var errSchedulerTimeout = fmt.Errorf("Schedule time out")

const (
	containerCpus            = 0.25
	containerMem             = 128
	defaultFinishedTasksSize = 1024
)

// PodScheduleFunc implements how to schedule
// pods among slaves. We can have different implementation
// for different scheduling policy.
//
// The Schedule function takes a group of slaves (each contains offers
// from that slave), and a group of pods.
//
// After deciding which slave to schedule the pod, it fills the task info and the
//'SelectedMachine' channel  with the host name of the slave.
// See the FIFOScheduleFunc for example.
type PodScheduleFunc func(k *KubernetesScheduler, slaves map[string]*Slave, tasks map[string]*PodTask) []*PodTask

// A struct that describes a pod task.
type PodTask struct {
	ID              string
	Pod             *api.Pod
	SelectedMachine chan string
	TaskInfo        *mesos.TaskInfo
	OfferIds        []string
	Launched        bool
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

// Fill the TaskInfo in the PodTask, should be called in PodScheduleFunc.
func (t *PodTask) FillTaskInfo(slaveId string, offer *mesos.Offer) {
	t.OfferIds = append(t.OfferIds, offer.GetId().GetValue())

	var err error

	// TODO(nnielsen): We only launch one pod per offer. We should be able to launch multiple.

	// TODO(nnielsen): Assign/aquire pod port.
	t.TaskInfo.TaskId = &mesos.TaskID{Value: proto.String(t.ID)}
	t.TaskInfo.SlaveId = &mesos.SlaveID{Value: proto.String(slaveId)}
	t.TaskInfo.Resources = []*mesos.Resource{
		mesos.ScalarResource("cpus", containerCpus),
		mesos.ScalarResource("mem", containerMem),
	}
	if ports := rangeResource("ports", t.Ports()); ports != nil {
		t.TaskInfo.Resources = append(t.TaskInfo.Resources, ports)
	}
	t.TaskInfo.Data, err = yaml.Marshal(&t.Pod.DesiredState.Manifest)
	if err != nil {
		log.Warningf("Failed to marshal the manifest")
	}
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

func (t *PodTask) AcceptOffer(slaveId string, offer *mesos.Offer) bool {
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
		log.V(2).Infof("Could not schedule pod %s: %d ports could not be allocated on slave %s", t.Pod.ID, unsatisfiedPorts, slaveId)
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
		ID:              taskId, // pod.JSONBase.ID,
		Pod:             pod,
		SelectedMachine: make(chan string, 1),
		TaskInfo:        new(mesos.TaskInfo),
		Launched:        false,
	}
	task.TaskInfo.Name = proto.String("PodTask")
	task.TaskInfo.Executor = executor
	return task, nil
}

// A struct that describes the slave.
type Slave struct {
	HostName string
	Offers   map[string]*mesos.Offer
	tasks    map[string]*PodTask
}

func newSlave(hostName string) *Slave {
	return &Slave{
		HostName: hostName,
		Offers:   make(map[string]*mesos.Offer),
		tasks:    make(map[string]*PodTask),
	}
}

// KubernetesScheduler implements:
// 1: The interfaces of the mesos scheduler.
// 2: The interface of a kubernetes scheduler.
// 3: The interfaces of a kubernetes pod registry.
type KubernetesScheduler struct {
	// We use a lock here to avoid races
	// between invoking the mesos callback
	// and the invoking the pod registry interfaces.
	*sync.RWMutex

	// easy access to etcd ops
	tools.EtcdHelper

	// Mesos context.
	executor    *mesos.ExecutorInfo
	Driver      mesos.SchedulerDriver
	frameworkId *mesos.FrameworkID
	masterInfo  *mesos.MasterInfo
	registered  bool

	// OfferID => offer.
	offers map[string]*mesos.Offer

	// SlaveID => slave.
	slaves map[string]*Slave

	// Slave's hostname => slaveID
	slaveIDs map[string]string

	// Fields for task information book keeping.
	pendingTasks  map[string]*PodTask
	runningTasks  map[string]*PodTask
	finishedTasks *ring.Ring

	podToTask map[string]string

	// The function that does scheduling.
	scheduleFunc PodScheduleFunc

	client   *client.Client
	podQueue *cache.FIFO
}

// New create a new KubernetesScheduler
func New(executor *mesos.ExecutorInfo, scheduleFunc PodScheduleFunc, client *client.Client, helper tools.EtcdHelper) *KubernetesScheduler {
	return &KubernetesScheduler{
		new(sync.RWMutex),
		helper,
		executor,
		nil,
		nil,
		nil,
		false,
		make(map[string]*mesos.Offer),
		make(map[string]*Slave),
		make(map[string]string),
		make(map[string]*PodTask),
		make(map[string]*PodTask),
		ring.New(defaultFinishedTasksSize),
		make(map[string]string),
		scheduleFunc,
		client,
		cache.NewFIFO(),
	}
}

// Registered is called when the scheduler registered with the master successfully.
func (k *KubernetesScheduler) Registered(driver mesos.SchedulerDriver,
	frameworkId *mesos.FrameworkID, masterInfo *mesos.MasterInfo) {
	k.frameworkId = frameworkId
	k.masterInfo = masterInfo
	k.registered = true
	log.Infof("Scheduler registered with the master: %v with frameworkId: %v\n", masterInfo, frameworkId)
}

// Reregistered is called when the scheduler re-registered with the master successfully.
// This happends when the master fails over.
func (k *KubernetesScheduler) Reregistered(driver mesos.SchedulerDriver, masterInfo *mesos.MasterInfo) {
	log.Infof("Scheduler reregistered with the master: %v with frameworkId: %v\n", masterInfo)
	k.registered = true
}

// Disconnected is called when the scheduler loses connection to the master.
func (k *KubernetesScheduler) Disconnected(driver mesos.SchedulerDriver) {
	log.Infof("Master disconnected!\n")
	k.registered = false
}

// ResourceOffers is called when the scheduler receives some offers from the master.
func (k *KubernetesScheduler) ResourceOffers(driver mesos.SchedulerDriver, offers []*mesos.Offer) {
	log.Infof("Received offers\n")
	log.V(2).Infof("%v\n", offers)

	k.Lock()
	defer k.Unlock()

	// Record the offers in the global offer map as well as each slave's offer map.
	for _, offer := range offers {
		offerId := offer.GetId().GetValue()
		slaveId := offer.GetSlaveId().GetValue()

		slave, exists := k.slaves[slaveId]
		if !exists {
			k.slaves[slaveId] = newSlave(offer.GetHostname())
			slave = k.slaves[slaveId]
		}
		slave.Offers[offerId] = offer
		k.offers[offerId] = offer
		k.slaveIDs[slave.HostName] = slaveId
	}
	k.doSchedule()
}

// requires the caller to have locked the offers and slaves state
func (k *KubernetesScheduler) deleteOffer(oid string) {
	slaveId := k.offers[oid].GetSlaveId().GetValue()
	delete(k.offers, oid)

	if slave, found := k.slaves[slaveId]; !found {
		log.Warningf("No slave for id %s associated with offer id %s", slaveId, oid)
	} else {
		delete(slave.Offers, oid)
	}
}

// OfferRescinded is called when the resources are recinded from the scheduler.
func (k *KubernetesScheduler) OfferRescinded(driver mesos.SchedulerDriver, offerId *mesos.OfferID) {
	log.Infof("Offer rescinded %v\n", offerId)

	k.Lock()
	defer k.Unlock()
	oid := offerId.GetValue()
	k.deleteOffer(oid)
}

// StatusUpdate is called when a status update message is sent to the scheduler.
func (k *KubernetesScheduler) StatusUpdate(driver mesos.SchedulerDriver, taskStatus *mesos.TaskStatus) {
	log.Infof("Received status update %v\n", taskStatus)

	k.Lock()
	defer k.Unlock()

	switch taskStatus.GetState() {
	case mesos.TaskState_TASK_STAGING:
		k.handleTaskStaging(taskStatus)
	case mesos.TaskState_TASK_STARTING:
		k.handleTaskStarting(taskStatus)
	case mesos.TaskState_TASK_RUNNING:
		k.handleTaskRunning(taskStatus)
	case mesos.TaskState_TASK_FINISHED:
		k.handleTaskFinished(taskStatus)
	case mesos.TaskState_TASK_FAILED:
		k.handleTaskFailed(taskStatus)
	case mesos.TaskState_TASK_KILLED:
		k.handleTaskKilled(taskStatus)
	case mesos.TaskState_TASK_LOST:
		k.handleTaskLost(taskStatus)
	}
}

func (k *KubernetesScheduler) handleTaskStaging(taskStatus *mesos.TaskStatus) {
	log.Errorf("Not implemented: task staging")
}

func (k *KubernetesScheduler) handleTaskStarting(taskStatus *mesos.TaskStatus) {
	log.Errorf("Not implemented: task starting")
}

func (k *KubernetesScheduler) handleTaskRunning(taskStatus *mesos.TaskStatus) {
	taskId, slaveId := taskStatus.GetTaskId().GetValue(), taskStatus.GetSlaveId().GetValue()
	slave, exists := k.slaves[slaveId]
	if !exists {
		log.Warningf("Ignore status TASK_RUNNING because the slave does not exist\n")
		return
	}
	task, exists := k.pendingTasks[taskId]
	if !exists {
		log.Warningf("Ignore status TASK_RUNNING (%s) because the the task is discarded: '%v'", taskId, k.pendingTasks)
		return
	}
	if _, exists = k.runningTasks[taskId]; exists {
		log.Warningf("Ignore status TASK_RUNNING because the the task is already running")
		return
	}
	if containsTask(k.finishedTasks, taskId) {
		log.Warningf("Ignore status TASK_RUNNING because the the task is already finished")
		return
	}

	log.Infof("Received running status: '%v'", taskStatus)

	task.Pod.CurrentState.Status = task.Pod.DesiredState.Status
	task.Pod.CurrentState.Manifest = task.Pod.DesiredState.Manifest
	task.Pod.CurrentState.Host = task.Pod.DesiredState.Host
	if iplist, err := net.LookupIP(task.Pod.CurrentState.Host); err != nil {
		log.Warningf("Failed to resolve HostIP from CurrentState.Host '%v': %v", task.Pod.CurrentState.Host, err)
	} else {
		task.Pod.CurrentState.HostIP = iplist[0].String()
		log.V(2).Infof("Setting HostIP to '%v'", task.Pod.CurrentState.HostIP)
	}

	if taskStatus.Data != nil {
		var target api.PodInfo
		err := json.Unmarshal(taskStatus.Data, &target)
		if err == nil {
			task.Pod.CurrentState.Info = target
			/// XXX this is problematic using default Docker networking on a default
			/// Docker bridge -- meaning that pod IP's are not routable across the
			/// k8s-mesos cluster. For now, I've duplicated logic from k8s fillPodInfo
			netContainerInfo, ok := target["net"] // docker.Container
			if ok {
				if netContainerInfo.NetworkSettings != nil {
					task.Pod.CurrentState.PodIP = netContainerInfo.NetworkSettings.IPAddress
				} else {
					log.Warningf("No network settings: %#v", netContainerInfo)
				}
			} else {
				log.Warningf("Couldn't find network container for %s in %v", task.Pod.ID, target)
			}
		}
	} else {
		log.Warningf("Missing Data for task '%v'", taskId)
	}

	k.runningTasks[taskId] = task
	slave.tasks[taskId] = task
	delete(k.pendingTasks, taskId)
}

func (k *KubernetesScheduler) handleTaskFinished(taskStatus *mesos.TaskStatus) {
	taskId, slaveId := taskStatus.GetTaskId().GetValue(), taskStatus.GetSlaveId().GetValue()
	slave, exists := k.slaves[slaveId]
	if !exists {
		log.Warningf("Ignore status TASK_FINISHED because the slave does not exist\n")
		return
	}
	if _, exists := k.pendingTasks[taskId]; exists {
		panic("Pending task finished, this couldn't happen")
	}
	if _, exists := k.runningTasks[taskId]; exists {
		log.Warningf("Ignore status TASK_FINISHED because the the task is not running")
		return
	}
	if containsTask(k.finishedTasks, taskId) {
		log.Warningf("Ignore status TASK_FINISHED because the the task is already finished")
		return
	}

	task := k.runningTasks[taskId]
	_, exists = k.podToTask[task.ID]
	if exists {
		delete(k.podToTask, task.ID)
	}

	k.finishedTasks.Next().Value = taskId
	delete(k.runningTasks, taskId)
	delete(slave.tasks, taskId)
}

func (k *KubernetesScheduler) handleTaskFailed(taskStatus *mesos.TaskStatus) {
	log.Errorf("Task failed: '%v'", taskStatus)

	taskId := taskStatus.GetTaskId().GetValue()

	podId := ""
	if _, exists := k.pendingTasks[taskId]; exists {
		task := k.pendingTasks[taskId]
		podId = task.Pod.ID
		delete(k.pendingTasks, taskId)
	}
	if _, exists := k.runningTasks[taskId]; exists {
		task := k.runningTasks[taskId]
		podId = task.Pod.ID
		delete(k.runningTasks, taskId)
	}

	if podId != "" {
		_, exists := k.podToTask[podId]
		if exists {
			delete(k.podToTask, podId)
		}
	}
	// TODO (jdefelice) delete from slave.tasks?
}

func (k *KubernetesScheduler) handleTaskKilled(taskStatus *mesos.TaskStatus) {
	log.Errorf("Task killed: '%v'", taskStatus)
	taskId := taskStatus.GetTaskId().GetValue()

	podId := ""
	if _, exists := k.pendingTasks[taskId]; exists {
		task := k.pendingTasks[taskId]
		podId = task.Pod.ID
		delete(k.pendingTasks, taskId)
	}
	if _, exists := k.runningTasks[taskId]; exists {
		task := k.runningTasks[taskId]
		podId = task.Pod.ID
		delete(k.runningTasks, taskId)
	}

	if podId != "" {
		log.V(2).Infof("Deleting pod-task mapping: %s", podId)
		_, exists := k.podToTask[podId]
		if exists {
			delete(k.podToTask, podId)
		}
	}
	// TODO (jdefelice) delete from slave.tasks?
}

func (k *KubernetesScheduler) handleTaskLost(taskStatus *mesos.TaskStatus) {
	log.Errorf("Task lost: '%v'", taskStatus)
	taskId := taskStatus.GetTaskId().GetValue()

	podId := ""
	if _, exists := k.pendingTasks[taskId]; exists {
		task := k.pendingTasks[taskId]
		podId = task.Pod.ID
		delete(k.pendingTasks, taskId)
	}
	if _, exists := k.runningTasks[taskId]; exists {
		task := k.runningTasks[taskId]
		podId = task.Pod.ID
		delete(k.runningTasks, taskId)
	}

	if podId != "" {
		_, exists := k.podToTask[podId]
		if exists {
			delete(k.podToTask, podId)
		}
	}
	// TODO (jdefelice) delete from slave.tasks?
}

// FrameworkMessage is called when the scheduler receives a message from the executor.
func (k *KubernetesScheduler) FrameworkMessage(driver mesos.SchedulerDriver,
	executorId *mesos.ExecutorID, slaveId *mesos.SlaveID, message string) {
	log.Infof("Received messages from executor %v of slave %v, %v\n", executorId, slaveId, message)
}

// SlaveLost is called when some slave is lost.
func (k *KubernetesScheduler) SlaveLost(driver mesos.SchedulerDriver, slaveId *mesos.SlaveID) {
	log.Infof("Slave %v is lost\n", slaveId)
	// TODO(yifan): Restart any unfinished tasks on that slave.
}

// ExecutorLost is called when some executor is lost.
func (k *KubernetesScheduler) ExecutorLost(driver mesos.SchedulerDriver,
	executorId *mesos.ExecutorID, slaveId *mesos.SlaveID, status int) {
	log.Infof("Executor %v of slave %v is lost, status: %v\n", executorId, slaveId, status)
	// TODO(yifan): Restart any unfinished tasks of the executor.
}

// Error is called when there is some error.
func (k *KubernetesScheduler) Error(driver mesos.SchedulerDriver, message string) {
	log.Errorf("Scheduler error: %v\n", message)
}

// Schedule implements the Scheduler interface of the Kubernetes.
// It returns the selectedMachine's name and error (if there's any).
func (k *KubernetesScheduler) Schedule(pod api.Pod, unused algorithm.MinionLister) (string, error) {
	log.Infof("Try to schedule pod %v\n", pod.ID)

	k.Lock()
	defer k.Unlock()

	if taskID, ok := k.podToTask[pod.ID]; !ok {
		return "", fmt.Errorf("Pod %s cannot be resolved to a task", pod.ID)
	} else {
		if task, found := k.pendingTasks[taskID]; !found {
			return "", fmt.Errorf("Task %s is not pending, nothing to schedule", taskID)
		} else {
			k.doSchedule()
			select {
			case machine := <-task.SelectedMachine:
				return machine, nil
			case <-time.After(time.Second * 10):
				// XXX don't hard code this, use something configurable
				return "", errSchedulerTimeout
			}
		}
	}
}

// Call ScheduleFunc and subtract some resources.
func (k *KubernetesScheduler) doSchedule() {
	if tasks := k.scheduleFunc(k, k.slaves, k.pendingTasks); tasks != nil {
		// Subtract offers.
		for _, task := range tasks {
			for _, offerId := range task.OfferIds {
				k.deleteOffer(offerId)
			}
		}
	}
}

// implementation of scheduling plugin's NextPod func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) yield() *api.Pod {
	pod := k.podQueue.Pop().(*api.Pod)
	// TODO: Remove or reduce verbosity by sep 6th, 2014. Leave until then to
	// make it easy to find scheduling problems.
	log.Infof("About to try and schedule pod %v\n", pod.ID)
	return pod
}

// implementation of scheduling plugin's Error func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) handleSchedulingError(pod *api.Pod, err error) {
	log.Errorf("Error scheduling %v: %v; retrying", pod.ID, err)

	// Retry asynchronously.
	// Note that this is extremely rudimentary and we need a more real error handling path.
	go func() {
		defer util.HandleCrash()
		podId := pod.ID
		// Get the pod again; it may have changed/been scheduled already.
		pod = &api.Pod{}
		err := k.client.Get().Path("pods").Path(podId).Do().Into(pod)
		if err != nil {
			log.Errorf("Error getting pod %v for retry: %v; abandoning", podId, err)
			return
		}
		if pod.DesiredState.Host == "" {
			// ensure that the pod hasn't been deleted while we were trying to schedule it
			k.Lock()
			defer k.Unlock()

			if taskId, exists := k.podToTask[podId]; exists {
				if task, ok := k.pendingTasks[taskId]; ok {
					// "pod" now refers to a Pod instance that is not pointed to by the PodTask, so update our records
					task.Pod = pod
					k.podQueue.Add(pod.ID, pod)
				} else {
					// this state shouldn't really be possible, so I'm warning if we ever see it
					log.Warningf("Scheduler detected pod no longer pending: %v, will not re-queue", podId)
				}
			} else {
				log.Infof("Scheduler detected deleted pod: %v, will not re-queue", podId)
			}
		}
	}()
}

// ListPods obtains a list of pods that match selector.
func (k *KubernetesScheduler) ListPods(selector labels.Selector) (*api.PodList, error) {
	log.V(2).Infof("List pods for '%v'\n", selector)

	k.RLock()
	defer k.RUnlock()

	var result []api.Pod
	for _, task := range k.runningTasks {
		pod := task.Pod

		var l labels.Set = pod.Labels
		if selector.Matches(l) || selector.Empty() {
			result = append(result, *pod)
		}
	}

	// TODO(nnielsen): Refactor tasks append for the three lists.
	for _, task := range k.pendingTasks {
		pod := task.Pod

		var l labels.Set = pod.Labels
		if selector.Matches(l) || selector.Empty() {
			result = append(result, *pod)
		}
	}

	// TODO(nnielsen): Wire up check in finished tasks

	matches := &api.PodList{Items: result}
	log.V(2).Infof("Returning pods: '%v'\n", matches)

	return matches, nil
}

// Get a specific pod.
func (k *KubernetesScheduler) GetPod(podId string) (*api.Pod, error) {
	log.V(2).Infof("Get pod '%s'\n", podId)

	debug.PrintStack()

	k.RLock()
	defer k.RUnlock()

	taskId, exists := k.podToTask[podId]
	if !exists {
		return nil, fmt.Errorf("Could not resolve pod '%s' to task id", podId)
	}

	if task, exists := k.pendingTasks[taskId]; exists {
		// return nil, fmt.Errorf("Pod '%s' is still pending", podId)
		log.V(2).Infof("Pending Pod '%s': %v", podId, task.Pod)
		return task.Pod, nil
	}
	if containsTask(k.finishedTasks, taskId) {
		return nil, fmt.Errorf("Pod '%s' is finished", podId)
	}
	if task, exists := k.runningTasks[taskId]; exists {
		log.V(2).Infof("Running Pod '%s': %v", podId, task.Pod)
		return task.Pod, nil
	}
	return nil, fmt.Errorf("Unknown Pod %v", podId)
}

// Create a pod based on a specification; DOES NOT schedule it onto a specific machine,
// instead the pod is queued for scheduling.
func (k *KubernetesScheduler) CreatePod(pod *api.Pod) error {
	log.V(2).Infof("Create pod: '%v'\n", pod)
	// Set current status to "Waiting".
	pod.CurrentState.Status = api.PodWaiting
	pod.CurrentState.Host = ""
	// DesiredState.Host == "" is a signal to the scheduler that this pod needs scheduling.
	pod.DesiredState.Status = api.PodRunning
	pod.DesiredState.Host = ""

	task, err := newPodTask(pod, k.executor)
	if err != nil {
		return err
	}

	k.Lock()
	defer k.Unlock()

	if _, ok := k.podToTask[pod.ID]; ok {
		return fmt.Errorf("Pod %s already launched. Please choose a unique pod name", pod.JSONBase.ID)
	}

	k.podQueue.Add(pod.ID, pod)
	k.podToTask[pod.JSONBase.ID] = task.ID
	k.pendingTasks[task.ID] = task

	return nil
}

// implements binding.Registry, launches the pod-associated-task in mesos
func (k *KubernetesScheduler) Bind(binding *api.Binding) error {
	k.Lock()
	defer k.Unlock()

	podId := binding.PodID
	taskId, exists := k.podToTask[podId]
	if !exists {
		return fmt.Errorf("Could not resolve pod '%s' to task id", podId)
	}

	task, exists := k.pendingTasks[taskId]
	if !exists {
		return fmt.Errorf("Pod Task does not exist %v\n", taskId)
	}

	// TODO(yifan): By this time, there is a chance that the slave is disconnected.
	log.V(2).Infof("Launching task : %v", task)
	offerId := &mesos.OfferID{Value: proto.String(task.OfferIds[0])}
	if err := k.Driver.LaunchTasks(offerId, []*mesos.TaskInfo{task.TaskInfo}, nil); err != nil {
		// TODO(jdefelice): should we attempt to reschedule the pod here?
		return fmt.Errorf("Failed to launch task for pod %s: %v", podId, err)
	}
	task.Pod.DesiredState.Host = binding.Host
	task.Launched = true

	// we *intentionally* do not record our binding to etcd since we're not using bindings
	// to manage pod lifecycle
	return nil
}

// Update an existing pod.
func (k *KubernetesScheduler) UpdatePod(pod *api.Pod) error {
	// TODO(yifan): Need to send a special message to the slave/executor.
	// TODO(nnielsen): Pod updates not yet supported by kubelet.
	return fmt.Errorf("Not implemented: UpdatePod")
}

// Delete an existing pod.
func (k *KubernetesScheduler) DeletePod(podId string) error {
	log.V(2).Infof("Delete pod '%s'\n", podId)

	k.Lock()
	defer k.Unlock()

	// TODO set pod.DesiredState.Host=""

	// prevent the scheduler from attempting to pop this; it's also possible that
	// it's concurrently being scheduled (somewhere between pod scheduling and
	// binding)
	k.podQueue.Delete(podId)

	taskId, exists := k.podToTask[podId]
	if !exists {
		return fmt.Errorf("Could not resolve pod '%s' to task id", podId)
	}

	// determine if the task has already been launched to mesos, if not then
	// cleanup is easier (podToTask,pendingTasks) since there's no state to sync

	if task, exists := k.runningTasks[taskId]; exists {
		taskId := &mesos.TaskID{Value: proto.String(task.ID)}
		return k.Driver.KillTask(taskId)
	}

	if task, exists := k.pendingTasks[taskId]; exists {
		if !task.Launched {
			delete(k.podToTask, podId)
			delete(k.pendingTasks, taskId)
			return nil
		}
		taskId := &mesos.TaskID{Value: proto.String(task.ID)}
		return k.Driver.KillTask(taskId)
	}

	return fmt.Errorf("Cannot kill pod '%s': pod not found", podId)
}

func (k *KubernetesScheduler) WatchPods(resourceVersion uint64, filter func(*api.Pod) bool) (watch.Interface, error) {
	return nil, nil
}

// A FCFS scheduler.
func FCFSScheduleFunc(k *KubernetesScheduler, slaves map[string]*Slave, tasks map[string]*PodTask) []*PodTask {
	for _, task := range tasks {
		if task.TaskInfo.TaskId != nil {
			// skip tasks that have already made it through FillTaskInfo
			continue
		}
		for slaveId, slave := range slaves {
			for _, offer := range slave.Offers {
				if !task.AcceptOffer(slaveId, offer) {
					log.V(2).Infof("Declining offer %v", offer)
					if err := k.Driver.DeclineOffer(offer.Id, nil); err != nil {
						log.Warningf("Failed to decline offer %v", offer.Id)
					}
					k.deleteOffer(offer.Id.GetValue())
					continue
				}

				// Fill the task info.
				task.FillTaskInfo(slaveId, offer)
				task.SelectedMachine <- slave.HostName

				// TODO(nnielsen): Schedule multiple pods.
				// Just schedule one task for now.
				return []*PodTask{task}
			}
		}
	}
	return nil
}

func containsHostName(machines []string, machine string) bool {
	for _, m := range machines {
		if m == machine {
			return true
		}
	}
	return false
}

func containsTask(finishedTasks *ring.Ring, taskId string) bool {
	for i := 0; i < defaultFinishedTasksSize; i++ {
		value := finishedTasks.Next().Value
		if value == nil {
			continue
		}
		if value.(string) == taskId {
			return true
		}
	}
	return false
}

// Create creates a scheduler and all support functions.
func (k *KubernetesScheduler) NewPluginConfig() *plugin.Config {

	return &plugin.Config{
		MinionLister: nil,
		Algorithm:    k,
		Binder:       k,
		NextPod: func() *api.Pod {
			return k.yield()
		},
		Error: func(pod *api.Pod, err error) {
			k.handleSchedulingError(pod, err)
		},
	}
}
