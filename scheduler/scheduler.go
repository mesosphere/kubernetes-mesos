package scheduler

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	kubernetes "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	log "github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/golang/glog"
	"github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/mesosphere/mesos-go/mesos"
)

var ErrSchedulerTimeout = fmt.Errorf("Schedule time out")

// A task to run the pod.
type podTask struct {
	pod             *api.Pod
	machines        []string
	selectedMachine chan string
	status          *mesos.TaskStatus
}

func newPodTask(pod *api.Pod, machines []string) *podTask {
	task := &podTask{
		pod:             pod,
		machines:        make([]string, len(machines)),
		selectedMachine: make(chan string, 1),
		status:          new(mesos.TaskStatus),
	}
	copy(task.machines, machines)
	return task
}

// A queue contains all pending tasks.
type pendingPodQueue struct {
	sync.RWMutex
	list.List
}

// Create a new pod task.

// A Kubernete Scheduler that runs on top of Mesos.
type KubernetesScheduler struct {
	frameworkId     *mesos.FrameworkID
	ScheduleTimeout time.Duration
	pendingPods     *pendingPodQueue
}

// New create a new KubernetesScheduler.
// the scheduleTimeout specifies the timeout for one Schedule call,
// if the task cannot be satisfied before the timeout, the Schedule()
// will return an error. The default timeout is 10 minutes.
func New(scheduleTimeout time.Duration) *KubernetesScheduler {
	if scheduleTimeout == 0 {
		scheduleTimeout = time.Minute * 10
	}
	return &KubernetesScheduler{
		ScheduleTimeout: scheduleTimeout,
		pendingPods:     new(pendingPodQueue),
	}
}

// Add a new task to the queue.
func (k *KubernetesScheduler) addTask(task *podTask) {
	k.pendingPods.Lock()
	defer k.pendingPods.Unlock()
	k.pendingPods.PushBack(task)
}

// Registered is called when the scheduler registered with the master successfully.
func (k *KubernetesScheduler) Registered(driver mesos.SchedulerDriver,
	frameworkId *mesos.FrameworkID, masterInfo *mesos.MasterInfo) {
	k.frameworkId = frameworkId
	log.Infof("Scheduler registered with the master: %v with frameworkId: %v\n", masterInfo, frameworkId)
}

// Reregistered is called when the scheduler re-registered with the master successfully.
// This happends when the master fails over.
func (k *KubernetesScheduler) Reregistered(driver mesos.SchedulerDriver, masterInfo *mesos.MasterInfo) {
	log.Infof("Scheduler reregistered with the master: %v with frameworkId: %v\n", masterInfo)
}

// Disconnected is called when the scheduler loses connection to the master.
func (k *KubernetesScheduler) Disconnected(driver mesos.SchedulerDriver) {
	log.Infof("Master disconnected!\n")
}

// ResourceOffers is called when the scheduler receives some offers from the master.
func (k *KubernetesScheduler) ResourceOffers(driver mesos.SchedulerDriver, offers []*mesos.Offer) {
	log.Infof("Received offers\n")
	log.V(2).Infof("%v\n", offers)

	// TODO(yifan): Pick up one task and statisfy it.
}

// OfferRescinded is called when the resources are recinded from the scheduler.
func (k *KubernetesScheduler) OfferRescinded(driver mesos.SchedulerDriver, offerId *mesos.OfferID) {
	log.Infof("Offer rescinded %v\n", offerId)
	// TODO(yifan): Rescinded offers.
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
	log.Error(message)
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
	select {
	case <-time.After(k.ScheduleTimeout):
		log.Warningf("Schedule times out")
		return "", ErrSchedulerTimeout
	case selectedMachine = <-task.selectedMachine:
		return selectedMachine, nil
	}
}
