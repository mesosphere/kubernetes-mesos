package scheduler

import (
	"container/ring"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	log "github.com/golang/glog"
	mesos "github.com/mesos/mesos-go/mesosproto"
	mutil "github.com/mesos/mesos-go/mesosutil"
	bindings "github.com/mesos/mesos-go/scheduler"
	"github.com/mesosphere/kubernetes-mesos/pkg/executor/messages"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/meta"
)

const (
	defaultFinishedTasksSize = 1024 // size of the finished task history buffer
	defaultOfferTTL          = 5    // seconds that an offer is viable, prior to being expired
	defaultOfferLingerTTL    = 120  // seconds that an expired offer lingers in history
	defaultListenerDelay     = 1    // number of seconds between offer listener notifications
	defaultUpdatesBacklog    = 2048 // size of the pod updates channel
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

type PluginInterface interface {
	// the apiserver may have a different state for the pod than we do
	// so reconcile our records, but only for this one pod
	reconcilePod(api.Pod)

	// execute the Scheduling plugin, should start a go routine and return immediately
	Run()
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

	// Mesos context.
	executor    *mesos.ExecutorInfo
	driver      bindings.SchedulerDriver
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

	client     *client.Client
	plugin     PluginInterface
	etcdClient tools.EtcdClient
}

// New create a new KubernetesScheduler
func New(executor *mesos.ExecutorInfo, scheduleFunc PodScheduleFunc, client *client.Client, etcdClient tools.EtcdClient) *KubernetesScheduler {
	var k *KubernetesScheduler
	k = &KubernetesScheduler{
		RWMutex:  new(sync.RWMutex),
		executor: executor,
		offers: CreateOfferRegistry(OfferRegistryConfig{
			declineOffer: func(id string) error {
				offerId := newOfferID(id)
				filters := &mesos.Filters{}
				_, err := k.driver.DeclineOffer(offerId, filters)
				return err
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
		etcdClient:    etcdClient,
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

func (k *KubernetesScheduler) Init(d bindings.SchedulerDriver, pl PluginInterface) {
	k.driver = d
	k.plugin = pl
	k.offers.Init()
}

// Registered is called when the scheduler registered with the master successfully.
func (k *KubernetesScheduler) Registered(driver bindings.SchedulerDriver,
	frameworkId *mesos.FrameworkID, masterInfo *mesos.MasterInfo) {
	k.frameworkId = frameworkId
	k.masterInfo = masterInfo
	k.registered = true
	go k.storeFrameworkId() //TODO(jdef) only do this if we're checkpointing?
	log.Infof("Scheduler registered with the master: %v with frameworkId: %v\n", masterInfo, frameworkId)

	//TODO(jdef) partial reconciliation started... needs work
	r := &Reconciler{Action: k.ReconcileRunningTasks}
	go util.Forever(func() { r.Run(driver) }, 5*time.Minute) // TODO(jdef) parameterize reconciliation interval
}

func (k *KubernetesScheduler) storeFrameworkId() {
	_, err := k.etcdClient.Set(meta.FrameworkIDKey, k.frameworkId.GetValue(), 0)
	if err != nil {
		log.Error(err)
	}
}

// Reregistered is called when the scheduler re-registered with the master successfully.
// This happends when the master fails over.
func (k *KubernetesScheduler) Reregistered(driver bindings.SchedulerDriver, masterInfo *mesos.MasterInfo) {
	log.Infof("Scheduler reregistered with the master: %v\n", masterInfo)
	k.registered = true

	//TODO(jdef) partial reconciliation started... needs work
	r := &Reconciler{Action: k.ReconcileRunningTasks}
	go util.Forever(func() { r.Run(driver) }, 5*time.Minute) // TODO(jdef) parameterize reconciliation interval
}

// Disconnected is called when the scheduler loses connection to the master.
func (k *KubernetesScheduler) Disconnected(driver bindings.SchedulerDriver) {
	log.Infof("Master disconnected!\n")
	k.registered = false

	k.Lock()
	defer k.Unlock()

	// discard all cached offers to avoid unnecessary TASK_LOST updates
	k.offers.Invalidate("")
}

// ResourceOffers is called when the scheduler receives some offers from the master.
func (k *KubernetesScheduler) ResourceOffers(driver bindings.SchedulerDriver, offers []*mesos.Offer) {
	log.V(2).Infof("Received offers %+v", offers)

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
func (k *KubernetesScheduler) OfferRescinded(driver bindings.SchedulerDriver, offerId *mesos.OfferID) {
	log.Infof("Offer rescinded %v\n", offerId)

	k.Lock()
	defer k.Unlock()
	oid := offerId.GetValue()
	k.deleteOffer(oid)
}

// StatusUpdate is called when a status update message is sent to the scheduler.
func (k *KubernetesScheduler) StatusUpdate(driver bindings.SchedulerDriver, taskStatus *mesos.TaskStatus) {
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
		k.handleTaskLost(driver, taskStatus)
	}
}

func (k *KubernetesScheduler) handleTaskStaging(taskStatus *mesos.TaskStatus) {
	log.Errorf("Not implemented: task staging")
}

func (k *KubernetesScheduler) handleTaskStarting(taskStatus *mesos.TaskStatus) {
	// we expect to receive this when a launched task is finally "bound"
	// via the API server. however, there's nothing specific for us to do
	// here.

	taskId := taskStatus.GetTaskId().GetValue()
	switch task, state := k.getTask(taskId); state {
	case statePending:
		task.updatedTime = time.Now()
		//TODO(jdef) properly emit metric, or event type instead of just logging
		task.bindTime = task.updatedTime
		log.V(1).Infof("metric time_to_bind %v task %v pod %v", task.bindTime.Sub(task.launchTime), task.ID, task.Pod.Name)
	default:
		log.Warningf("Ignore status TASK_STARTING because the the task is not pending")
	}
}

func (k *KubernetesScheduler) handleTaskRunning(taskStatus *mesos.TaskStatus) {
	taskId, slaveId := taskStatus.GetTaskId().GetValue(), taskStatus.GetSlaveId().GetValue()
	if _, exists := k.slaves[slaveId]; !exists {
		log.Warningf("Ignore status TASK_RUNNING because the slave does not exist\n")
		return
	}
	switch task, state := k.getTask(taskId); state {
	case statePending:
		task.updatedTime = time.Now()
		log.Infof("Received running status for pending task: %+v", taskStatus)
		k.fillRunningPodInfo(task, taskStatus)
		k.runningTasks[taskId] = task
		delete(k.pendingTasks, taskId)
	case stateRunning:
		task.updatedTime = time.Now()
		log.V(2).Info("Ignore status TASK_RUNNING because the the task is already running")
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
			"received finished status for running task: %+v, running/pod task queue length = %d/%d",
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
	log.Errorf("task failed: %+v", taskStatus)
	taskId := taskStatus.GetTaskId().GetValue()

	switch task, state := k.getTask(taskId); state {
	case statePending:
		delete(k.pendingTasks, taskId)
		delete(k.podToTask, task.podKey)
		if task.launched && messages.CreateBindingFailure == taskStatus.GetMessage() {
			go k.plugin.reconcilePod(*task.Pod)
		}
	case stateRunning:
		delete(k.runningTasks, taskId)
		delete(k.podToTask, task.podKey)
	}
}

func (k *KubernetesScheduler) handleTaskKilled(taskStatus *mesos.TaskStatus) {
	var task *PodTask
	defer func() {
		msg := fmt.Sprintf("task killed: %+v, task %+v", taskStatus, task)
		if task != nil && task.deleted {
			// we were expecting this, nothing out of the ordinary
			log.V(2).Infoln(msg)
		} else {
			log.Errorln(msg)
		}
	}()

	taskId := taskStatus.GetTaskId().GetValue()
	task, state := k.getTask(taskId)
	switch state {
	case statePending:
		delete(k.pendingTasks, taskId)
		fallthrough
	case stateRunning:
		delete(k.runningTasks, taskId)
		delete(k.podToTask, task.podKey)
	}
}

func (k *KubernetesScheduler) handleTaskLost(driver bindings.SchedulerDriver, status *mesos.TaskStatus) {
	log.Errorf("task lost: %+v", status)
	taskId := status.GetTaskId().GetValue()

	switch task, state := k.getTask(taskId); state {
	case statePending:
		delete(k.pendingTasks, taskId)
		fallthrough
	case stateRunning:
		if status.ExecutorId != nil && status.SlaveId != nil {
			//TODO(jdef) this may not be meaningful once we have proper checkpointing and master detection
			//If we're reconciling and receive this then the executor may be
			//running a task that we need it to kill. It's possible that the framework
			//is unrecognized by the master at this point, so KillTask is not guaranteed
			//to do anything. The underlying driver transport may be able to send a
			//FrameworkMessage directly to the slave to terminate the task.
			log.V(2).Info("forwarding TASK_LOST message to executor %v on slave %v", status.ExecutorId, status.SlaveId)
			data := fmt.Sprintf("task-lost:%s", taskId) //TODO(jdef) use a real message type
			if _, err := driver.SendFrameworkMessage(status.ExecutorId, status.SlaveId, data); err != nil {
				log.Error(err)
			}
		}
		delete(k.runningTasks, taskId)
		delete(k.podToTask, task.podKey)
	}
}

// FrameworkMessage is called when the scheduler receives a message from the executor.
func (k *KubernetesScheduler) FrameworkMessage(driver bindings.SchedulerDriver,
	executorId *mesos.ExecutorID, slaveId *mesos.SlaveID, message string) {
	log.Infof("Received messages from executor %v of slave %v, %v\n", executorId, slaveId, message)
}

// SlaveLost is called when some slave is lost.
func (k *KubernetesScheduler) SlaveLost(driver bindings.SchedulerDriver, slaveId *mesos.SlaveID) {
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
func (k *KubernetesScheduler) ExecutorLost(driver bindings.SchedulerDriver,
	executorId *mesos.ExecutorID, slaveId *mesos.SlaveID, status int) {
	log.Infof("Executor %v of slave %v is lost, status: %v\n", executorId, slaveId, status)
	// TODO(yifan): Restart any unfinished tasks of the executor.
}

// Error is called when there is an unrecoverable error in the scheduler or scheduler driver.
// The driver should have been aborted before this is invoked.
func (k *KubernetesScheduler) Error(driver bindings.SchedulerDriver, message string) {
	log.Fatalf("fatal scheduler error: %v\n", message)
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

// intended to be invoked as a Reconciler.Action by Reconciler.Run
func (k *KubernetesScheduler) ReconcileRunningTasks(driver bindings.SchedulerDriver, canceled <-chan struct{}) error {
	log.Info("reconcile running tasks")
	remaining := make(map[string]bool)
	statusList := []*mesos.TaskStatus{}
	func() {
		k.RLock()
		defer k.RUnlock()
		for taskId := range k.runningTasks {
			remaining[taskId] = true
			statusList = append(statusList, mutil.NewTaskStatus(mutil.NewTaskID(taskId), mesos.TaskState_TASK_RUNNING))
		}
	}()
	if _, err := driver.ReconcileTasks(statusList); err != nil {
		return err
	}
	start := time.Now()
	const maxBackoff = 120 * time.Second
	first := true
	for backoff := 1 * time.Second; first || len(remaining) > 0; backoff = backoff * 2 {
		first = false
		//TODO(jdef) reconcileTasks(remaining, canceled)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		select {
		case <-canceled:
			return nil //TODO(jdef) should probably return a cancelation error
		case <-time.After(backoff):
			func() {
				k.RLock()
				defer k.RUnlock()
				for taskId := range remaining {
					if task, found := k.runningTasks[taskId]; found && task.updatedTime.Before(start) {
						// keep this task in remaining list
						continue
					}
					delete(remaining, taskId)
				}
			}()
		}
	}
	return nil
}

type Reconciler struct {
	Action  func(driver bindings.SchedulerDriver, canceled <-chan struct{}) error
	running int32 // 1 when Action is running, 0 otherwise
}

// execute task reconciliation, returns a cancelation channel or nil if reconciliation is already running.
// a client may signal that reconciliation should be canceled by closing the cancelation channel.
// no objects are ever read from, or written to, the cancelation channel.
func (r *Reconciler) Run(driver bindings.SchedulerDriver) <-chan struct{} {
	if atomic.CompareAndSwapInt32(&r.running, 0, 1) {
		canceled := make(chan struct{})
		go func() {
			defer atomic.StoreInt32(&r.running, 0)
			err := r.Action(driver, canceled)
			if err != nil {
				log.Error(err)
			}
		}()
		return canceled
	}
	return nil
}
