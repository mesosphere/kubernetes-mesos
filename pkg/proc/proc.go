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

func New() Process {
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

func (self *procImpl) Do(a Action) {
	ch := make(chan struct{})
	wrapped := Action(func() {
		defer close(ch)
		a()
	})

	select {
	case <-self.terminate:
		return
	// wait for action to begin executing
	case self.exec <- wrapped:
	}

	select {
	case <-self.terminate:
		return
	// wait for completion of action
	case <-ch:
	}
}

func (self *procImpl) DoLater(a Action) {
	select {
	case <-self.terminate:
		return
	case self.backlog <- a:
		// noop
	}
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
