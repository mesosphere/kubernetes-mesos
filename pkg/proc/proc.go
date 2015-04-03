package proc

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/golang/glog"
	"github.com/mesosphere/kubernetes-mesos/pkg/runtime"
)

const (
	defaultActionHandlerCrashDelay = 100 * time.Millisecond
	defaultActionQueueDepth        = 1024
	defaultActionScheduleTimeout   = 2 * time.Minute
)

type procImpl struct {
	Config
	backlog   chan Action
	terminate chan struct{} // signaled via close()
	wg        sync.WaitGroup
	done      runtime.Signal
	state     *stateType
	pid       uint32
}

type Config struct {
	// cooldown period in between deferred action crashes
	actionHandlerCrashDelay time.Duration

	// determines the size of the deferred action backlog
	actionQueueDepth uint32

	// how long we should wait before giving up on scheduling a deferred action
	actionScheduleTimeout time.Duration
}

var (
	defaultConfig = Config{
		actionHandlerCrashDelay: defaultActionHandlerCrashDelay,
		actionQueueDepth:        defaultActionQueueDepth,
		actionScheduleTimeout:   defaultActionScheduleTimeout,
	}
	pid uint32
)

func New() ProcessInit {
	return newConfigured(defaultConfig)
}

func newConfigured(config Config) ProcessInit {
	state := stateNew
	pi := &procImpl{
		Config:    config,
		backlog:   make(chan Action, config.actionQueueDepth),
		terminate: make(chan struct{}),
		state:     &state,
		pid:       atomic.AddUint32(&pid, 1),
	}
	pi.wg.Add(1) // symmetrical to wg.Done() in End()
	pi.done = runtime.Go(pi.wg.Wait)
	return pi
}

func (self *procImpl) Done() <-chan struct{} {
	return self.done
}

func (self *procImpl) Begin() {
	if !self.state.transition(stateNew, stateRunning) {
		panic(fmt.Errorf("failed to transition from New to Idle state"))
	}
	defer log.V(2).Infof("started process %d", self.pid)
	addMe := true
	finished := make(chan struct{})
	// execute actions on the backlog chan
	runtime.Go(func() {
		runtime.Until(func() {
			if addMe {
				addMe = false
				self.wg.Add(1)
			}
			for action := range self.backlog {
				// rely on Until to handle action panics
				action()
			}
			close(finished)
		}, self.actionHandlerCrashDelay, finished)
	}).Then(func() {
		log.V(2).Infof("finished processing action backlog for process %d", self.pid)
		if !addMe {
			self.wg.Done()
		}
	})
}

/*TODO(jdef) determine if we really need this

// execute some action in the context of the current lifecycle. actions
// executed via this func are to be executed in a concurrency-safe manner:
// no two actions should execute at the same time. invocations of this func
// will block until the given action is executed.
//
// returns errProcessTerminated if the process already ended, or is terminated
// before the reported completion of the executed action. if the process
// terminates at the same time as the action completes it is still possible for
// the action to have completed and still receive errProcessTerminated.
func DoAndWait(p Process, a Action) error {
	ch := make(chan struct{})
	err := p.Do(func() {
		defer close(ch)
		a()
	})
	if err != nil {
		return err
	}
	select {
	case <-p.Done():
		return errProcessTerminated
	case <-ch:
		return nil
	}
}
*/

// execute some action in the context of the current lifecycle. actions
// executed via this func are to be executed in a concurrency-safe manner:
// no two actions should execute at the same time. invocations of this func
// should never block.
// returns errProcessTerminated if the process already ended.
func (self *procImpl) doLater(deferredAction Action) (err error) {
	defer func() {
		if r := recover(); r != nil {
			defer self.End()
			err = fmt.Errorf("attempted to schedule action on a closed process backlog: %v", r)
		} else if err != nil {
			log.V(2).Infof("failed to execute action: %v", err)
		}
	}()
	a := Action(func() {
		self.wg.Add(1)
		defer self.wg.Done()
		deferredAction()
	})
	select {
	case <-self.terminate:
		err = errProcessTerminated
	case <-time.After(self.actionScheduleTimeout):
		return errActionScheduleTimeout
	case self.backlog <- a:
		// success! noop
	}
	return
}

// implementation of Doer interface, schedules some action to be executed in
// a deferred excution context via doLater.
func (self *procImpl) Do(a Action) error {
	return self.doLater(a)
}

func (self *procImpl) flush() {
	defer func() {
		log.V(2).Infof("flushed action backlog for process %d", self.pid)
	}()
	log.V(2).Infof("flushing action backlog for process %d", self.pid)
	for range self.backlog {
		// discard all
	}
}

func (self *procImpl) End() {
	if self.state.transitionTo(stateTerminal, stateTerminal) {
		log.V(2).Infof("terminating process %d", self.pid)
		close(self.backlog)
		close(self.terminate)
		self.wg.Done()
		log.V(2).Infof("waiting for deferred actions to complete")
		<-self.Done()
		self.flush()
	}
}

type processAdapter struct {
	parent   Process
	delegate Doer
}

func (p *processAdapter) Do(a Action) error {
	if p == nil || p.parent == nil || p.delegate == nil {
		return errIllegalState
	}
	ch := make(chan error, 1)
	err := p.parent.Do(func() {
		ch <- p.delegate.Do(a)
	})
	if err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-p.Done():
		<-ch
		return errProcessTerminated
	}
}

func (p *processAdapter) End() {
	if p != nil && p.parent != nil {
		p.parent.End()
	}
}

func (p *processAdapter) Done() <-chan struct{} {
	if p != nil && p.parent != nil {
		return p.parent.Done()
	}
	return nil
}

// returns a process that, within its execution context, delegates to the specified Doer.
// if the given Doer instance is nil, a valid Process is still returned though calls to its
// Do() implementation will always return errIllegalState.
// if the given Process instance is nil then in addition to the behavior in the prior sentence,
// calls to End() and Done() are effectively noops.
func DoWith(other Process, d Doer) Process {
	return &processAdapter{
		parent:   other,
		delegate: d,
	}
}
