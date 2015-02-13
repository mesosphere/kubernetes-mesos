package scheduler

import (
	"fmt"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/mesosphere/kubernetes-mesos/pkg/queue"
)

// wrapper for the k8s pod type so that we can define additional methods on a "pod"
type Pod struct {
	*api.Pod
	deadline *time.Time
	delay    *time.Duration
	notify   queue.BreakChan
}

// implements Copyable
func (p *Pod) Copy() queue.Copyable {
	if p == nil {
		return nil
	}
	//TODO(jdef) we may need a better "deep-copy" implementation
	pod := *(p.Pod)
	return &Pod{Pod: &pod}
}

// implements Unique
func (p *Pod) GetUID() string {
	return p.UID
}

// implements Deadlined
func (dp *Pod) Deadline() (time.Time, bool) {
	if dp.deadline != nil {
		return *(dp.deadline), true
	}
	return time.Time{}, false
}

func (dp *Pod) GetDelay() time.Duration {
	if dp.delay != nil {
		return *(dp.delay)
	}
	return 0
}

func (p *Pod) Breaker() queue.BreakChan {
	return p.notify
}

func (p *Pod) String() string {
	displayDeadline := "<none>"
	if deadline, ok := p.Deadline(); ok {
		displayDeadline = deadline.String()
	}
	return fmt.Sprintf("{pod:%v, deadline:%v, delay:%v}", p.Pod.Name, displayDeadline, p.GetDelay())
}
