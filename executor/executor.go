package executor

import (
	"encoding/json"
	"sync"
	"time"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	"gopkg.in/v1/yaml"
)

const (
	containerPollTime = 300 * time.Millisecond
	launchGracePeriod = 5 * time.Minute
)

type kuberTask struct {
	mesosTaskInfo     *mesos.TaskInfo
	containerManifest *api.ContainerManifest
}

// KubernetesExecutor is an mesos executor that runs pods
// in a minion machine.
type KubernetesExecutor struct {
	kl         *kubelet.Kubelet // the kubelet instance.
	updateChan chan<- interface{}
	driver     mesos.ExecutorDriver
	registered bool
	tasks      map[string]*kuberTask
	pods       map[string]*kubelet.Pod
        lock       sync.RWMutex
	namespace  string
}

// New creates a new kubernete executor.
func New(driver mesos.ExecutorDriver, kl *kubelet.Kubelet, ch chan<- interface{}, ns string) *KubernetesExecutor {
	return &KubernetesExecutor{
		kl:         kl,
		updateChan: ch,
		driver:     driver,
		registered: false,
		tasks:      make(map[string]*kuberTask),
		pods:       make(map[string]*kubelet.Pod),
		namespace:  ns,
	}
}

// Registered is called when the executor is successfully registered with the slave.
func (k *KubernetesExecutor) Registered(driver mesos.ExecutorDriver,
	executorInfo *mesos.ExecutorInfo, frameworkInfo *mesos.FrameworkInfo, slaveInfo *mesos.SlaveInfo) {
	log.Infof("Executor %v of framework %v registered with slave %v\n",
		executorInfo, frameworkInfo, slaveInfo)
	k.registered = true
}

// Reregistered is called when the executor is successfully re-registered with the slave.
// This can happen when the slave fails over.
func (k *KubernetesExecutor) Reregistered(driver mesos.ExecutorDriver, slaveInfo *mesos.SlaveInfo) {
	log.Infof("Reregistered with slave %v\n", slaveInfo)
	k.registered = true
}

// Disconnected is called when the executor is disconnected with the slave.
func (k *KubernetesExecutor) Disconnected(driver mesos.ExecutorDriver) {
	log.Infof("Slave is disconnected\n")
	k.registered = false
}

// LaunchTask is called when the executor receives a request to launch a task.
func (k *KubernetesExecutor) LaunchTask(driver mesos.ExecutorDriver, taskInfo *mesos.TaskInfo) {
	log.Infof("Launch task %v\n", taskInfo)

	if !k.registered {
		log.Warningf("Ignore launch task because the executor is disconnected\n")
		k.sendStatusUpdate(taskInfo.GetTaskId(),
			mesos.TaskState_TASK_FAILED, "Executor not registered yet")
		return
	}

	k.lock.Lock()
	defer k.lock.Unlock()

	taskId := taskInfo.GetTaskId().GetValue()
	if _, found := k.tasks[taskId]; found {
		log.Warningf("Task already launched\n")
		// Not to send back TASK_RUNNING here, because
		// may be duplicated messages or duplicated task id.
		return
	}
	// Get the container manifest from the taskInfo.
	var manifest api.ContainerManifest
	if err := yaml.Unmarshal(taskInfo.GetData(), &manifest); err != nil {
		log.Warningf("Failed to extract yaml data from the taskInfo.data %v\n", err)
		k.sendStatusUpdate(taskInfo.GetTaskId(),
			mesos.TaskState_TASK_FAILED, "Failed to extract yaml data")
		return
	}

	// TODO(nnielsen): Verify this assumption. Manifest ID's has been marked
	// to be deprecated.
	podID := manifest.ID
	uuid := manifest.UUID

	// Add the task.
	k.tasks[taskId] = &kuberTask{
		mesosTaskInfo:     taskInfo,
		containerManifest: &manifest,
	}

	pod := kubelet.Pod{
		Name:      podID,
		Namespace: k.namespace,
		Manifest:  manifest,
	}
	k.pods[podID] = &pod

	getPidInfo := func(name string) (api.PodInfo, error) {
		podFullName := kubelet.GetPodFullName(&kubelet.Pod{Name: name, Namespace: k.namespace})
		return k.kl.GetPodInfo(podFullName, uuid)
	}

	// TODO(nnielsen): Fail if container is already running.
	// TODO Checkpoint pods.

	// Send the pod updates to the channel.
	update := kubelet.PodUpdate{Op: kubelet.SET}
	for _, p := range k.pods {
		update.Pods = append(update.Pods, *p)
	}
	k.updateChan <- update

	// Delay reporting 'task running' until container is up.
	go func() {
		expires := time.Now().Add(launchGracePeriod)
		for {
			now := time.Now()
			if now.After(expires) {
				log.Warningf("Launch expired grace period of '%v'", launchGracePeriod)
				break
			}

			// We need to poll the kublet for the pod state, as
			// there is no existing event / push model for this.
			time.Sleep(containerPollTime)

			info, err := getPidInfo(podID)
			if err != nil {
				continue
			}

			log.V(2).Infof("Found pod info: '%v'", info)
			data, err := json.Marshal(info)

			statusUpdate := &mesos.TaskStatus{
				TaskId:  taskInfo.GetTaskId(),
				State:   mesos.NewTaskState(mesos.TaskState_TASK_RUNNING),
				Message: proto.String("Pod '" + podID + "' is running"),
				Data:    data,
			}

			if err := k.driver.SendStatusUpdate(statusUpdate); err != nil {
				log.Warningf("Failed to send status update%v, %v", err)
			}

			// TODO(nnielsen): Monitor health of container and report if lost.
			go func() {
				knownPod := func() bool {
					_, err := getPidInfo(podID)
					return err == nil
				}
				// Wait for the pod to go away and stop monitoring once it does
				// TODO (jdefelice) replace with an /events watch?
				for {
					time.Sleep(containerPollTime)
					if k.checkForLostPodTask(taskInfo, knownPod) {
						return
					}
				}
			}()

			return
		}
		k.sendStatusUpdate(taskInfo.GetTaskId(), mesos.TaskState_TASK_LOST, "Task lost: launch failed")
	}()
}

// Returns true if the pod becomes unkown and there is on longer a task record on file
// TODO (jdefelice) don't send false alarms for deleted pods (KILLED tasks)
func (k *KubernetesExecutor) checkForLostPodTask(taskInfo *mesos.TaskInfo, isKnownPod func() bool ) bool {
	k.lock.RLock()
	defer k.lock.RUnlock()

	if !isKnownPod() {
		taskId := taskInfo.GetTaskId().GetValue()
		if _, ok := k.tasks[taskId]; !ok {
			log.Infof("Ignoring lost container: task not present")
		} else {
			k.sendStatusUpdate(taskInfo.GetTaskId(), mesos.TaskState_TASK_LOST, "Task lost: container disappeared")
		}
		return true
	}
	return false
}

// KillTask is called when the executor receives a request to kill a task.
func (k *KubernetesExecutor) KillTask(driver mesos.ExecutorDriver, taskId *mesos.TaskID) {
	k.lock.Lock()
	defer k.lock.Unlock()

	log.Infof("Kill task %v\n", taskId)

	if !k.registered {
		log.Warningf("Ignore kill task because the executor is disconnected\n")
		return
	}

	k.killPodForTask(taskId.GetValue(), "Task killed")
}

// Kills the pod associated with the given task. Assumes that the caller has is locking around
// pod and task storage.
func (k *KubernetesExecutor) killPodForTask(tid, reason string) {
	task, ok := k.tasks[tid]
	if !ok {
		log.Infof("Failed to kill task, unknown task %v\n", tid)
		return
	}
	delete(k.tasks, tid)

	// TODO(nnielsen): Verify this assumption. Manifest ID's has been marked
	// to be deprecated.
	pid := task.containerManifest.ID
	if _, found := k.pods[pid]; !found {
		log.Warningf("Cannot remove Unknown pod %v\n", pid)
	} else {
		delete(k.pods, pid)

		// Send the pod updates to the channel.
		update := kubelet.PodUpdate{Op: kubelet.SET}
		for _, p := range k.pods {
			update.Pods = append(update.Pods, *p)
		}
		k.updateChan <- update
	}
	// TODO(yifan): Check the result of the kill event.

	k.sendStatusUpdate(task.mesosTaskInfo.GetTaskId(), mesos.TaskState_TASK_KILLED, reason)
}

// FrameworkMessage is called when the framework sends some message to the executor
func (k *KubernetesExecutor) FrameworkMessage(driver mesos.ExecutorDriver, message string) {
	log.Infof("Receives message from framework %v\n", message)
	// TODO(yifan): Check for update message.
}

// Shutdown is called when the executor receives a shutdown request.
func (k *KubernetesExecutor) Shutdown(driver mesos.ExecutorDriver) {
	log.Infof("Shutdown the executor\n")

	k.lock.Lock()
	defer k.lock.Unlock()

	for tid, _ := range k.tasks {
		k.killPodForTask(tid, "Executor shutdown")
	}
}

// Error is called when some error happens.
func (k *KubernetesExecutor) Error(driver mesos.ExecutorDriver, message string) {
	log.Errorf("Executor error: %v\n", message)
}

func (k *KubernetesExecutor) sendStatusUpdate(taskId *mesos.TaskID, state mesos.TaskState, message string) {
	statusUpdate := &mesos.TaskStatus{
		TaskId:  taskId,
		State:   &state,
		Message: proto.String(message),
	}
	// TODO(yifan): Maybe try to resend again in the future.
	if err := k.driver.SendStatusUpdate(statusUpdate); err != nil {
		log.Warningf("Failed to send status update%v, %v", err)
	}
}
