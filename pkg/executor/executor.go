package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/container"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/dockertools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
	"github.com/fsouza/go-dockerclient"
	"github.com/gogo/protobuf/proto"
	log "github.com/golang/glog"
	bindings "github.com/mesos/mesos-go/executor"
	mesos "github.com/mesos/mesos-go/mesosproto"
	mutil "github.com/mesos/mesos-go/mesosutil"
	"github.com/mesosphere/kubernetes-mesos/pkg/executor/messages"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/meta"
)

const (
	containerPollTime = 300 * time.Millisecond
	launchGracePeriod = 5 * time.Minute
)

type stateType int32

const (
	disconnectedState stateType = iota
	connectedState
	suicidalState
	terminalState
)

func (s *stateType) get() stateType {
	return stateType(atomic.LoadInt32((*int32)(s)))
}

func (s *stateType) transition(from, to stateType) bool {
	return atomic.CompareAndSwapInt32((*int32)(s), int32(from), int32(to))
}

func (s *stateType) transitionTo(to stateType, unless ...stateType) bool {
	if len(unless) == 0 {
		atomic.StoreInt32((*int32)(s), int32(to))
		return true
	}
	for {
		state := s.get()
		for _, x := range unless {
			if state == x {
				return false
			}
		}
		if s.transition(state, to) {
			return true
		}
	}
}

type kuberTask struct {
	mesosTaskInfo *mesos.TaskInfo
	podName       string
}

// func that attempts suicide
type jumper func(bindings.ExecutorDriver, <-chan struct{})

type suicideWatcher interface {
	Next(time.Duration, bindings.ExecutorDriver, jumper) suicideWatcher
	Reset(time.Duration) bool
	Stop() bool
}

// KubernetesExecutor is an mesos executor that runs pods
// in a minion machine.
type KubernetesExecutor struct {
	kl                  *kubelet.Kubelet // the kubelet instance.
	updateChan          chan<- interface{}
	state               stateType
	tasks               map[string]*kuberTask
	pods                map[string]*api.Pod
	lock                sync.RWMutex
	sourcename          string
	client              *client.Client
	events              <-chan watch.Event
	done                chan struct{} // signals shutdown
	outgoing            chan func() (mesos.Status, error)
	dockerClient        dockertools.DockerInterface
	suicideWatch        suicideWatcher
	suicideTimeout      time.Duration
	kubeletFinished     <-chan struct{} // signals that kubelet Run() died
	initialRegistration sync.Once
}

type Config struct {
	Kubelet         *kubelet.Kubelet
	Updates         chan<- interface{}
	SourceName      string
	APIClient       *client.Client
	Watch           watch.Interface
	Docker          dockertools.DockerInterface
	SuicideTimeout  time.Duration
	KubeletFinished <-chan struct{}
}

func (k *KubernetesExecutor) isConnected() bool {
	return connectedState == (&k.state).get()
}

// New creates a new kubernetes executor.
func New(config Config) *KubernetesExecutor {
	k := &KubernetesExecutor{
		kl:              config.Kubelet,
		updateChan:      config.Updates,
		state:           disconnectedState,
		tasks:           make(map[string]*kuberTask),
		pods:            make(map[string]*api.Pod),
		sourcename:      config.SourceName,
		client:          config.APIClient,
		done:            make(chan struct{}),
		outgoing:        make(chan func() (mesos.Status, error), 1024),
		dockerClient:    config.Docker,
		suicideTimeout:  config.SuicideTimeout,
		kubeletFinished: config.KubeletFinished,
		suicideWatch:    &suicideTimer{},
	}
	//TODO(jdef) do something real with these events..
	if config.Watch != nil {
		events := config.Watch.ResultChan()
		if events != nil {
			go func() {
				for e := range events {
					// e ~= watch.Event { ADDED, *api.Event }
					log.V(1).Info(e)
				}
			}()
			k.events = events
		}
	}
	return k
}

func (k *KubernetesExecutor) Init(driver bindings.ExecutorDriver) {
	k.killKubeletContainers()
	k.resetSuicideWatch(driver)
	go k.sendLoop()
	//TODO(jdef) monitor kubeletFinished and shutdown if it happens
}

func (k *KubernetesExecutor) Done() <-chan struct{} {
	return k.done
}

func (k *KubernetesExecutor) isDone() bool {
	select {
	case <-k.done:
		return true
	default:
		return false
	}
}

// Registered is called when the executor is successfully registered with the slave.
func (k *KubernetesExecutor) Registered(driver bindings.ExecutorDriver,
	executorInfo *mesos.ExecutorInfo, frameworkInfo *mesos.FrameworkInfo, slaveInfo *mesos.SlaveInfo) {
	if k.isDone() {
		return
	}
	log.Infof("Executor %v of framework %v registered with slave %v\n",
		executorInfo, frameworkInfo, slaveInfo)
	if !(&k.state).transition(disconnectedState, connectedState) {
		log.Errorf("failed to register/transition to a connected state")
	}
	k.initialRegistration.Do(k.onInitialRegistration)
}

// Reregistered is called when the executor is successfully re-registered with the slave.
// This can happen when the slave fails over.
func (k *KubernetesExecutor) Reregistered(driver bindings.ExecutorDriver, slaveInfo *mesos.SlaveInfo) {
	if k.isDone() {
		return
	}
	log.Infof("Reregistered with slave %v\n", slaveInfo)
	if !(&k.state).transition(disconnectedState, connectedState) {
		log.Errorf("failed to reregister/transition to a connected state")
	}
	k.initialRegistration.Do(k.onInitialRegistration)
}

func (k *KubernetesExecutor) onInitialRegistration() {
	// emit an empty update to allow the mesos "source" to be marked as seen
	k.updateChan <- kubelet.PodUpdate{
		Pods:   []api.Pod{},
		Op:     kubelet.SET,
		Source: k.sourcename,
	}
}

// Disconnected is called when the executor is disconnected with the slave.
func (k *KubernetesExecutor) Disconnected(driver bindings.ExecutorDriver) {
	if k.isDone() {
		return
	}
	log.Infof("Slave is disconnected\n")
	if !(&k.state).transition(connectedState, disconnectedState) {
		log.Errorf("failed to disconnect/transition to a disconnected state")
	}
}

// LaunchTask is called when the executor receives a request to launch a task.
func (k *KubernetesExecutor) LaunchTask(driver bindings.ExecutorDriver, taskInfo *mesos.TaskInfo) {
	if k.isDone() {
		return
	}
	log.Infof("Launch task %v\n", taskInfo)

	if !k.isConnected() {
		log.Errorf("Ignore launch task because the executor is disconnected\n")
		k.sendStatus(driver, newStatus(taskInfo.GetTaskId(), mesos.TaskState_TASK_FAILED,
			messages.ExecutorUnregistered))
		return
	}

	obj, err := api.Codec.Decode(taskInfo.GetData())
	if err != nil {
		log.Errorf("failed to extract yaml data from the taskInfo.data %v", err)
		k.sendStatus(driver, newStatus(taskInfo.GetTaskId(), mesos.TaskState_TASK_FAILED,
			messages.UnmarshalTaskDataFailure))
		return
	}
	pod, ok := obj.(*api.Pod)
	if !ok {
		log.Errorf("expected *api.Pod instead of %T: %+v", pod, pod)
		k.sendStatus(driver, newStatus(taskInfo.GetTaskId(), mesos.TaskState_TASK_FAILED,
			messages.UnmarshalTaskDataFailure))
		return
	}

	k.lock.Lock()
	defer k.lock.Unlock()

	taskId := taskInfo.GetTaskId().GetValue()
	if _, found := k.tasks[taskId]; found {
		log.Errorf("task already launched\n")
		// Not to send back TASK_RUNNING here, because
		// may be duplicated messages or duplicated task id.
		return
	}
	// remember this task so that:
	// (a) we ignore future launches for it
	// (b) we have a record of it so that we can kill it if needed
	// (c) we're leaving podName == "" for now, indicates we don't need to delete containers
	k.tasks[taskId] = &kuberTask{
		mesosTaskInfo: taskInfo,
	}
	k.resetSuicideWatch(driver)

	go k.launchTask(driver, taskId, pod)
}

// TODO(jdef) add metrics for this?
type suicideTimer struct {
	timer *time.Timer
}

func (w *suicideTimer) Next(d time.Duration, driver bindings.ExecutorDriver, f jumper) suicideWatcher {
	return &suicideTimer{
		timer: time.AfterFunc(d, func() {
			log.Warningf("Suicide timeout (%v) expired", d)
			f(driver, nil)
		}),
	}
}

func (w *suicideTimer) Stop() (result bool) {
	if w != nil && w.timer != nil {
		log.Infoln("stopping suicide watch") //TODO(jdef) debug
		result = w.timer.Stop()
	}
	return
}

// return true if the timer was successfully reset
func (w *suicideTimer) Reset(d time.Duration) bool {
	if w != nil && w.timer != nil {
		log.Infoln("resetting suicide watch") //TODO(jdef) debug
		w.timer.Reset(d)
		return true
	}
	return false
}

// determine whether we need to start a suicide countdown. if so, then start
// a timer that, upon expiration, causes this executor to commit suicide.
// this implementation runs asynchronously. callers that wish to wait for the
// reset to complete may wait for the returned signal chan to close.
func (k *KubernetesExecutor) resetSuicideWatch(driver bindings.ExecutorDriver) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		k.lock.Lock()
		defer k.lock.Unlock()

		if k.suicideTimeout < 1 {
			return
		}

		if k.suicideWatch != nil {
			if len(k.tasks) > 0 {
				k.suicideWatch.Stop()
				return
			}
			if k.suicideWatch.Reset(k.suicideTimeout) {
				// valid timer, reset was successful
				return
			}
		}

		//TODO(jdef) reduce verbosity here once we're convinced that suicide watch is working properly
		log.Infof("resetting suicide watch timer for %v", k.suicideTimeout)

		k.suicideWatch = k.suicideWatch.Next(k.suicideTimeout, driver, jumper(k.attemptSuicide))
	}()
	return ch
}

func (k *KubernetesExecutor) attemptSuicide(driver bindings.ExecutorDriver, abort <-chan struct{}) {
	k.lock.Lock()
	defer k.lock.Unlock()

	// this attempt may have been queued and since been aborted
	select {
	case <-abort:
		//TODO(jdef) reduce verbosity once suicide watch is working properly
		log.Infof("aborting suicide attempt since watch was cancelled")
		return
	default: // continue
	}

	// fail-safe, will abort kamikaze attempts if there are tasks
	if len(k.tasks) > 0 {
		ids := []string{}
		for taskid := range k.tasks {
			ids = append(ids, taskid)
		}
		log.Errorf("suicide attempt failed, there are still running tasks: %v", ids)
		return
	}

	log.Infoln("Attempting suicide")
	if (&k.state).transitionTo(suicidalState, suicidalState, terminalState) {
		//TODO(jdef) let the scheduler know?
		//TODO(jdef) is suicide more graceful than slave-demanded shutdown?
		k.doShutdown(driver)
	}
}

func (k *KubernetesExecutor) getPidInfo(name string) (api.PodStatus, error) {
	return k.kl.GetPodStatus(name)
}

// async continuation of LaunchTask
func (k *KubernetesExecutor) launchTask(driver bindings.ExecutorDriver, taskId string, pod *api.Pod) {

	//HACK(jdef): cloned binding construction from k8s plugin/pkg/scheduler/scheduler.go
	binding := &api.Binding{
		ObjectMeta: api.ObjectMeta{
			Namespace:   pod.Namespace,
			Name:        pod.Name,
			Annotations: make(map[string]string),
		},
		Target: api.ObjectReference{
			Kind: "Node",
			Name: pod.Annotations[meta.BindingHostKey],
		},
	}

	// forward the annotations that the scheduler wants to apply
	for k, v := range pod.Annotations {
		binding.Annotations[k] = v
	}

	deleteTask := func() {
		k.lock.Lock()
		defer k.lock.Unlock()
		delete(k.tasks, taskId)
		k.resetSuicideWatch(driver)
	}

	log.Infof("Binding '%v/%v' to '%v' with annotations %+v...", pod.Namespace, pod.Name, binding.Target.Name, binding.Annotations)
	ctx := api.WithNamespace(api.NewContext(), binding.Namespace)
	// TODO(k8s): use Pods interface for binding once clusters are upgraded
	// return b.Pods(binding.Namespace).Bind(binding)
	err := k.client.Post().Namespace(api.NamespaceValue(ctx)).Resource("bindings").Body(binding).Do().Error()
	if err != nil {
		deleteTask()
		k.sendStatus(driver, newStatus(mutil.NewTaskID(taskId), mesos.TaskState_TASK_FAILED,
			messages.CreateBindingFailure))
		return
	}

	podFullName := container.GetPodFullName(pod)

	// allow a recently failed-over scheduler the chance to recover the task/pod binding:
	// it may have failed and recovered before the apiserver is able to report the updated
	// binding information. replays of this status event will signal to the scheduler that
	// the apiserver should be up-to-date.
	data, err := json.Marshal(api.PodStatusResult{
		ObjectMeta: api.ObjectMeta{
			Name:     podFullName,
			SelfLink: "/podstatusresult",
		},
	})
	if err != nil {
		deleteTask()
		log.Errorf("failed to marshal pod status result: %v", err)
		k.sendStatus(driver, newStatus(mutil.NewTaskID(taskId), mesos.TaskState_TASK_FAILED,
			err.Error()))
		return
	}

	k.lock.Lock()
	defer k.lock.Unlock()

	// Add the task.
	task, found := k.tasks[taskId]
	if !found {
		log.V(1).Infof("task %v not found, probably killed: aborting launch, reporting lost", taskId)
		k.reportLostTask(driver, taskId, messages.LaunchTaskFailed)
		return
	}

	//TODO(jdef) check for duplicate pod name, if found send TASK_ERROR

	// from here on, we need to delete containers associated with the task
	// upon it going into a terminal state
	task.podName = podFullName
	k.pods[podFullName] = pod

	// Send the pod updates to the channel.
	update := kubelet.PodUpdate{Op: kubelet.SET}
	for _, p := range k.pods {
		update.Pods = append(update.Pods, *p)
	}
	k.updateChan <- update

	statusUpdate := &mesos.TaskStatus{
		TaskId:  mutil.NewTaskID(taskId),
		State:   mesos.TaskState_TASK_STARTING.Enum(),
		Message: proto.String(messages.CreateBindingSuccess),
		Data:    data,
	}
	k.sendStatus(driver, statusUpdate)

	// Delay reporting 'task running' until container is up.
	go k._launchTask(driver, taskId, podFullName)
}

func (k *KubernetesExecutor) _launchTask(driver bindings.ExecutorDriver, taskId, podFullName string) {

	expired := make(chan struct{})
	time.AfterFunc(launchGracePeriod, func() { close(expired) })

	getMarshalledInfo := func() (data []byte, cancel bool) {
		// potentially long call..
		if podStatus, err := k.getPidInfo(podFullName); err == nil {
			select {
			case <-expired:
				cancel = true
			default:
				k.lock.Lock()
				defer k.lock.Unlock()
				if _, found := k.tasks[taskId]; !found {
					// don't bother with the pod status if the task is already gone
					cancel = true
					break
				} else if podStatus.Phase != api.PodRunning {
					// avoid sending back a running status before it's really running
					break
				}
				log.V(2).Infof("Found pod status: '%v'", podStatus)
				result := api.PodStatusResult{
					ObjectMeta: api.ObjectMeta{
						Name:     podFullName,
						SelfLink: "/podstatusresult",
					},
					Status: podStatus,
				}
				if data, err = json.Marshal(result); err != nil {
					log.Errorf("failed to marshal pod status result: %v", err)
				}
			}
		}
		return
	}

waitForRunningPod:
	for {
		select {
		case <-expired:
			log.Warningf("Launch expired grace period of '%v'", launchGracePeriod)
			break waitForRunningPod
		case <-time.After(containerPollTime):
			if data, cancel := getMarshalledInfo(); cancel {
				break waitForRunningPod
			} else if data == nil {
				continue waitForRunningPod
			} else {
				k.lock.Lock()
				defer k.lock.Unlock()
				if _, found := k.tasks[taskId]; !found {
					goto reportLost
				}

				statusUpdate := &mesos.TaskStatus{
					TaskId:  mutil.NewTaskID(taskId),
					State:   mesos.TaskState_TASK_RUNNING.Enum(),
					Message: proto.String(fmt.Sprintf("pod-running:%s", podFullName)),
					Data:    data,
				}

				k.sendStatus(driver, statusUpdate)

				// continue to monitor the health of the pod
				go k.__launchTask(driver, taskId, podFullName)
				return
			}
		}
	}

	k.lock.Lock()
	defer k.lock.Unlock()
reportLost:
	k.reportLostTask(driver, taskId, messages.LaunchTaskFailed)
}

func (k *KubernetesExecutor) __launchTask(driver bindings.ExecutorDriver, taskId, podFullName string) {
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
		if k.checkForLostPodTask(driver, taskId, knownPod) {
			return
		}
	}
}

// Intended to be executed as part of the pod monitoring loop, this fn (ultimately) checks with Docker
// whether the pod is running. It will only return false if the task is still registered and the pod is
// registered in Docker. Otherwise it returns true. If there's still a task record on file, but no pod
// in Docker, then we'll also send a TASK_LOST event.
func (k *KubernetesExecutor) checkForLostPodTask(driver bindings.ExecutorDriver, taskId string, isKnownPod func() bool) bool {
	// TODO (jdefelice) don't send false alarms for deleted pods (KILLED tasks)
	k.lock.Lock()
	defer k.lock.Unlock()

	// TODO(jdef) we should really consider k.pods here, along with what docker is reporting, since the
	// kubelet may constantly attempt to instantiate a pod as long as it's in the pod state that we're
	// handing to it. otherwise, we're probably reporting a TASK_LOST prematurely. Should probably
	// consult RestartPolicy to determine appropriate behavior. Should probably also gracefully handle
	// docker daemon restarts.
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
func (k *KubernetesExecutor) KillTask(driver bindings.ExecutorDriver, taskId *mesos.TaskID) {
	if k.isDone() {
		return
	}
	log.Infof("Kill task %v\n", taskId)

	if !k.isConnected() {
		//TODO(jdefelice) sent TASK_LOST here?
		log.Warningf("Ignore kill task because the executor is disconnected\n")
		return
	}

	k.lock.Lock()
	defer k.lock.Unlock()
	k.removePodTask(driver, taskId.GetValue(), messages.TaskKilled, mesos.TaskState_TASK_KILLED)
}

// Reports a lost task to the slave and updates internal task and pod tracking state.
// Assumes that the caller is locking around pod and task state.
func (k *KubernetesExecutor) reportLostTask(driver bindings.ExecutorDriver, tid, reason string) {
	k.removePodTask(driver, tid, reason, mesos.TaskState_TASK_LOST)
}

// deletes the pod and task associated with the task identified by tid and sends a task
// status update to mesos. also attempts to reset the suicide watch.
// Assumes that the caller is locking around pod and task state.
func (k *KubernetesExecutor) removePodTask(driver bindings.ExecutorDriver, tid, reason string, state mesos.TaskState) {
	task, ok := k.tasks[tid]
	if !ok {
		log.V(1).Infof("Failed to remove task, unknown task %v\n", tid)
		return
	}
	delete(k.tasks, tid)
	k.resetSuicideWatch(driver)

	pid := task.podName
	if _, found := k.pods[pid]; !found {
		log.Warningf("Cannot remove unknown pod %v for task %v", pid, tid)
	} else {
		log.V(2).Infof("deleting pod %v for task %v", pid, tid)
		delete(k.pods, pid)

		// Send the pod updates to the channel.
		update := kubelet.PodUpdate{Op: kubelet.SET}
		for _, p := range k.pods {
			update.Pods = append(update.Pods, *p)
		}
		k.updateChan <- update
	}
	// TODO(jdef): ensure that the update propagates, perhaps return a signal chan?
	k.sendStatus(driver, newStatus(mutil.NewTaskID(tid), state, reason))
}

// FrameworkMessage is called when the framework sends some message to the executor
func (k *KubernetesExecutor) FrameworkMessage(driver bindings.ExecutorDriver, message string) {
	if k.isDone() {
		return
	}
	if !k.isConnected() {
		log.Warningf("Ignore framework message because the executor is disconnected\n")
		return
	}

	log.Infof("Receives message from framework %v\n", message)
	//TODO(jdef) master reported a lost task, reconcile this! @see scheduler.go:handleTaskLost
	if strings.HasPrefix("task-lost:", message) && len(message) > 10 {
		taskId := message[10:]
		if taskId != "" {
			// clean up pod state
			k.lock.Lock()
			defer k.lock.Unlock()
			k.reportLostTask(driver, taskId, messages.TaskLostAck)
		}
	}

	switch message {
	case messages.Kamikaze:
		k.attemptSuicide(driver, nil)
	}
}

// Shutdown is called when the executor receives a shutdown request.
func (k *KubernetesExecutor) Shutdown(driver bindings.ExecutorDriver) {
	k.lock.Lock()
	defer k.lock.Unlock()
	k.doShutdown(driver)
}

// assumes that caller has obtained state lock
func (k *KubernetesExecutor) doShutdown(driver bindings.ExecutorDriver) {
	defer func() {
		log.Exit("exiting with unclean shutdown: %v", recover())
	}()

	(&k.state).transitionTo(terminalState)

	// signal to all listeners that this KubeletExecutor is done!
	close(k.done)

	log.Infoln("Stopping executor driver")
	_, err := driver.Stop()
	if err != nil {
		log.Warningf("failed to stop executor driver: %v", err)
	}

	log.Infoln("Shutdown the executor")

	// according to docs, mesos will generate TASK_LOST updates for us
	// if needed, so don't take extra time to do that here.
	k.tasks = map[string]*kuberTask{}

	select {
	// the main Run() func may still be running... wait for it to finish: it will
	// clear the pod configuration cleanly, telling k8s "there are no pods" and
	// clean up resources (pods, volumes, etc).
	case <-k.kubeletFinished:

	//TODO(jdef) attempt to wait for events to propagate to API server?

	// TODO(jdef) extract constant, should be smaller than whatever the
	// slave graceful shutdown timeout period is.
	case <-time.After(15 * time.Second):
		log.Errorf("timed out waiting for kubelet Run() to die")
	}

	log.Infoln("exiting")
	os.Exit(0)
}

// Destroy existing k8s containers
func (k *KubernetesExecutor) killKubeletContainers() {
	if containers, err := dockertools.GetKubeletDockerContainers(k.dockerClient, true); err == nil {
		opts := docker.RemoveContainerOptions{
			RemoveVolumes: true,
			Force:         true,
		}
		for _, container := range containers {
			opts.ID = container.ID
			log.V(2).Infof("Removing container: %v", opts.ID)
			if err := k.dockerClient.RemoveContainer(opts); err != nil {
				log.Warning(err)
			}
		}
	} else {
		log.Warningf("Failed to list kubelet docker containers: %v", err)
	}
}

// Error is called when some error happens.
func (k *KubernetesExecutor) Error(driver bindings.ExecutorDriver, message string) {
	log.Errorln(message)
}

func newStatus(taskId *mesos.TaskID, state mesos.TaskState, message string) *mesos.TaskStatus {
	return &mesos.TaskStatus{
		TaskId:  taskId,
		State:   &state,
		Message: proto.String(message),
	}
}

func (k *KubernetesExecutor) sendStatus(driver bindings.ExecutorDriver, status *mesos.TaskStatus) {
	select {
	case <-k.done:
	default:
		k.outgoing <- func() (mesos.Status, error) { return driver.SendStatusUpdate(status) }
	}
}

func (k *KubernetesExecutor) sendFrameworkMessage(driver bindings.ExecutorDriver, msg string) {
	select {
	case <-k.done:
	default:
		k.outgoing <- func() (mesos.Status, error) { return driver.SendFrameworkMessage(msg) }
	}
}

func (k *KubernetesExecutor) sendLoop() {
	defer log.V(1).Info("sender loop exiting")
	for {
		select {
		case <-k.done:
			return
		default:
			if !k.isConnected() {
				select {
				case <-k.done:
				case <-time.After(1 * time.Second):
				}
				continue
			}
			sender, ok := <-k.outgoing
			if !ok {
				// programming error
				panic("someone closed the outgoing channel")
			}
			if status, err := sender(); err == nil {
				continue
			} else {
				log.Error(err)
				if status == mesos.Status_DRIVER_ABORTED {
					return
				}
			}
			// attempt to re-queue the sender
			select {
			case <-k.done:
			case k.outgoing <- sender:
			}
		}
	}
}
