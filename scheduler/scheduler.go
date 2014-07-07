package main

import (
	"flag"
	"fmt"

	"github.com/mesosphere/kubernetes-mesos/3rdparty/code.google.com/p/goprotobuf/proto"
	"github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/mesosphere/mesos-go/mesos"
)

func main() {
	exit := make(chan bool)

	master := flag.String("master", "localhost:5050", "Location of leading Mesos master")
	executorUri := flag.String("executor-uri", "", "URI of executor executable")
	flag.Parse()

	executor := &mesos.ExecutorInfo{
		ExecutorId: &mesos.ExecutorID{Value: proto.String("default")},
		Command: &mesos.CommandInfo{
			Value: proto.String("./example_executor"),
			Uris: []*mesos.CommandInfo_URI{
				&mesos.CommandInfo_URI{Value: executorUri},
			},
		},
		Name:   proto.String("Kubernetes Mesos Scheduler"),
		Source: proto.String("kubernetes-mesos"),
	}

	driver := mesos.SchedulerDriver{
		Master: *master,
		Framework: mesos.FrameworkInfo{
			Name: proto.String("Kubernetes"),
			User: proto.String(""),
		},

		Scheduler: &mesos.Scheduler{
			ResourceOffers: func(driver *mesos.SchedulerDriver, offers []mesos.Offer) {
				for _, offer := range offers {
					fmt.Printf("Received offer: %d\n", offer)
					tasks := []mesos.TaskInfo{} // TODO: this better!
					// driver.LaunchTasks(offer.Id, tasks)
					driver.DeclineOffer(offer.Id)
				}
			},

			StatusUpdate: func(driver *mesos.SchedulerDriver, status mesos.TaskStatus) {
				fmt.Printf("Received status update with state [%s] and [%s]", *status.State, *status.Message)
			},
		},
	}

	driver.Init()
	defer driver.Destroy()
	driver.Start()
	<-exit
	driver.Stop(false)
}
