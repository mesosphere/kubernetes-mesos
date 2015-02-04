package executor

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	"github.com/gogo/protobuf/proto"
	log "github.com/golang/glog"
	edriver "github.com/mesos/mesos-go/executor"
	mesos "github.com/mesos/mesos-go/mesosproto"
	"github.com/mesosphere/kubernetes-mesos/pkg/executor/messages"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/meta"
	"gopkg.in/v2/yaml"
)

const (
	containerPollTime = 300 * time.Millisecond
	launchGracePeriod = 5 * time.Minute
)

type kuberTask struct {
	mesosTaskInfo *mesos.TaskInfo
	podName       string
}

// KubernetesExecutor is an mesos executor that runs pods
// in a minion machine.
type KubernetesExecutor struct {
	kl         *kubelet.Kubelet // the kubelet instance.
	updateChan chan<- interface{}
	registered bool
	tasks      map[string]*kuberTask
	pods       map[string]*api.BoundPod
	lock       sync.RWMutex
	sourcename string
	client     *client.Client
}

// New creates a new kubernetes executor.
func New(kl *kubelet.Kubelet, ch chan<- interface{}, ns string, cl *client.Client) *KubernetesExecutor {
	return &KubernetesExecutor{
		kl:         kl,
		updateChan: ch,
		registered: false,
		tasks:      make(map[string]*kuberTask),
		pods:       make(map[string]*api.BoundPod),
		sourcename: ns,
		client:     cl,
	}
}

// Registered is called when the executor is successfully registered with the slave.
func (k *KubernetesExecutor) Registered(driver edriver.ExecutorDriver,
	executorInfo *mesos.ExecutorInfo, frameworkInfo *mesos.FrameworkInfo, slaveInfo *mesos.SlaveInfo) {
	log.Infof("Executor %v of framework %v registered with slave %v\n",
		executorInfo, frameworkInfo, slaveInfo)
	k.registered = true
}

// Reregistered is called when the executor is successfully re-registered with the slave.
// This can happen when the slave fails over.
func (k *KubernetesExecutor) Reregistered(driver edriver.ExecutorDriver, slaveInfo *mesos.SlaveInfo) {
	log.Infof("Reregistered with slave %v\n", slaveInfo)
	k.registered = true
}

// Disconnected is called when the executor is disconnected with the slave.
func (k *KubernetesExecutor) Disconnected(driver edriver.ExecutorDriver) {
	log.Infof("Slave is disconnected\n")
	k.registered = false
}

// LaunchTask is called when the executor receives a request to launch a task.
func (k *KubernetesExecutor) LaunchTask(driver edriver.ExecutorDriver, taskInfo *mesos.TaskInfo) {
	log.Infof("Launch task %v\n", taskInfo)

	if !k.registered {
		log.Warningf("Ignore launch task because the executor is disconnected\n")
		k.sendStatusUpdate(driver, taskInfo.GetTaskId(),
			mesos.TaskState_TASK_FAILED, messages.ExecutorUnregistered)
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
	go k.launchTask(driver, taskInfo)
}

func (k *KubernetesExecutor) getPidInfo(name string) (api.PodInfo, error) {
	return k.kl.GetPodInfo(name, "")
}

func (k *KubernetesExecutor) launchTask(driver edriver.ExecutorDriver, taskInfo *mesos.TaskInfo) {
	// Get the bound pod spec from the taskInfo.
	var pod api.BoundPod
	if err := yaml.Unmarshal(taskInfo.GetData(), &pod); err != nil {
		log.Warningf("Failed to extract yaml data from the taskInfo.data %v\n", err)
		k.sendStatusUpdate(driver, taskInfo.GetTaskId(),
			mesos.TaskState_TASK_FAILED, messages.UnmarshalTaskDataFailure)
		return
	}

	//HACK(jdef): cloned binding construction from k8s plugin/pkg/scheduler/scheduler.go
	binding := &api.Binding{
		ObjectMeta: api.ObjectMeta{
			Namespace:   pod.Namespace,
			Annotations: make(map[string]string),
		},
		PodID: pod.Name,
		Host:  pod.Annotations[meta.BindingHostKey],
	}
	//TODO(jdef) this next part is useless until k8s implements annotation propagation
	//from api.Binding to api.BoundPod in etcd.go:assignPod()
	for k, v := range pod.Annotations {
		binding.Annotations[k] = v
	}
	log.Infof("Binding '%v' to '%v' ...", binding.PodID, binding.Host)
	ctx := api.WithNamespace(api.NewDefaultContext(), binding.Namespace)
	err := k.client.Post().Namespace(api.Namespace(ctx)).Resource("bindings").Body(binding).Do().Error()
	if err != nil {
		k.sendStatusUpdate(driver, taskInfo.GetTaskId(),
			mesos.TaskState_TASK_FAILED, messages.CreateBindingFailure)
		return
	}

	k.sendStatusUpdate(driver, taskInfo.GetTaskId(),
		mesos.TaskState_TASK_STARTING, messages.CreateBindingSuccess)

	podFullName := kubelet.GetPodFullName(&api.BoundPod{
		ObjectMeta: api.ObjectMeta{
			Name:        pod.Name,
			Namespace:   pod.Namespace,
			Annotations: map[string]string{kubelet.ConfigSourceAnnotationKey: k.sourcename},
		},
	})

	k.lock.Lock()
	defer k.lock.Unlock()

	// Add the task.
	taskId := taskInfo.GetTaskId().GetValue()
	k.tasks[taskId] = &kuberTask{
		mesosTaskInfo: taskInfo,
		podName:       podFullName,
	}
	k.pods[podFullName] = &pod

	// TODO(nnielsen) Checkpoint pods.

	// Send the pod updates to the channel.
	update := kubelet.PodUpdate{Op: kubelet.SET}
	for _, p := range k.pods {
		update.Pods = append(update.Pods, *p)
	}
	k.updateChan <- update

	// Delay reporting 'task running' until container is up.
	go k._launchTask(driver, taskInfo, podFullName)
}

func (k *KubernetesExecutor) _launchTask(driver edriver.ExecutorDriver, taskInfo *mesos.TaskInfo, podFullName string) {
	expires := time.Now().Add(launchGracePeriod)
	taskId := taskInfo.GetTaskId()
	for {
		now := time.Now()
		if now.After(expires) {
			log.Warningf("Launch expired grace period of '%v'", launchGracePeriod)
			break
		}

		// We need to poll the kublet for the pod state, as
		// there is no existing event / push model for this.
		time.Sleep(containerPollTime)

		info, err := k.getPidInfo(podFullName)
		if err != nil {
			continue
		}

		// avoid sending back a running status while pod networking is down
		if podnet, ok := info["net"]; !ok || podnet.State.Running == nil {
			continue
		}

		log.V(2).Infof("Found pod info: '%v'", info)
		data, err := json.Marshal(info)
		if err != nil {
			log.Error(err)
			continue // attempt to recover
		}

		running := mesos.TaskState_TASK_RUNNING
		statusUpdate := &mesos.TaskStatus{
			TaskId:  taskId,
			State:   &running,
			Message: proto.String("Pod '" + podFullName + "' is running"),
			Data:    data,
		}

		if _, err := driver.SendStatusUpdate(statusUpdate); err != nil {
			log.Warningf("Failed to send status update%v, %v", err)
		}

		// continue to monitor the health of the pod
		go k.__launchTask(driver, taskInfo, podFullName)
		return
	} //forever

	k.lock.Lock()
	defer k.lock.Unlock()
	k.reportLostTask(driver, taskId.GetValue(), messages.LaunchTaskFailed)
}

func (k *KubernetesExecutor) __launchTask(driver edriver.ExecutorDriver, taskInfo *mesos.TaskInfo, podFullName string) {
	// TODO(nnielsen): Monitor health of pod and report if lost.
	// Should we also allow this to fail a couple of times before reporting lost?
	// What if the docker daemon is restarting and we can't connect, but it's
	// going to bring the pods back online as soon as it restarts?
	knownPod := func() bool {
		_, err := k.getPidInfo(podFullName)
		return err == nil
	}
	// Wait for the pod to go away and stop monitoring once it does
	// TODO (jdefelice) replace with an /events watch?
	for {
		time.Sleep(containerPollTime)
		if k.checkForLostPodTask(driver, taskInfo, knownPod) {
			return
		}
	}
}

// Intended to be executed as part of the pod monitoring loop, this fn (ultimately) checks with Docker
// whether the pod is running. It will only return false if the task is still registered and the pod is
// registered in Docker. Otherwise it returns true. If there's still a task record on file, but no pod
// in Docker, then we'll also send a TASK_LOST event.
func (k *KubernetesExecutor) checkForLostPodTask(driver edriver.ExecutorDriver, taskInfo *mesos.TaskInfo, isKnownPod func() bool) bool {
	// TODO (jdefelice) don't send false alarms for deleted pods (KILLED tasks)
	k.lock.Lock()
	defer k.lock.Unlock()

	// TODO(jdef) we should really consider k.pods here, along with what docker is reporting, since the kubelet
	// may constantly attempt to instantiate a pod as long as it's in the pod state that we're handing to it.
	// otherwise, we're probably reporting a TASK_LOST prematurely. Should probably consult RestartPolicy to
	// determine appropriate behavior. Should probably also gracefully handle docker daemon restarts.
	taskId := taskInfo.GetTaskId().GetValue()
	if _, ok := k.tasks[taskId]; ok {
		if isKnownPod() {
			return false
		} else {
			log.Warningf("Detected lost pod, reporting lost task %v", taskId)
			k.reportLostTask(driver, taskId, messages.ContainersDisappeared)
		}
	} else {
		log.V(2).Infof("Task %v no longer registered, stop monitoring for lost pods", taskId)
	}
	return true
}

// KillTask is called when the executor receives a request to kill a task.
func (k *KubernetesExecutor) KillTask(driver edriver.ExecutorDriver, taskId *mesos.TaskID) {
	k.lock.Lock()
	defer k.lock.Unlock()

	log.Infof("Kill task %v\n", taskId)

	if !k.registered {
		//TODO(jdefelice) sent TASK_LOST here?
		log.Warningf("Ignore kill task because the executor is disconnected\n")
		return
	}

	k.killPodForTask(driver, taskId.GetValue(), messages.TaskKilled)
}

// Kills the pod associated with the given task. Assumes that the caller is locking around
// pod and task storage.
func (k *KubernetesExecutor) killPodForTask(driver edriver.ExecutorDriver, tid, reason string) {
	task, ok := k.tasks[tid]
	if !ok {
		log.Infof("Failed to kill task, unknown task %v\n", tid)
		return
	}
	delete(k.tasks, tid)

	pid := task.podName
	if _, found := k.pods[pid]; !found {
		log.Warningf("Cannot remove Unknown pod %v for task %v", pid, tid)
	} else {
		log.V(2).Infof("Deleting pod %v for task %v", pid, tid)
		delete(k.pods, pid)

		// Send the pod updates to the channel.
		update := kubelet.PodUpdate{Op: kubelet.SET}
		for _, p := range k.pods {
			update.Pods = append(update.Pods, *p)
		}
		k.updateChan <- update
	}
	// TODO(yifan): Check the result of the kill event.

	k.sendStatusUpdate(driver, task.mesosTaskInfo.GetTaskId(), mesos.TaskState_TASK_KILLED, reason)
}

// Reports a lost task to the slave and updates internal task and pod tracking state.
// Assumes that the caller is locking around pod and task state.
func (k *KubernetesExecutor) reportLostTask(driver edriver.ExecutorDriver, tid, reason string) {
	task, ok := k.tasks[tid]
	if !ok {
		log.Infof("Failed to report lost task, unknown task %v\n", tid)
		return
	}
	delete(k.tasks, tid)

	pid := task.podName
	if _, found := k.pods[pid]; !found {
		log.Warningf("Cannot remove Unknown pod %v for lost task %v", pid, tid)
	} else {
		log.V(2).Infof("Deleting pod %v for lost task %v", pid, tid)
		delete(k.pods, pid)

		// Send the pod updates to the channel.
		update := kubelet.PodUpdate{Op: kubelet.SET}
		for _, p := range k.pods {
			update.Pods = append(update.Pods, *p)
		}
		k.updateChan <- update
	}
	// TODO(yifan): Check the result of the kill event.

	k.sendStatusUpdate(driver, task.mesosTaskInfo.GetTaskId(), mesos.TaskState_TASK_LOST, reason)
}

// FrameworkMessage is called when the framework sends some message to the executor
func (k *KubernetesExecutor) FrameworkMessage(driver edriver.ExecutorDriver, message string) {
	log.Infof("Receives message from framework %v\n", message)
	// TODO(yifan): Check for update message.
}

// Shutdown is called when the executor receives a shutdown request.
func (k *KubernetesExecutor) Shutdown(driver edriver.ExecutorDriver) {
	log.Infof("Shutdown the executor\n")

	k.lock.Lock()
	defer k.lock.Unlock()

	for tid, _ := range k.tasks {
		k.killPodForTask(driver, tid, messages.ExecutorShutdown)
	}
}

// Error is called when some error happens.
func (k *KubernetesExecutor) Error(driver edriver.ExecutorDriver, message string) {
	log.Errorf("Executor error: %v\n", message)
}

func (k *KubernetesExecutor) sendStatusUpdate(driver edriver.ExecutorDriver, taskId *mesos.TaskID, state mesos.TaskState, message string) {
	statusUpdate := &mesos.TaskStatus{
		TaskId:  taskId,
		State:   &state,
		Message: proto.String(message),
	}
	// TODO(yifan): Maybe try to resend again in the future.
	if _, err := driver.SendStatusUpdate(statusUpdate); err != nil {
		log.Warningf("Failed to send status update%v, %v", err)
	}
}
