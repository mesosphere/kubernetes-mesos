package main

import (
	"fmt"

	"code.google.com/p/goprotobuf/proto"
	"github.com/mesosphere/mesos-go/mesos"
)

type MesosExecutor bool

func (e MesosExecutor) Registered(driver mesos.ExecutorDriver, executor *mesos.ExecutorInfo,
	framework *mesos.FrameworkInfo, slave *mesos.SlaveInfo) {
	fmt.Println("Executor registered!")
}

func (e MesosExecutor) LaunchTask(driver mesos.ExecutorDriver, taskInfo *mesos.TaskInfo) {
	fmt.Println("Launch task!")
	driver.SendStatusUpdate(&mesos.TaskStatus{
		TaskId:  taskInfo.TaskId,
		State:   mesos.NewTaskState(mesos.TaskState_TASK_RUNNING),
		Message: proto.String("Go task is running!"),
	})

	driver.SendStatusUpdate(&mesos.TaskStatus{
		TaskId:  taskInfo.TaskId,
		State:   mesos.NewTaskState(mesos.TaskState_TASK_FINISHED),
		Message: proto.String("Go task is done!"),
	})
}

func (e MesosExecutor) Disconnected(mesos.ExecutorDriver) {}

func (e MesosExecutor) Reregistered(mesos.ExecutorDriver, *mesos.SlaveInfo) {}

func (e MesosExecutor) KillTask(mesos.ExecutorDriver, *mesos.TaskID) {}

func (e MesosExecutor) FrameworkMessage(mesos.ExecutorDriver, string) {}

func (e MesosExecutor) Shutdown(mesos.ExecutorDriver) {}

func (e MesosExecutor) Error(mesos.ExecutorDriver, string) {}

func main() {
	driver := &mesos.MesosExecutorDriver{
		Executor: MesosExecutor(true),
	}

	driver.Init()
	defer driver.Destroy()

	driver.Run()
}
