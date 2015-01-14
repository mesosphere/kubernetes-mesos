package scheduler

import (
	"errors"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	algorithm "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/mesosphere/kubernetes-mesos/pkg/queue"
)

// PodScheduleFunc implements how to schedule pods among slaves.
// We can have different implementation for different scheduling policy.
//
// The Schedule function accepts a group of slaves (each contains offers from
// that slave) and a single pod, which aligns well with the k8s scheduling
// algorithm. It returns an offerId that is acceptable for the pod, otherwise
// nil. The caller is responsible for filling in task state w/ relevant offer
// details.
//
// See the FCFSScheduleFunc for example.
type PodScheduleFunc func(r OfferRegistry, slaves SlaveIndex, task *PodTask) (PerishableOffer, error)

// A minimal placeholder
type empty struct{}

type stateType int

const (
	statePending stateType = iota
	stateRunning
	stateFinished
	stateUnknown
)

var (
	noSuitableOffersErr = errors.New("No suitable offers for pod/task")
	noSuchPodErr        = errors.New("No such pod exists")
)

// wrapper for the k8s pod type so that we can define additional methods on a "pod"
type Pod struct {
	*api.Pod
	deadline *time.Time
	delay    *time.Duration
	notify   queue.BreakChan
}

// adapter for k8s pkg/scheduler/Scheduler interface
type SchedulerFunc func(api.Pod, algorithm.MinionLister) (selectedMachine string, err error)

func (f SchedulerFunc) Schedule(pod api.Pod, lister algorithm.MinionLister) (string, error) {
	return f(pod, lister)
}

type SlaveIndex interface {
	slaveFor(id string) (*Slave, bool)
}
