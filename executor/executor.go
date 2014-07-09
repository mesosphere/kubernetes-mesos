package executor

import (
	log "github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/golang/glog"
	"github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/mesosphere/mesos-go/mesos"
)

// KuberneteExecutor is an mesos executor that runs pods
// in a minion machine.
type KuberneteExecutor struct {
}

// New creates a new kubernete executor.
func New() *KuberneteExecutor {
	return &KuberneteExecutor{}
}

// Registered is called when the executor is successfully registered with the slave.
func (k *KuberneteExecutor) Registered(driver mesos.ExecutorDriver,
	executorInfo *mesos.ExecutorInfo, frameworkInfo *mesos.FrameworkInfo, slaveInfo *mesos.SlaveInfo) {
	log.Infof("Executor %v of framework %v registered with slave %v\n",
		executorInfo, frameworkInfo, slaveInfo)

	// TODO(yifan): Update the executor's status.
}

// Reregistered is called when the executor is successfully re-registered with the slave.
// This can happen when the slave fails over.
func (k *KuberneteExecutor) Reregistered(driver mesos.ExecutorDriver,
	executorInfo *mesos.ExecutorInfo, frameworkInfo *mesos.FrameworkInfo, slaveInfo *mesos.SlaveInfo) {
	log.Infof("Executor %v of framework %v reregistered with slave %v\n",
		executorInfo, frameworkInfo, slaveInfo)

	// TODO(yifan): Update the executor's status.
}

// Disconnected is called when the executor is disconnected with the slave.
func (k *KuberneteExecutor) Disconnected(driver mesos.ExecutorDriver) {
	log.Infof("Slave is disconnected\n")

	// TODO(yifan): Update the executor's status.
}

// LaunchTask is called when the executor receives a request to launch a task.
func (k *KuberneteExecutor) LaunchTask(driver mesos.ExecutorDriver, taskInfo *mesos.TaskInfo) {
	log.Infof("Launch task %v\n", taskInfo)

	// TODO(yifan): Launch a pod by update the pod manifest, and do a sync.
	// Then send back status update.
}

// KillTask is called when the executor receives a request to kill a task.
func (k *KuberneteExecutor) KillTask(driver mesos.ExecutorDriver, taskId *mesos.TaskID) {
	log.Infof("Kill task %v\n", taskId)

	// TODO(yifan): kill the container by updating the pod manifest, and do a sync.
	// Then send back status update.
}

// FrameworkMessage is called when the framework sends some message to the executor
func (k *KuberneteExecutor) FrameworkMessage(driver mesos.ExecutorDriver, message string) {
	log.Infof("Receives message from framework %v\n", message)
}

// Shutdown is called when the executor receives a shutdown request.
func (k *KuberneteExecutor) Shutdown(driver mesos.ExecutorDriver) {
	log.Infof("Shutdown the executor\n")

	// TODO(yifan): kill all running containers by updating the pod manifest, and do a sync.
	// Then send back status update.
}

// Error is called when some error happens.
func (k *KuberneteExecutor) Error(driver mesos.ExecutorDriver, message string) {
	log.Errorf("Executor error: %v\n", message)
}
