package proc

import (
	"fmt"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/mesosphere/kubernetes-mesos/pkg/runtime"
)

const (
	defaultActionHandlerCrashDelay = 100 * time.Millisecond
	defaultActionQueueDepth        = 1024
	defaultActionScheduleTimeout   = 2 * time.Minute // how long we should wait before giving up on scheduling an action
)

type procImpl struct {
	Config
	backlog   chan Action
	terminate chan struct{} // signaled via close()
	wg        sync.WaitGroup
	done      runtime.Signal
	state     *stateType
}

type Config struct {
	actionHandlerCrashDelay time.Duration
	actionQueueDepth        uint32
	actionScheduleTimeout   time.Duration
}

var (
	defaultConfig = Config{
		actionHandlerCrashDelay: defaultActionHandlerCrashDelay,
		actionQueueDepth:        defaultActionQueueDepth,
		actionScheduleTimeout:   defaultActionScheduleTimeout,
	}
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
	}
	// order is important here...
	pi.wg.Add(1) // corresponds to the jobs started in Begin()
	pi.done = runtime.Go(pi.wg.Wait)
	return pi
}

func (self *procImpl) Done() <-chan struct{} {
	return self.done
}

func (self *procImpl) Begin() {
	if !self.state.transition(stateNew, stateRunning) {
		util.HandleError(fmt.Errorf("failed to transition from New to Idle state"))
		return
	}
	// execute actions on the backlog chan
	runtime.Go(func() {
		runtime.Until(func() {
			for {
				select {
				case <-self.terminate:
					return
				case action, ok := <-self.backlog:
					if !ok {
						return
					}
					action()
				}
			}
		}, self.actionHandlerCrashDelay, self.terminate)
	}).Then(self.wg.Done)
}

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

// execute some action in the context of the current lifecycle. actions
// executed via this func are to be executed in a concurrency-safe manner:
// no two actions should execute at the same time. invocations of this func
// should never block.
// returns errProcessTerminated if the process already ended.
func (self *procImpl) DoLater(a Action) (err error) {
	defer func() {
		if r := recover(); r != nil {
			defer self.End()
			err = fmt.Errorf("attempted to schedule action on a closed process backlog: %v", r)
		}
	}()
	a = Action(func() {
		self.wg.Add(1)
		defer self.wg.Done()
		a()
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
// a deferred excution context via DoLater.
func (self *procImpl) Do(a Action) error {
	return self.DoLater(a)
}

func (self *procImpl) flush(ch chan Action) {
waitForDone:
	for {
		select {
		case <-self.Done():
			break waitForDone
		case <-ch: // discard all
		}
	}
	for range ch {
		// discard all
	}
}

func (self *procImpl) End() {
	if self.state.transitionTo(stateTerminal, stateTerminal) {
		close(self.backlog)
		close(self.terminate)
		go self.flush(self.backlog)
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
