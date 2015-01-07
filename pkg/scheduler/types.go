package scheduler

import (
	"errors"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
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
type PodScheduleFunc func(r OfferRegistry, slaves map[string]*Slave, task *PodTask) (PerishableOffer, error)

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
)

// wrapper for the k8s pod type so that we can define additional methods on a "pod"
type Pod struct {
	*api.Pod
}
