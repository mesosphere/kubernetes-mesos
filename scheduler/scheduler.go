package scheduler

import (
	"container/list"
	"fmt"
	"sync"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	kubernetes "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	log "github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/golang/glog"
	"github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/mesosphere/mesos-go/mesos"
)

var errSchedulerTimeout = fmt.Errorf("Schedule time out")

const defaultFinishedTasksSize = 1024

// PodScheduleFunc implements how to schedule
// pods among slaves. We can have different implementation
// for different scheduling policy.
//
// The Schedule function takes a group of slaves (each contains offers
// from that slave), and a group of pods.
// To schedule a pod, just fill the 'SelectedMachine' field of
// the PodTask. See the FIFOScheduleFunc for example.
type PodScheduleFunc func(slaves map[string]*Slave, tasks *list.List)

// A struct describes a pod task.
type PodTask struct {
	Pod             *api.Pod
	Machines        []string
	SelectedMachine chan string
	TaskInfo        *mesos.TaskInfo
}

func newPodTask(pod *api.Pod, machines []string) *PodTask {
	task := &PodTask{
		Pod:             pod,
		Machines:        make([]string, len(machines)),
		SelectedMachine: make(chan string, 1),
		TaskInfo:        new(mesos.TaskInfo),
	}
	copy(task.Machines, machines)
	return task
}

// A struct that describes the slave.
type Slave struct {
	Offers map[string]*mesos.Offer
	Tasks  map[string]*PodTask
	State  bool // connected or disconnected.
}

func newSlave() *Slave {
	return &Slave{
		Offers: make(map[string]*mesos.Offer),
		Tasks:  make(map[string]*PodTask),
	}
}

// KubernetesScheduler implements:
// 1: The interfaces of the mesos scheduler.
// 2: The interface of a kubernetes scheduler.
// 3: The interfaces of a kubernetes pod registry.
type KubernetesScheduler struct {
	// We use a lock here to avoid races
	// between invoking the mesos callback
	// and the invoking the pob registry interfaces.
	*sync.RWMutex

	// Mesos context.
	driver      mesos.SchedulerDriver
	frameworkId *mesos.FrameworkID
	masterInfo  *mesos.MasterInfo
	registered  bool

	// OfferID => offer.
	offers map[string]*mesos.Offer

	// SlaveID => slave.
	slaves map[string]*Slave

	// Fields for task information book keeping.
	pendingTasks  *list.List
	runningTasks  map[string]*PodTask
	finishedTasks []*PodTask

	// The function that does scheduling.
	scheduleFunc PodScheduleFunc
}

// New create a new KubernetesScheduler.
func New(driver mesos.SchedulerDriver, scheduleFunc PodScheduleFunc) *KubernetesScheduler {
	return &KubernetesScheduler{
		new(sync.RWMutex),
		driver,
		nil,
		nil,
		false,
		make(map[string]*mesos.Offer),
		make(map[string]*Slave),
		list.New(),
		make(map[string]*PodTask),
		make([]*PodTask, defaultFinishedTasksSize),
		scheduleFunc,
	}
}

// Add a new task to the queue.
func (k *KubernetesScheduler) addTask(task *PodTask) {
	k.Lock()
	defer k.Unlock()
	k.pendingTasks.PushBack(task)
	k.scheduleFunc(k.slaves, k.pendingTasks)
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

	// Add offers to the pool.
	for _, offer := range offers {
		k.offers[offer.GetId().GetValue()] = offer
	}
	k.scheduleFunc(k.slaves, k.pendingTasks)
}

// OfferRescinded is called when the resources are recinded from the scheduler.
func (k *KubernetesScheduler) OfferRescinded(driver mesos.SchedulerDriver, offerId *mesos.OfferID) {
	log.Infof("Offer rescinded %v\n", offerId)

	k.Lock()
	defer k.Unlock()
	delete(k.offers, offerId.GetValue())
}

// StatusUpdate is called when a status update message is sent to the scheduler.
func (k *KubernetesScheduler) StatusUpdate(driver mesos.SchedulerDriver, taskStatus *mesos.TaskStatus) {
	log.Infof("Received status update %v\n", taskStatus)
	// TODO(yifan): Check status.
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
func (k *KubernetesScheduler) Schedule(pod api.Pod, minionLister kubernetes.MinionLister) (selectedMachine string, err error) {
	machineLists, err := minionLister.List()
	if err != nil {
		log.Warningf("minionLister.List() error %v\n", err)
		return "", err
	}

	task := newPodTask(&pod, machineLists)
	k.addTask(task)
	return <-task.SelectedMachine, nil
}

// ListPods obtains a list of pods that match selector.
func (k *KubernetesScheduler) ListPods(selector labels.Selector) ([]api.Pod, error) {
	return nil, fmt.Errorf("Not implemented")
}

// Get a specific pod.
func (k *KubernetesScheduler) GetPod(podID string) (*api.Pod, error) {
	// TODO(yifan): Send a message to slave/executor to get the pod status.
	// Need to implement this to let the PodRegistryStorage know that
	// the pod is running.
	//
	// Consider to read from a channel.
	return nil, fmt.Errorf("Not implemented")
}

// Create a pod based on a specification, schedule it onto a specific machine.
func (k *KubernetesScheduler) CreatePod(machine string, pod api.Pod) error {
	// TODO(yifan): Call launchTask.
	return fmt.Errorf("Not implemented")
}

// Update an existing pod.
func (k *KubernetesScheduler) UpdatePod(pod api.Pod) error {
	// TODO(yifan): Need to send a special message to the slave/executor.
	return fmt.Errorf("Not implemented")
}

// Delete an existing pod.
func (k *KubernetesScheduler) DeletePod(podID string) error {
	// TODO(yifan): killtask
	return fmt.Errorf("Not implemented")
}

// A FIFO scheduler.
func FIFOScheduleFunc(slaves map[string]*Slave, tasks *list.List) {
	for _, slave := range slaves {
		for task := tasks.Front(); task != nil; task = task.Next() {
			_ = slave
			// TODO(yifan):
			// if resources is ok.
			// task.SelectedMachine <- slave's hostname

			// Just schedule one pod for now.
			break
		}
	}
}
