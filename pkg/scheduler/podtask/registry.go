package podtask

import (
	"container/ring"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	log "github.com/golang/glog"
	mesos "github.com/mesos/mesos-go/mesosproto"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/metrics"
)

/**
HACK(jdef): we're not using etcd but k8s has implemented namespace support and
we're going to try to honor that by namespacing pod keys. Hence, the following
funcs that were stolen from:
    https://github.com/GoogleCloudPlatform/kubernetes/blob/release-0.5/pkg/registry/etcd/etcd.go
**/

const (
	PodPath                  = "/pods"
	defaultFinishedTasksSize = 1024
)

type Registry interface {
	Register(*T, error) (*T, error)
	Unregister(*T)
	Get(taskId string) (task *T, currentState StateType)
	TaskForPod(podID string) (taskID string, ok bool)
	UpdateStatus(status *mesos.TaskStatus) (*T, StateType)
	List(filter *StateType) []string
}

type inMemoryRegistry struct {
	rw            sync.RWMutex
	taskRegistry  map[string]*T
	tasksFinished *ring.Ring
	podToTask     map[string]string
}

func NewInMemoryRegistry() Registry {
	return &inMemoryRegistry{
		taskRegistry:  make(map[string]*T),
		tasksFinished: ring.New(defaultFinishedTasksSize),
		podToTask:     make(map[string]string),
	}
}

func (k *inMemoryRegistry) List(filter *StateType) (taskids []string) {
	k.rw.RLock()
	defer k.rw.RUnlock()
	for id, task := range k.taskRegistry {
		if filter == nil || *filter == task.State {
			taskids = append(taskids, id)
		}
	}
	return
}

func (k *inMemoryRegistry) TaskForPod(podID string) (taskID string, ok bool) {
	k.rw.RLock()
	defer k.rw.RUnlock()
	// assume caller is holding scheduler lock
	taskID, ok = k.podToTask[podID]
	return
}

// registers a pod task unless the spec'd error is not nil
func (k *inMemoryRegistry) Register(task *T, err error) (*T, error) {
	if err == nil {
		k.rw.Lock()
		defer k.rw.Unlock()
		k.podToTask[task.podKey] = task.ID
		k.taskRegistry[task.ID] = task
	}
	return task, err
}

func (k *inMemoryRegistry) Unregister(task *T) {
	k.rw.Lock()
	defer k.rw.Unlock()
	delete(k.podToTask, task.podKey)
	delete(k.taskRegistry, task.ID)
}

func (k *inMemoryRegistry) Get(taskId string) (*T, StateType) {
	k.rw.RLock()
	defer k.rw.RUnlock()
	return k._get(taskId)
}

// assume that the caller has already locked around access to task state
func (k *inMemoryRegistry) _get(taskId string) (*T, StateType) {
	if task, found := k.taskRegistry[taskId]; found {
		return task, task.State
	}
	return nil, StateUnknown
}

func (k *inMemoryRegistry) UpdateStatus(status *mesos.TaskStatus) (*T, StateType) {
	taskId := status.GetTaskId().GetValue()

	k.rw.Lock()
	defer k.rw.Unlock()
	task, state := k._get(taskId)

	switch status.GetState() {
	case mesos.TaskState_TASK_STAGING:
		k.handleTaskStaging(task, state, status)
	case mesos.TaskState_TASK_STARTING:
		k.handleTaskStarting(task, state, status)
	case mesos.TaskState_TASK_RUNNING:
		k.handleTaskRunning(task, state, status)
	case mesos.TaskState_TASK_FINISHED:
		k.handleTaskFinished(task, state, status)
	case mesos.TaskState_TASK_FAILED:
		k.handleTaskFailed(task, state, status)
	case mesos.TaskState_TASK_KILLED:
		k.handleTaskKilled(task, state, status)
	case mesos.TaskState_TASK_LOST:
		k.handleTaskLost(task, state, status)
	default:
		log.Warning("unhandled task status update: %+v", status)
	}
	return task, state
}

func (k *inMemoryRegistry) handleTaskStaging(task *T, state StateType, status *mesos.TaskStatus) {
	log.Errorf("Not implemented: task staging")
}

func (k *inMemoryRegistry) handleTaskStarting(task *T, state StateType, status *mesos.TaskStatus) {
	// we expect to receive this when a launched task is finally "bound"
	// via the API server. however, there's nothing specific for us to do
	// here.
	switch state {
	case StatePending:
		task.UpdatedTime = time.Now()
		if !task.Has(Bound) {
			task.Set(Bound)
			task.bindTime = task.UpdatedTime
			timeToBind := task.bindTime.Sub(task.launchTime)
			metrics.BindLatency.Observe(metrics.InMicroseconds(timeToBind))
		}
	default:
		log.Warningf("Ignore status TASK_STARTING because the the task is not pending")
	}
}

func (k *inMemoryRegistry) handleTaskRunning(task *T, state StateType, status *mesos.TaskStatus) {
	switch state {
	case StatePending:
		task.UpdatedTime = time.Now()
		log.Infof("Received running status for pending task: %+v", status)
		fillRunningPodInfo(task, status)
		task.State = StateRunning
	case StateRunning:
		task.UpdatedTime = time.Now()
		log.V(2).Info("Ignore status TASK_RUNNING because the the task is already running")
	case StateFinished:
		log.Warningf("Ignore status TASK_RUNNING because the the task is already finished")
	default:
		log.Warningf("Ignore status TASK_RUNNING (%+v) because the the task is discarded", status.GetTaskId())
	}
}

func fillRunningPodInfo(task *T, taskStatus *mesos.TaskStatus) {
	if taskStatus.Data != nil {
		if err := json.Unmarshal(taskStatus.Data, &task.Pod.Status); err != nil {
			log.Errorf("Invalid TaskStatus.Data for task '%v': %v", task.ID, err)
		}
	} else {
		log.Errorf("Missing TaskStatus.Data for task '%v'", task.ID)
	}
}

func (k *inMemoryRegistry) handleTaskFinished(task *T, state StateType, status *mesos.TaskStatus) {
	switch state {
	case StatePending:
		panic("Pending task finished, this couldn't happen")
	case StateRunning:
		log.V(2).Infof("received finished status for running task: %+v", status)
		delete(k.podToTask, task.podKey)
		task.State = StateFinished
		task.UpdatedTime = time.Now()
		k.tasksFinished = k.recordFinishedTask(task.ID)
	case StateFinished:
		log.Warningf("Ignore status TASK_FINISHED because the the task is already finished")
	default:
		log.Warningf("Ignore status TASK_FINISHED because the the task is not running")
	}
}

// record that a task has finished.
// older record are expunged one at a time once the historical ring buffer is saturated.
// assumes caller is holding state lock.
func (k *inMemoryRegistry) recordFinishedTask(taskId string) *ring.Ring {
	slot := k.tasksFinished.Next()
	if slot.Value != nil {
		// garbage collect older finished task from the registry
		gctaskId := slot.Value.(string)
		if gctask, found := k.taskRegistry[gctaskId]; found && gctask.State == StateFinished {
			delete(k.taskRegistry, gctaskId)
		}
	}
	slot.Value = taskId
	return slot
}

func (k *inMemoryRegistry) handleTaskFailed(task *T, state StateType, status *mesos.TaskStatus) {
	log.Errorf("task failed: %+v", status)
	switch state {
	case StatePending:
		delete(k.taskRegistry, task.ID)
		delete(k.podToTask, task.podKey)
	case StateRunning:
		delete(k.taskRegistry, task.ID)
		delete(k.podToTask, task.podKey)
	}
}

func (k *inMemoryRegistry) handleTaskKilled(task *T, state StateType, status *mesos.TaskStatus) {
	defer func() {
		msg := fmt.Sprintf("task killed: %+v, task %+v", status, task)
		if task != nil && task.Has(Deleted) {
			// we were expecting this, nothing out of the ordinary
			log.V(2).Infoln(msg)
		} else {
			log.Errorln(msg)
		}
	}()
	switch state {
	case StatePending, StateRunning:
		delete(k.taskRegistry, task.ID)
		delete(k.podToTask, task.podKey)
	}
}

func (k *inMemoryRegistry) handleTaskLost(task *T, state StateType, status *mesos.TaskStatus) {
	log.Warningf("task lost: %+v", status)
	switch state {
	case StateRunning, StatePending:
		delete(k.taskRegistry, task.ID)
		delete(k.podToTask, task.podKey)
	}
}
