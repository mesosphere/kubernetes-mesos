package proc

import (
	"sync"
	"time"

	log "github.com/golang/glog"
	"github.com/mesosphere/kubernetes-mesos/pkg/runtime"
)

const (
	actionHandlerCrashDelay = 100 * time.Millisecond
)

type procImpl struct {
	backlog   chan Action
	exec      chan Action   // for immediate execution
	terminate chan struct{} // signaled via close()
	termOnce  sync.Once
}

func New() ProcessInit {
	return &procImpl{
		backlog:   make(chan Action, 1024), // TODO(jdef) extract backlog
		exec:      make(chan Action),       // intentionally unbuffered
		terminate: make(chan struct{}),
	}
}

func (self *procImpl) Done() <-chan struct{} {
	return self.terminate
}

func (self *procImpl) Begin() {
	// execute actions on the exec chan
	go runtime.Until(func() {
		for {
			select {
			case <-self.terminate:
				return
			case action, ok := <-self.exec:
				if !ok {
					return
				}
				action()
			}
		}
	}, actionHandlerCrashDelay, self.terminate)

	// propagate actions from the backlog
	go runtime.Until(func() {
		done := false
		for !done {
			select {
			case <-self.terminate:
				return
			case action, ok := <-self.backlog:
				if !ok {
					return
				}
				func() {
					defer func() {
						if r := recover(); r != nil {
							defer self.End()
							done = true
							log.Errorf("attempted to write to a closed exec chan: %v", r)
						}
					}()
					select {
					case <-self.terminate:
						done = true
					case self.exec <- action:
					}
				}()
			}
		}
	}, actionHandlerCrashDelay, self.terminate)
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
			log.Errorf("attempted to write to a closed process backlog: %v", r)
		}
	}()
	select {
	case <-self.terminate:
		err = errProcessTerminated
	case self.backlog <- a:
	}
	return
}

// implementation of Doer interface, schedules some action to be executed in
// a deferred excution context via DoLater.
func (self *procImpl) Do(a Action) error {
	return self.DoLater(a)
}

func (self *procImpl) End() {
	self.termOnce.Do(func() {
		close(self.terminate)

		// flush the exec
		for _ = range self.exec {
		}

		// flush the backlog
		for _ = range self.backlog {
		}
	})
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
		e := p.delegate.Do(a)
		func() {
			defer func() {
				if r := recover(); r != nil {
					defer p.End()
					log.Errorf("failed to propagate delegation error: %v", e)
				}
			}()
			ch <- e
		}()
	})
	if err != nil {
		return err
	}
	return <-ch
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
