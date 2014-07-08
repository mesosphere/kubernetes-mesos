package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"code.google.com/p/goprotobuf/proto"
	"github.com/mesosphere/mesos-go/mesos"
)

type ExampleScheduler struct {
	taskLimit int
	taskId    int
	exit      chan struct{}
}

func NewExampleScheduler() *ExampleScheduler {
	return &ExampleScheduler{
		taskLimit: 5,
		taskId:    0,
		exit:      make(chan struct{}),
	}
}

var executor *mesos.ExecutorInfo

func (s *ExampleScheduler) ResourceOffers(driver mesos.SchedulerDriver, offers []*mesos.Offer) {
	for _, offer := range offers {
		s.taskId++
		fmt.Printf("Launching task: %d\n", s.taskId)

		tasks := []*mesos.TaskInfo{
			&mesos.TaskInfo{
				Name: proto.String("go-task"),
				TaskId: &mesos.TaskID{
					Value: proto.String("go-task-" + strconv.Itoa(s.taskId)),
				},
				SlaveId:  offer.SlaveId,
				Executor: executor,
				Resources: []*mesos.Resource{
					mesos.ScalarResource("cpus", 1),
					mesos.ScalarResource("mem", 512),
				},
			},
		}

		driver.LaunchTasks(offer.Id, tasks, nil)
	}
	return
}

func (s *ExampleScheduler) StatusUpdate(driver mesos.SchedulerDriver, status *mesos.TaskStatus) {
	fmt.Println("Received task status: " + *status.Message)

	if *status.State == mesos.TaskState_TASK_FINISHED {
		s.taskLimit--
		if s.taskLimit <= 0 {
			close(s.exit)
		}
	}
	return
}
func (s *ExampleScheduler) Registered(mesos.SchedulerDriver, *mesos.FrameworkID, *mesos.MasterInfo) {}

func (s *ExampleScheduler) Reregistered(mesos.SchedulerDriver, *mesos.MasterInfo) {}

func (s *ExampleScheduler) Disconnected(mesos.SchedulerDriver) {}

func (s *ExampleScheduler) OfferRescinded(mesos.SchedulerDriver, *mesos.OfferID) {}

func (s *ExampleScheduler) SlaveLost(mesos.SchedulerDriver, *mesos.SlaveID) {}

func (s *ExampleScheduler) Error(mesos.SchedulerDriver, string) {}

func (s *ExampleScheduler) FrameworkMessage(mesos.SchedulerDriver, *mesos.ExecutorID, *mesos.SlaveID, string) {
}

func (s *ExampleScheduler) ExecutorLost(mesos.SchedulerDriver, *mesos.ExecutorID, *mesos.SlaveID, int) {
}

func main() {
	localExecutor, _ := executorPath()

	master := flag.String("master", "localhost:5050", "Location of leading Mesos master")
	executorUri := flag.String("executor-uri", localExecutor, "URI of executor executable")
	flag.Parse()

	executor = &mesos.ExecutorInfo{
		ExecutorId: &mesos.ExecutorID{Value: proto.String("default")},
		Command: &mesos.CommandInfo{
			Value: proto.String("./example_executor"),
			Uris: []*mesos.CommandInfo_URI{
				&mesos.CommandInfo_URI{Value: executorUri},
			},
		},
		Name:   proto.String("Test Executor (Go)"),
		Source: proto.String("go_test"),
	}

	exampleScheduler := NewExampleScheduler()
	driver := &mesos.MesosSchedulerDriver{
		Master: *master,
		Framework: mesos.FrameworkInfo{
			Name: proto.String("GoFramework"),
			User: proto.String(""),
		},

		Scheduler: exampleScheduler,
	}

	driver.Init()
	defer driver.Destroy()

	driver.Start()
	<-exampleScheduler.exit
	driver.Stop(false)
}

func executorPath() (string, error) {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		return "", err
	}

	path := dir + "/example_executor"
	return path, nil
}
