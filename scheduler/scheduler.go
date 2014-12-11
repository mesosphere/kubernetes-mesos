package scheduler

import (
	"container/ring"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	kpod "github.com/GoogleCloudPlatform/kubernetes/pkg/registry/pod"
	algorithm "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
	plugin "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/scheduler"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
)

const (
	defaultFinishedTasksSize = 1024 // size of the finished task history buffer
	defaultOfferTTL          = 5    // seconds that an offer is viable, prior to being expired
	defaultOfferLingerTTL    = 120  // seconds that an expired offer lingers in history
	defaultListenerDelay     = 1    // number of seconds between offer listener notifications
)

type stateType int

const (
	statePending stateType = iota
	stateRunning
	stateFinished
	stateUnknown
)

var (
	noSuitableOffersErr = errors.New("No suitable offers for pod/task")
)

// PodScheduleFunc implements how to schedule pods among slaves.
// We can have different implementation for different scheduling policy.
//
// The Schedule function accepts a group of slaves (each contains offers from
// that slave) and a single pod, which aligns well with the k8s scheduling
// algorithm. It returns an offerId that is acceptable for the pod, otherwise
// nil. The caller is responsible for filling in task state w/ relevant offer
// details.
//
// See the FIFOScheduleFunc for example.
type PodScheduleFunc func(r OfferRegistry, slaves map[string]*Slave, task *PodTask) (PerishableOffer, error)

// A struct that describes the slave.
type empty struct{}
type Slave struct {
	HostName string
	Offers   map[string]empty
}

func newSlave(hostName string) *Slave {
	return &Slave{
		HostName: hostName,
		Offers:   make(map[string]empty),
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
	driver      mesos.SchedulerDriver
	frameworkId *mesos.FrameworkID
	masterInfo  *mesos.MasterInfo
	registered  bool

	offers OfferRegistry

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

	manifestFactory kpod.ManifestFactory
}

// New create a new KubernetesScheduler
func New(executor *mesos.ExecutorInfo, scheduleFunc PodScheduleFunc, client *client.Client, helper tools.EtcdHelper) *KubernetesScheduler {
	var k *KubernetesScheduler
	k = &KubernetesScheduler{
		RWMutex:    new(sync.RWMutex),
		EtcdHelper: helper,
		executor:   executor,
		offers: CreateOfferRegistry(OfferRegistryConfig{
			declineOffer: func(id string) error {
				offerId := &mesos.OfferID{Value: proto.String(id)}
				return k.driver.DeclineOffer(offerId, nil)
			},
			ttl:           defaultOfferTTL * time.Second,
			lingerTtl:     defaultOfferLingerTTL * time.Second, // remember expired offers so that we can tell if a previously scheduler offer relies on one
			listenerDelay: defaultListenerDelay * time.Second,
		}),
		slaves:        make(map[string]*Slave),
		slaveIDs:      make(map[string]string),
		pendingTasks:  make(map[string]*PodTask),
		runningTasks:  make(map[string]*PodTask),
		finishedTasks: ring.New(defaultFinishedTasksSize),
		podToTask:     make(map[string]string),
		scheduleFunc:  scheduleFunc,
		client:        client,
		podQueue:      cache.NewFIFO(),
	}
	return k
}

// assume that the caller has already locked around access to task state
func (k *KubernetesScheduler) getTask(taskId string) (*PodTask, stateType) {
	if task, found := k.runningTasks[taskId]; found {
		return task, stateRunning
	}
	if task, found := k.pendingTasks[taskId]; found {
		return task, statePending
	}
	if containsTask(k.finishedTasks, taskId) {
		return nil, stateFinished
	}
	return nil, stateUnknown
}

func (k *KubernetesScheduler) Init(d mesos.SchedulerDriver, f kpod.ManifestFactory) {
	k.offers.Init()
	k.driver = d
	k.manifestFactory = f
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
	log.Infof("Scheduler reregistered with the master: %v\n", masterInfo)
	k.registered = true
}

// Disconnected is called when the scheduler loses connection to the master.
func (k *KubernetesScheduler) Disconnected(driver mesos.SchedulerDriver) {
	log.Infof("Master disconnected!\n")
	k.registered = false

	k.Lock()
	defer k.Unlock()

	// discard all cached offers to avoid unnecessary TASK_LOST updates
	k.offers.Invalidate("")
}

// ResourceOffers is called when the scheduler receives some offers from the master.
func (k *KubernetesScheduler) ResourceOffers(driver mesos.SchedulerDriver, offers []*mesos.Offer) {
	log.Infof("Received offers\n")
	log.V(2).Infof("%v\n", offers)

	k.Lock()
	defer k.Unlock()

	// Record the offers in the global offer map as well as each slave's offer map.
	k.offers.Add(offers)
	for _, offer := range offers {
		offerId := offer.GetId().GetValue()
		slaveId := offer.GetSlaveId().GetValue()

		slave, exists := k.slaves[slaveId]
		if !exists {
			k.slaves[slaveId] = newSlave(offer.GetHostname())
			slave = k.slaves[slaveId]
		}
		slave.Offers[offerId] = empty{}
		k.slaveIDs[slave.HostName] = slaveId
	}
}

// requires the caller to have locked the offers and slaves state
func (k *KubernetesScheduler) deleteOffer(oid string) {
	if offer, ok := k.offers.Get(oid); ok {
		k.offers.Delete(oid)
		if details := offer.Details(); details != nil {
			slaveId := details.GetSlaveId().GetValue()

			if slave, found := k.slaves[slaveId]; !found {
				log.Infof("No slave for id %s associated with offer id %s", slaveId, oid)
			} else {
				delete(slave.Offers, oid)
			}
		} // else, offer already expired / lingering
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
	if _, exists := k.slaves[slaveId]; !exists {
		log.Warningf("Ignore status TASK_RUNNING because the slave does not exist\n")
		return
	}
	switch task, state := k.getTask(taskId); state {
	case statePending:
		log.Infof("Received running status for pending task: '%v'", taskStatus)
		k.fillRunningPodInfo(task, taskStatus)
		k.runningTasks[taskId] = task
		delete(k.pendingTasks, taskId)
	case stateRunning:
		log.Warningf("Ignore status TASK_RUNNING because the the task is already running")
	case stateFinished:
		log.Warningf("Ignore status TASK_RUNNING because the the task is already finished")
	default:
		log.Warningf("Ignore status TASK_RUNNING (%s) because the the task is discarded", taskId)
	}
}

func (k *KubernetesScheduler) fillRunningPodInfo(task *PodTask, taskStatus *mesos.TaskStatus) {
	task.Pod.CurrentState.Status = task.Pod.DesiredState.Status
	task.Pod.CurrentState.Manifest = task.Pod.DesiredState.Manifest
	task.Pod.CurrentState.Host = task.Pod.DesiredState.Host

	if taskStatus.Data != nil {
		var target api.PodInfo
		err := json.Unmarshal(taskStatus.Data, &target)
		if err == nil {
			task.Pod.CurrentState.Info = target
			/// TODO(jdef) this is problematic using default Docker networking on a default
			/// Docker bridge -- meaning that pod IP's are not routable across the
			/// k8s-mesos cluster. For now, I've duplicated logic from k8s fillPodInfo
			netContainerInfo, ok := target["net"] // docker.Container
			if ok {
				if netContainerInfo.PodIP != "" {
					task.Pod.CurrentState.PodIP = netContainerInfo.PodIP
				} else {
					log.Warningf("No network settings: %#v", netContainerInfo)
				}
			} else {
				log.Warningf("Couldn't find network container for %s in %v", task.Pod.ID, target)
			}
		} else {
			log.Errorf("Invalid TaskStatus.Data for task '%v': %v", task.ID, err)
		}
	} else {
		log.Errorf("Missing TaskStatus.Data for task '%v'", task.ID)
	}
}

func (k *KubernetesScheduler) handleTaskFinished(taskStatus *mesos.TaskStatus) {
	taskId, slaveId := taskStatus.GetTaskId().GetValue(), taskStatus.GetSlaveId().GetValue()
	if _, exists := k.slaves[slaveId]; !exists {
		log.Warningf("Ignore status TASK_FINISHED because the slave does not exist\n")
		return
	}
	switch task, state := k.getTask(taskId); state {
	case statePending:
		panic("Pending task finished, this couldn't happen")
	case stateRunning:
		log.V(2).Infof(
			"Received finished status for running task: '%v', running/pod task queue length = %d/%d",
			taskStatus, len(k.runningTasks), len(k.podToTask))
		delete(k.podToTask, task.Pod.ID)
		k.finishedTasks.Next().Value = taskId
		delete(k.runningTasks, taskId)
	case stateFinished:
		log.Warningf("Ignore status TASK_FINISHED because the the task is already finished")
	default:
		log.Warningf("Ignore status TASK_FINISHED because the the task is not running")
	}
}

func (k *KubernetesScheduler) handleTaskFailed(taskStatus *mesos.TaskStatus) {
	log.Errorf("Task failed: '%v'", taskStatus)
	taskId := taskStatus.GetTaskId().GetValue()

	switch task, state := k.getTask(taskId); state {
	case statePending:
		delete(k.pendingTasks, taskId)
		delete(k.podToTask, task.Pod.ID)
	case stateRunning:
		delete(k.runningTasks, taskId)
		delete(k.podToTask, task.Pod.ID)
	}
}

func (k *KubernetesScheduler) handleTaskKilled(taskStatus *mesos.TaskStatus) {
	log.Errorf("Task killed: '%v'", taskStatus)
	taskId := taskStatus.GetTaskId().GetValue()

	switch task, state := k.getTask(taskId); state {
	case statePending:
		delete(k.pendingTasks, taskId)
		delete(k.podToTask, task.Pod.ID)
	case stateRunning:
		delete(k.runningTasks, taskId)
		delete(k.podToTask, task.Pod.ID)
	}
}

func (k *KubernetesScheduler) handleTaskLost(taskStatus *mesos.TaskStatus) {
	log.Errorf("Task lost: '%v'", taskStatus)
	taskId := taskStatus.GetTaskId().GetValue()

	switch task, state := k.getTask(taskId); state {
	case statePending:
		delete(k.pendingTasks, taskId)
		delete(k.podToTask, task.Pod.ID)
	case stateRunning:
		delete(k.runningTasks, taskId)
		delete(k.podToTask, task.Pod.ID)
	}
}

// FrameworkMessage is called when the scheduler receives a message from the executor.
func (k *KubernetesScheduler) FrameworkMessage(driver mesos.SchedulerDriver,
	executorId *mesos.ExecutorID, slaveId *mesos.SlaveID, message string) {
	log.Infof("Received messages from executor %v of slave %v, %v\n", executorId, slaveId, message)
}

// SlaveLost is called when some slave is lost.
func (k *KubernetesScheduler) SlaveLost(driver mesos.SchedulerDriver, slaveId *mesos.SlaveID) {
	log.Infof("Slave %v is lost\n", slaveId)

	k.Lock()
	defer k.Unlock()

	// invalidate all offers mapped to that slave
	if slave, ok := k.slaves[slaveId.GetValue()]; ok {
		for offerId := range slave.Offers {
			k.offers.Invalidate(offerId)
		}
	}

	// TODO(jdef): delete slave from our internal list?

	// unfinished tasks/pods will be dropped. use a replication controller if you want pods to
	// be restarted when slaves die.
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
			return k.doSchedule(task)
		}
	}
}

// Call ScheduleFunc and subtract some resources, returning the name of the machine the task is scheduled on
func (k *KubernetesScheduler) doSchedule(task *PodTask) (string, error) {
	offer, err := k.scheduleFunc(k.offers, k.slaves, task)
	if err != nil {
		return "", err
	}
	slaveId := offer.Details().GetSlaveId().GetValue()
	if slave, ok := k.slaves[slaveId]; !ok {
		// not much sense in Release()ing the offer here since its owner died
		offer.Release()
		k.offers.Invalidate(offer.Details().Id.GetValue())
		task.ClearTaskInfo()
		return "", fmt.Errorf("Slave disappeared (%v) while scheduling task %v", slaveId, task.ID)
	} else {
		task.FillTaskInfo(offer)
		return slave.HostName, nil
	}
}

// implementation of scheduling plugin's NextPod func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) yield() *api.Pod {
	pod := k.podQueue.Pop().(*api.Pod)
	log.V(2).Infof("About to try and schedule pod %v\n", pod.ID)
	return pod
}

// implementation of scheduling plugin's Error func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) handleSchedulingError(backoff *podBackoff, pod *api.Pod, err error) {
	log.Infof("Error scheduling %v: %v; retrying", pod.ID, err)
	backoff.gc()

	// Retry asynchronously.
	// Note that this is extremely rudimentary and we need a more real error handling path.
	go func() {
		defer util.HandleCrash()
		podId := pod.ID
		// did we error out because if non-matching offers? if so, register an offer
		// listener to be notified if/when a matching offer comes in.
		var offersAvailable <-chan empty
		if err == noSuitableOffersErr {
			offersAvailable = k.offers.Listen(podId, func(offer *mesos.Offer) bool {
				k.RLock()
				defer k.RUnlock()
				if taskId, ok := k.podToTask[podId]; ok {
					switch task, state := k.getTask(taskId); state {
					case statePending:
						return task.AcceptOffer(offer)
					}
				}
				return false
			})
		}
		backoff.wait(podId, offersAvailable)

		// Get the pod again; it may have changed/been scheduled already.
		pod = &api.Pod{}
		err := k.client.Get().Path("pods").Path(podId).Do().Into(pod)
		if err != nil {
			log.Infof("Failed to get pod %v for retry: %v; abandoning", podId, err)
			return
		}
		if pod.DesiredState.Host == "" {
			// ensure that the pod hasn't been deleted while we were trying to schedule it
			k.Lock()
			defer k.Unlock()

			if taskId, exists := k.podToTask[podId]; exists {
				if task, ok := k.pendingTasks[taskId]; ok && !task.hasAcceptedOffer() {
					// "pod" now refers to a Pod instance that is not pointed to by the PodTask, so update our records
					// TODO(jdef) not sure that this is strictly necessary since once the pod is schedule, only the ID is
					// passed around in the Pod.Registry API
					task.Pod = pod
					k.podQueue.Add(pod.ID, pod)
				} else {
					// this state shouldn't really be possible, so I'm warning if we ever see it
					log.Errorf("Scheduler detected pod no longer pending: %v, will not re-queue; possible offer leak", podId)
				}
			} else {
				log.Infof("Scheduler detected deleted pod: %v, will not re-queue", podId)
			}
		}
	}()
}

// ListPods obtains a list of pods that match selector.
func (k *KubernetesScheduler) ListPodsPredicate(ctx api.Context, filter func(*api.Pod) bool) (*api.PodList, error) {
	k.RLock()
	defer k.RUnlock()
	return k.listPods(filter)
}

// ListPods obtains a list of pods that match selector.
func (k *KubernetesScheduler) ListPods(ctx api.Context, selector labels.Selector) (*api.PodList, error) {
	log.V(2).Infof("List pods for '%v'\n", selector)
	k.RLock()
	defer k.RUnlock()
	return k.listPods(func(pod *api.Pod) bool {
		return selector.Matches(labels.Set(pod.Labels))
	})
}

// assumes that caller has already locked around scheduler state
func (k *KubernetesScheduler) listPods(filter func(*api.Pod) bool) (*api.PodList, error) {
	result := []api.Pod{}
	for _, task := range k.runningTasks {
		pod := task.Pod

		if filter(pod) {
			result = append(result, *pod)
		}
	}
	for _, task := range k.pendingTasks {
		pod := task.Pod

		if filter(pod) {
			result = append(result, *pod)
		}
	}

	// TODO(nnielsen): Wire up check in finished tasks. (jdef) not sure how many
	// finished tasks are really appropriate to return here. finished tasks do not
	// have a TTL in the finishedTasks ring and I don't think we want to return
	// hundreds of finished tasks here.

	matches := &api.PodList{Items: result}
	log.V(5).Infof("Returning pods: '%v'\n", matches)

	return matches, nil
}

// Get a specific pod. It's *very* important to return a clone of the Pod that
// we've saved because our caller will likely modify it.
func (k *KubernetesScheduler) GetPod(ctx api.Context, podId string) (*api.Pod, error) {
	log.V(2).Infof("Get pod '%s'\n", podId)

	k.RLock()
	defer k.RUnlock()

	taskId, exists := k.podToTask[podId]
	if !exists {
		return nil, fmt.Errorf("Could not resolve pod '%s' to task id", podId)
	}

	switch task, state := k.getTask(taskId); state {
	case statePending:
		log.V(5).Infof("Pending Pod '%s': %v", podId, task.Pod)
		podCopy := *task.Pod
		return &podCopy, nil
	case stateRunning:
		log.V(5).Infof("Running Pod '%s': %v", podId, task.Pod)
		podCopy := *task.Pod
		return &podCopy, nil
	case stateFinished:
		return nil, fmt.Errorf("Pod '%s' is finished", podId)
	case stateUnknown:
		return nil, fmt.Errorf("Unknown Pod %v", podId)
	default:
		return nil, fmt.Errorf("Unexpected task state %v for task %v", state, taskId)
	}
}

// Create a pod based on a specification; DOES NOT schedule it onto a specific machine,
// instead the pod is queued for scheduling.
func (k *KubernetesScheduler) CreatePod(ctx api.Context, pod *api.Pod) error {
	log.V(2).Infof("Create pod: '%v'\n", pod)
	// Set current status to "Waiting".
	pod.CurrentState.Status = api.PodWaiting
	pod.CurrentState.Host = ""
	// DesiredState.Host == "" is a signal to the scheduler that this pod needs scheduling.
	pod.DesiredState.Status = api.PodRunning
	pod.DesiredState.Host = ""

	// TODO(jdef) should we make a copy of the pod object instead of just assuming that the caller is
	// well behaved and will not change the state of the object it has given to us?
	task, err := newPodTask(pod, k.executor)
	if err != nil {
		return err
	}

	k.Lock()
	defer k.Unlock()

	if _, ok := k.podToTask[pod.ID]; ok {
		return fmt.Errorf("Pod %s already launched. Please choose a unique pod name", pod.ID)
	}

	k.podQueue.Add(pod.ID, pod)
	k.podToTask[pod.ID] = task.ID
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

	// sanity check: ensure that the task hasAcceptedOffer(), it's possible that between
	// Schedule() and now that the offer for this task was rescinded or invalidated.
	// ((we should never see this here))
	if !task.hasAcceptedOffer() {
		return fmt.Errorf("task has not accepted a valid offer, pod %v", podId)
	}

	// By this time, there is a chance that the slave is disconnected.
	offerId := task.GetOfferId()
	if offer, ok := k.offers.Get(offerId); !ok || offer.HasExpired() {
		// already rescinded or timed out or otherwise invalidated
		task.Offer.Release()
		task.ClearTaskInfo()
		return fmt.Errorf("failed prior to launchTask due to expired offer, pod %v", podId)
	}

	var err error
	if err = k.prepareTaskForLaunch(binding.Host, task); err == nil {
		log.V(2).Infof("Launching task : %v", task)
		taskList := []*mesos.TaskInfo{task.TaskInfo}
		if err = k.driver.LaunchTasks(task.Offer.Details().Id, taskList, nil); err == nil {
			// we *intentionally* do not record our binding to etcd since we're not using bindings
			// to manage pod lifecycle
			task.Pod.DesiredState.Host = binding.Host
			task.Launched = true
			k.offers.Invalidate(offerId)
			return nil
		}
	}
	task.Offer.Release()
	task.ClearTaskInfo()
	return fmt.Errorf("Failed to launch task for pod %s: %v", podId, err)
}

func (k *KubernetesScheduler) prepareTaskForLaunch(machine string, task *PodTask) error {
	// TODO(k8s): move this to a watch/rectification loop.
	manifest, err := k.manifestFactory.MakeManifest(machine, *task.Pod)
	if err != nil {
		log.V(2).Infof("Failed to generate an updated manifest")
		return err
	}

	// update the manifest here to pick up things like environment variables that
	// pod containers will use for service discovery. the kubelet-executor uses this
	// manifest to instantiate the pods and this is the last update we make before
	// firing up the pod.
	return task.UpdateDesiredState(&manifest)
}

// Update an existing pod.
func (k *KubernetesScheduler) UpdatePod(ctx api.Context, pod *api.Pod) error {
	// TODO(yifan): Need to send a special message to the slave/executor.
	// TODO(nnielsen): Pod updates not yet supported by kubelet.
	return fmt.Errorf("Not implemented: UpdatePod")
}

// Delete an existing pod.
func (k *KubernetesScheduler) DeletePod(ctx api.Context, podId string) error {
	log.V(2).Infof("Delete pod '%s'\n", podId)

	k.Lock()
	defer k.Unlock()

	// prevent the scheduler from attempting to pop this; it's also possible that
	// it's concurrently being scheduled (somewhere between pod scheduling and
	// binding) - if so, then we'll end up removing it from pendingTasks which
	// will abort Bind()ing
	k.podQueue.Delete(podId)

	taskId, exists := k.podToTask[podId]
	if !exists {
		return fmt.Errorf("Could not resolve pod '%s' to task id", podId)
	}

	// determine if the task has already been launched to mesos, if not then
	// cleanup is easier (podToTask,pendingTasks) since there's no state to sync
	var killTaskId *mesos.TaskID
	task, state := k.getTask(taskId)

	switch state {
	case statePending:
		if !task.Launched {
			// we've been invoked in between Schedule() and Bind()
			if task.hasAcceptedOffer() {
				task.Offer.Release()
				task.ClearTaskInfo()
			}
			delete(k.podToTask, podId)
			delete(k.pendingTasks, taskId)
			return nil
		}
		fallthrough
	case stateRunning:
		killTaskId = &mesos.TaskID{Value: proto.String(task.ID)}
	default:
		return fmt.Errorf("Cannot kill pod '%s': pod not found", podId)
	}
	// signal to watchers that the related pod is going down
	task.Pod.DesiredState.Host = ""
	return k.driver.KillTask(killTaskId)
}

func (k *KubernetesScheduler) WatchPods(ctx api.Context, resourceVersion string, filter func(*api.Pod) bool) (watch.Interface, error) {
	return nil, nil
}

// A FCFS scheduler.
func FCFSScheduleFunc(r OfferRegistry, slaves map[string]*Slave, task *PodTask) (PerishableOffer, error) {
	if task.hasAcceptedOffer() {
		// verify that the offer is still on the table
		offerId := task.GetOfferId()
		if offer, ok := r.Get(offerId); ok && !offer.HasExpired() {
			// skip tasks that have already have assigned offers
			return task.Offer, nil
		}
		task.Offer.Release()
		task.ClearTaskInfo()
	}

	var acceptedOffer PerishableOffer
	err := r.Walk(func(p PerishableOffer) (bool, error) {
		offer := p.Details()
		if offer == nil {
			return false, fmt.Errorf("nil offer while scheduling task %v", task.ID)
		}
		if task.AcceptOffer(offer) {
			if p.Acquire() {
				acceptedOffer = p
				log.V(3).Infof("Pod %v accepted offer %v", task.Pod.ID, offer.Id.GetValue())
				return true, nil // stop, we found an offer
			}
		}
		return false, nil // continue
	})
	if acceptedOffer != nil {
		if err != nil {
			log.Warningf("problems walking the offer registry: %v, attempting to continue", err)
		}
		return acceptedOffer, nil
	}
	if err != nil {
		log.V(2).Infof("failed to find a fit for pod: %v, err = %v", task.Pod.ID, err)
		return nil, err
	}
	log.V(2).Infof("failed to find a fit for pod: %v", task.Pod.ID)
	return nil, noSuitableOffersErr
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

	podBackoff := podBackoff{
		perPodBackoff: map[string]*backoffEntry{},
		clock:         realClock{},
	}
	return &plugin.Config{
		MinionLister: nil,
		Algorithm:    k,
		Binder:       k,
		NextPod: func() *api.Pod {
			return k.yield()
		},
		Error: func(pod *api.Pod, err error) {
			k.handleSchedulingError(&podBackoff, pod, err)
		},
	}
}
