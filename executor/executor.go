package executor

import (
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	log "github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/golang/glog"
	"github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/mesosphere/mesos-go/mesos"
)

type kuberTask struct {
	mesosTaskInfo     *mesos.TaskInfo
	containerManifest *api.ContainerManifest
}

// KuberneteExecutor is an mesos executor that runs pods
// in a minion machine.
type KuberneteExecutor struct {
	*kubelet.Kubelet // the kubelet instance.
	registered       bool
	tasks            map[string]*kuberTask
}

// New creates a new kubernete executor.
func New() *KuberneteExecutor {
	return &KuberneteExecutor{
		kubelet.New(),
		false,
		make(map[string]*kuberTask),
	}
}

func (k *KuberneteExecutor) gatherContainerManifests() []api.ContainerManifest {
	var manifests []api.ContainerManifest
	for _, task := range k.tasks {
		manifests = append(manifests, *task.containerManifest)
	}
	return manifests
}

// Registered is called when the executor is successfully registered with the slave.
func (k *KuberneteExecutor) Registered(driver mesos.ExecutorDriver,
	executorInfo *mesos.ExecutorInfo, frameworkInfo *mesos.FrameworkInfo, slaveInfo *mesos.SlaveInfo) {
	log.Infof("Executor %v of framework %v registered with slave %v\n",
		executorInfo, frameworkInfo, slaveInfo)

	k.registered = true
}

// Reregistered is called when the executor is successfully re-registered with the slave.
// This can happen when the slave fails over.
func (k *KuberneteExecutor) Reregistered(driver mesos.ExecutorDriver,
	executorInfo *mesos.ExecutorInfo, frameworkInfo *mesos.FrameworkInfo, slaveInfo *mesos.SlaveInfo) {
	log.Infof("Executor %v of framework %v reregistered with slave %v\n",
		executorInfo, frameworkInfo, slaveInfo)

	k.registered = true
}

// Disconnected is called when the executor is disconnected with the slave.
func (k *KuberneteExecutor) Disconnected(driver mesos.ExecutorDriver) {
	log.Infof("Slave is disconnected\n")

	k.registered = false
}

// LaunchTask is called when the executor receives a request to launch a task.
func (k *KuberneteExecutor) LaunchTask(driver mesos.ExecutorDriver, taskInfo *mesos.TaskInfo) {
	log.Infof("Launch task %v\n", taskInfo)

	if !k.registered {
		// TODO(yifan): Send back status update task lost.
		return
	}

	taskId := taskInfo.GetTaskId().GetValue()
	if _, found := k.tasks[taskId]; found {
		log.Warningf("Task already launched\n")
		// Not to send back TASK_RUNNING here, because
		// may be duplicated messages or duplicated task id.
		return
	}

	// Get the container manifest from the taskInfo.
	var manifest api.ContainerManifest
	if err := k.ExtractYAMLData(taskInfo.GetData(), &manifest); err != nil {
		log.Warningf("Failed to extract yaml data from the taskInfo.data %v\n", err)

		// TODO(yifan): Send back status update task lost.
		return
	}

	// Add the task.
	k.tasks[taskId] = &kuberTask{
		mesosTaskInfo:     taskInfo,
		containerManifest: &manifest,
	}

	// Call SyncManifests to force kuberlete to run docker containers.
	if err := k.SyncManifests(k.gatherContainerManifests()); err != nil {
		log.Warningf("Failed to extract yaml data from the taskInfo.data %v\n", err)

		// TODO(yifan): Send back status update task lost.
		return
	}

	// TODO(yifan): Send back TASK_RUNNING.
}

// KillTask is called when the executor receives a request to kill a task.
func (k *KuberneteExecutor) KillTask(driver mesos.ExecutorDriver, taskId *mesos.TaskID) {
	log.Infof("Kill task %v\n", taskId)

	tid := taskId.GetValue()
	if _, ok := k.tasks[tid]; !ok {
		log.Infof("Failed to kill task, unknown task %v\n", tid)
		return
	}
	delete(k.tasks, tid)

	// Call SyncManifests to force kuberlete to kill deleted containers.
	if err := k.SyncManifests(k.gatherContainerManifests()); err != nil {
		log.Warningf("Failed to extract yaml data from the taskInfo.data %v\n", err)

		// TODO(yifan): Check the error type, (if already killed?)
		return
	}

	// TODO(yifan): Send back TASK_LOST.
}

// FrameworkMessage is called when the framework sends some message to the executor
func (k *KuberneteExecutor) FrameworkMessage(driver mesos.ExecutorDriver, message string) {
	log.Infof("Receives message from framework %v\n", message)
}

// Shutdown is called when the executor receives a shutdown request.
func (k *KuberneteExecutor) Shutdown(driver mesos.ExecutorDriver) {
	log.Infof("Shutdown the executor\n")

	for tid := range k.tasks {
		delete(k.tasks, tid)
		// Call SyncManifests to force kuberlete to kill deleted containers.
		if err := k.SyncManifests(k.gatherContainerManifests()); err != nil {
			log.Warningf("Failed to extract yaml data from the taskInfo.data %v\n", err)

			// TODO(yifan): Check the error type, (if already killed?)
		}
	}
	// Then send back TASK_LOST.
}

// Error is called when some error happens.
func (k *KuberneteExecutor) Error(driver mesos.ExecutorDriver, message string) {
	log.Errorf("Executor error: %v\n", message)
}
