package scheduler

import (
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
	"github.com/mesosphere/kubernetes-mesos/pkg/offers"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/meta"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/podtask"
)

const (
	defaultOfferTTL       = 5    // seconds that an offer is viable, prior to being expired
	defaultOfferLingerTTL = 120  // seconds that an expired offer lingers in history
	defaultListenerDelay  = 1    // number of seconds between offer listener notifications
	defaultUpdatesBacklog = 2048 // size of the pod updates channel
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
	// In particular, changes to podtask.T objects are currently guarded by this lock.
	*sync.RWMutex

	// Mesos context.
	executor    *mesos.ExecutorInfo
	driver      bindings.SchedulerDriver
	frameworkId *mesos.FrameworkID
	masterInfo  *mesos.MasterInfo
	registered  bool

	offers       offers.Registry
	slaves       map[string]*Slave // SlaveID => slave.
	slaveIDs     map[string]string // Slave's hostname => slaveID
	taskRegistry podtask.Registry

	scheduleFunc PodScheduleFunc // The function that does scheduling.

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
		offers: offers.CreateRegistry(offers.RegistryConfig{
			DeclineOffer: func(id string) error {
				offerId := mutil.NewOfferID(id)
				filters := &mesos.Filters{}
				_, err := k.driver.DeclineOffer(offerId, filters)
				return err
			},
			TTL:           defaultOfferTTL * time.Second,
			LingerTTL:     defaultOfferLingerTTL * time.Second, // remember expired offers so that we can tell if a previously scheduler offer relies on one
			ListenerDelay: defaultListenerDelay * time.Second,
		}),
		slaves:       make(map[string]*Slave),
		slaveIDs:     make(map[string]string),
		taskRegistry: podtask.NewInMemoryRegistry(),
		scheduleFunc: scheduleFunc,
		client:       client,
		etcdClient:   etcdClient,
	}
	return k
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

// OfferRescinded is called when the resources are recinded from the scheduler.
func (k *KubernetesScheduler) OfferRescinded(driver bindings.SchedulerDriver, offerId *mesos.OfferID) {
	log.Infof("Offer rescinded %v\n", offerId)

	oid := offerId.GetValue()
	if offer, ok := k.offers.Get(oid); ok {
		k.offers.Delete(oid)
		if details := offer.Details(); details != nil {
			k.Lock()
			defer k.Unlock()

			slaveId := details.GetSlaveId().GetValue()

			if slave, found := k.slaves[slaveId]; !found {
				log.Infof("No slave for id %s associated with offer id %s", slaveId, oid)
			} else {
				delete(slave.Offers, oid)
			}
		} // else, offer already expired / lingering
	}
}

// StatusUpdate is called when a status update message is sent to the scheduler.
func (k *KubernetesScheduler) StatusUpdate(driver bindings.SchedulerDriver, taskStatus *mesos.TaskStatus) {
	//TODO(jdef) we're going to make changes to podtask.T objects in here and since the current TaskRegistry
	//implementation is in-memory we need a critical section for this.
	k.Lock()
	defer k.Unlock()

	log.Infof("Received status update %v\n", taskStatus)

	switch taskStatus.GetState() {
	case mesos.TaskState_TASK_STAGING:
		log.Errorf("Not implemented: task staging")
	case mesos.TaskState_TASK_RUNNING, mesos.TaskState_TASK_FINISHED, mesos.TaskState_TASK_STARTING:
		if !func() (exists bool) {
			slaveId := taskStatus.GetSlaveId().GetValue()
			_, exists = k.slaves[slaveId]
			return
		}() {
			log.Warningf("Ignore status %+v because the slave does not exist", taskStatus)
			return
		}
		fallthrough
	case mesos.TaskState_TASK_KILLED:
		k.taskRegistry.UpdateStatus(taskStatus)
	case mesos.TaskState_TASK_FAILED:
		if task, _ := k.taskRegistry.UpdateStatus(taskStatus); task != nil {
			if task.Has(podtask.Launched) && messages.CreateBindingFailure == taskStatus.GetMessage() {
				go k.plugin.reconcilePod(*task.Pod)
			}
		}
	case mesos.TaskState_TASK_LOST:
		task, state := k.taskRegistry.UpdateStatus(taskStatus)
		if state == podtask.StateRunning && taskStatus.ExecutorId != nil && taskStatus.SlaveId != nil {
			//TODO(jdef) this may not be meaningful once we have proper checkpointing and master detection
			//If we're reconciling and receive this then the executor may be
			//running a task that we need it to kill. It's possible that the framework
			//is unrecognized by the master at this point, so KillTask is not guaranteed
			//to do anything. The underlying driver transport may be able to send a
			//FrameworkMessage directly to the slave to terminate the task.
			log.V(2).Info("forwarding TASK_LOST message to executor %v on slave %v", taskStatus.ExecutorId, taskStatus.SlaveId)
			data := fmt.Sprintf("task-lost:%s", task.ID) //TODO(jdef) use a real message type
			if _, err := driver.SendFrameworkMessage(taskStatus.ExecutorId, taskStatus.SlaveId, data); err != nil {
				log.Error(err)
			}
		}
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

// intended to be invoked as a Reconciler.Action by Reconciler.Run
func (k *KubernetesScheduler) ReconcileRunningTasks(driver bindings.SchedulerDriver, canceled <-chan struct{}) error {
	log.Info("reconcile running tasks")

	statusList := []*mesos.TaskStatus{}
	if _, err := driver.ReconcileTasks(statusList); err != nil {
		return err
	}

	filter := podtask.StateRunning
	remaining := util.NewStringSet()
	remaining.Insert(k.taskRegistry.List(&filter)...)
	start := time.Now()
	const maxBackoff = 120 * time.Second // TODO(jdef) extract constant
	first := true
	for backoff := 1 * time.Second; first || remaining.Len() > 0; backoff = backoff * 2 {
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
					if task, state := k.taskRegistry.Get(taskId); state == podtask.StateRunning && task.UpdatedTime.Before(start) {
						// keep this task in remaining list
						continue
					}
					remaining.Delete(taskId)
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
				log.Errorf("reconciler action failed: %v", err)
			}
		}()
		return canceled
	}
	return nil
}
