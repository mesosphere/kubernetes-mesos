package proc

import (
	"sync"
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
	go func() {
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
	}()

	// propagate actions from the backlog
	go func() {
		for {
			select {
			case <-self.terminate:
				return
			case action, ok := <-self.backlog:
				if !ok {
					return
				}
				select {
				case <-self.terminate:
					return
				case self.exec <- action:
				}
			}
		}
	}()
}

// execute some action in the context of the current lifecycle. actions
// executed via this func are to be executed in a concurrency-safe manner:
// no two actions should execute at the same time. invocations of this func
// will block until the given action is executed. invocations of this func
// may take higher priority than invocations of DoLater.
//
// returns errProcessTerminated if the process already ended, or is terminated
// during the exection of the action. if the process terminates at the same
// time as the action completes it is still possible for the action to have
// completed and still receive errProcessTerminated.
func (self *procImpl) DoNow(a Action) error {
	ch := make(chan struct{})
	wrapped := Action(func() {
		defer close(ch)
		a()
	})

	select {
	case <-self.terminate:
		return errProcessTerminated
	// wait for action to begin executing
	case self.exec <- wrapped:
	}

	select {
	case <-self.terminate:
		return errProcessTerminated
	// wait for completion of action
	case <-ch:
	}
	return nil
}

// execute some action in the context of the current lifecycle. actions
// executed via this func are to be executed in a concurrency-safe manner:
// no two actions should execute at the same time. invocations of this func
// should never block.
// returns errProcessTerminated if the process already ended.
func (self *procImpl) DoLater(a Action) (err error) {
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

		// flush the exec chan
		select {
		case <-self.exec:
			// discard it
		default:
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
	ch := make(chan error, 1)
	err := p.parent.Do(func() {
		ch <- p.delegate.Do(a)
	})
	if err != nil {
		return err
	}
	return <-ch
}

func (p *processAdapter) End() {
	p.parent.End()
}

func (p *processAdapter) Done() <-chan struct{} {
	return p.parent.Done()
}

// returns a process that, within its execution context, delegates to the specified Doer
func DoWith(other Process, d Doer) Process {
	return &processAdapter{
		parent:   other,
		delegate: d,
	}
}
