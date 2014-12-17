package scheduler

import (
	"container/ring"
	"encoding/json"
	"sync"
	"time"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	kpod "github.com/GoogleCloudPlatform/kubernetes/pkg/registry/pod"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
)

const (
	defaultFinishedTasksSize = 1024 // size of the finished task history buffer
	defaultOfferTTL          = 5    // seconds that an offer is viable, prior to being expired
	defaultOfferLingerTTL    = 120  // seconds that an expired offer lingers in history
	defaultListenerDelay     = 1    // number of seconds between offer listener notifications
)

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
// 1: A mesos scheduler.
// 2: A kubernetes scheduler plugin.
// 3: A kubernetes pod.Registry.
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

	boundPodFactory kpod.BoundPodFactory
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

func (k *KubernetesScheduler) Init(d mesos.SchedulerDriver, f kpod.BoundPodFactory) {
	k.offers.Init()
	k.driver = d
	k.boundPodFactory = f
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
	task.Pod.Status.Phase = api.PodRunning
	if taskStatus.Data != nil {
		var info api.PodInfo
		err := json.Unmarshal(taskStatus.Data, &info)
		if err == nil {
			task.Pod.Status.Info = info
			/// TODO(jdef) this is problematic using default Docker networking on a default
			/// Docker bridge -- meaning that pod IP's are not routable across the
			/// k8s-mesos cluster. For now, I've duplicated logic from k8s fillPodInfo
			netContainerInfo, ok := info["net"] // docker.Container
			if ok {
				if netContainerInfo.PodIP != "" {
					task.Pod.Status.PodIP = netContainerInfo.PodIP
				} else {
					log.Warningf("No network settings: %#v", netContainerInfo)
				}
			} else {
				log.Warningf("Couldn't find network container for %s in %v", task.podKey, info)
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
		delete(k.podToTask, task.podKey)
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
		delete(k.podToTask, task.podKey)
	case stateRunning:
		delete(k.runningTasks, taskId)
		delete(k.podToTask, task.podKey)
	}
}

func (k *KubernetesScheduler) handleTaskKilled(taskStatus *mesos.TaskStatus) {
	log.Errorf("Task killed: '%v'", taskStatus)
	taskId := taskStatus.GetTaskId().GetValue()

	switch task, state := k.getTask(taskId); state {
	case statePending:
		delete(k.pendingTasks, taskId)
		delete(k.podToTask, task.podKey)
	case stateRunning:
		delete(k.runningTasks, taskId)
		delete(k.podToTask, task.podKey)
	}
}

func (k *KubernetesScheduler) handleTaskLost(taskStatus *mesos.TaskStatus) {
	log.Errorf("Task lost: '%v'", taskStatus)
	taskId := taskStatus.GetTaskId().GetValue()

	switch task, state := k.getTask(taskId); state {
	case statePending:
		delete(k.pendingTasks, taskId)
		delete(k.podToTask, task.podKey)
	case stateRunning:
		delete(k.runningTasks, taskId)
		delete(k.podToTask, task.podKey)
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
