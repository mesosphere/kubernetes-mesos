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
	// if the action processor crashes (if some Action panics) then we
	// wait this long before spinning up the action processor again.
	defaultActionHandlerCrashDelay = 100 * time.Millisecond

	// how many actions we can store in the backlog
	defaultActionQueueDepth = 1024
)

type procImpl struct {
	Config
	backlog   chan Action    // action queue
	terminate chan struct{}  // signaled via close()
	wg        sync.WaitGroup // End() terminates when the wait is over
	done      runtime.Signal
	state     *stateType
	pid       uint32
	writeLock sync.Mutex    // avoid data race between write and close of backlog
	changed   *sync.Cond    // wait/signal for backlog changes
	engine    DoerFunc      // isolated this for easier unit testing later on
	running   chan struct{} // closes once event loop processing starts
}

type Config struct {
	// cooldown period in between deferred action crashes
	actionHandlerCrashDelay time.Duration

	// determines the size of the deferred action backlog
	actionQueueDepth uint32
}

var (
	defaultConfig = Config{
		actionHandlerCrashDelay: defaultActionHandlerCrashDelay,
		actionQueueDepth:        defaultActionQueueDepth,
	}
	pid           uint32
	closedErrChan <-chan error
)

func init() {
	ch := make(chan error)
	close(ch)
	closedErrChan = ch
}

func New() Process {
	return newConfigured(defaultConfig)
}

func newConfigured(config Config) Process {
	state := stateNew
	pi := &procImpl{
		Config:    config,
		backlog:   make(chan Action, config.actionQueueDepth),
		terminate: make(chan struct{}),
		state:     &state,
		pid:       atomic.AddUint32(&pid, 1),
		running:   make(chan struct{}),
	}
	pi.engine = DoerFunc(pi.doLater)
	pi.changed = sync.NewCond(&pi.writeLock)
	pi.wg.Add(1) // symmetrical to wg.Done() in End()
	pi.done = pi.begin()
	return pi
}

// returns a chan that closes upon termination of the action processing loop
func (self *procImpl) Done() <-chan struct{} {
	return self.done
}

func (self *procImpl) Running() <-chan struct{} {
	return self.running
}

func (self *procImpl) begin() runtime.Signal {
	if !self.state.transition(stateNew, stateRunning) {
		panic(fmt.Errorf("failed to transition from New to Idle state"))
	}
	defer log.V(2).Infof("started process %d", self.pid)
	var entered runtime.Latch
	// execute actions on the backlog chan
	return runtime.Go(func() {
		runtime.Until(func() {
			if entered.Acquire() {
				close(self.running)
				self.wg.Add(1)
			}
			for action := range self.backlog {
				select {
				case <-self.terminate:
					return
				default:
					// signal to indicate there's room in the backlog now
					self.changed.Broadcast()
					// rely on Until to handle action panics
					action()
				}
			}
		}, self.actionHandlerCrashDelay, self.terminate)
	}).Then(func() {
		log.V(2).Infof("finished processing action backlog for process %d", self.pid)
		if !entered.Acquire() {
			self.wg.Done()
		}
	})
}

// execute some action in the context of the current lifecycle. actions
// executed via this func are to be executed in a concurrency-safe manner:
// no two actions should execute at the same time. invocations of this func
// should not block for very long, unless the action backlog is full or the
// process is terminating.
// returns errProcessTerminated if the process already ended.
func (self *procImpl) doLater(deferredAction Action) (err <-chan error) {
	a := Action(func() {
		self.wg.Add(1)
		defer self.wg.Done()
		deferredAction()
	})

	scheduled := false
	self.writeLock.Lock()
	defer self.writeLock.Unlock()

	for err == nil && !scheduled {
		switch s := self.state.get(); s {
		case stateRunning:
			select {
			case self.backlog <- a:
				scheduled = true
			default:
				self.changed.Wait()
			}
		case stateTerminal:
			err = ErrorChan(errProcessTerminated)
		default:
			err = ErrorChan(errIllegalState)
		}
	}
	return
}

// implementation of Doer interface, schedules some action to be executed via
// the current execution engine
func (self *procImpl) Do(a Action) <-chan error {
	return self.engine(a)
}

// spawn a goroutine that waits for an error. if a non-nil error is read from the
// channel then the handler func is invoked, otherwise (nil error or closed chan)
// the handler is skipped. if a nil handler is specified then it's not invoked.
// the signal chan that's returned closes once the error process logic (and handler,
// if any) has completed.
func OnError(ch <-chan error, f func(error), abort <-chan struct{}) <-chan struct{} {
	return runtime.Go(func() {
		if ch == nil {
			return
		}
		select {
		case err, ok := <-ch:
			if ok && err != nil && f != nil {
				f(err)
			}
		case <-abort:
			if f != nil {
				f(errProcessTerminated)
			}
		}
	})
}

func (self *procImpl) OnError(ch <-chan error, f func(error)) <-chan struct{} {
	return OnError(ch, f, self.Done())
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
		self.writeLock.Lock()
		defer self.writeLock.Unlock()

		log.V(2).Infof("terminating process %d", self.pid)

		close(self.backlog)
		close(self.terminate)
		self.wg.Done()
		self.changed.Broadcast()

		log.V(2).Infof("waiting for deferred actions to complete")

		// wait for all pending actions to complete, then flush the backlog
		self.wg.Wait()
		self.flush()
	}
}

type errorOnce struct {
	once  sync.Once
	err   chan error
	abort <-chan struct{}
}

func NewErrorOnce(abort <-chan struct{}) ErrorOnce {
	return &errorOnce{
		err:   make(chan error, 1),
		abort: abort,
	}
}

func (b *errorOnce) Err() <-chan error {
	return b.err
}

func (b *errorOnce) Report(err error) {
	b.once.Do(func() {
		select {
		case b.err <- err:
		default:
		}
	})
}

func (b *errorOnce) Send(errIn <-chan error) ErrorOnce {
	go b.forward(errIn)
	return b
}

func (b *errorOnce) forward(errIn <-chan error) {
	if errIn == nil {
		b.Report(nil)
		return
	}
	select {
	case err, _ := <-errIn:
		b.Report(err)
	case <-b.abort:
		b.Report(errProcessTerminated)
	}
}

type processAdapter struct {
	parent   Process
	delegate Doer
}

func (p *processAdapter) Do(a Action) <-chan error {
	if p == nil || p.parent == nil || p.delegate == nil {
		return ErrorChan(errIllegalState)
	}
	errCh := NewErrorOnce(p.Done())
	go func() {
		errOuter := p.parent.Do(func() {
			errInner := p.delegate.Do(a)
			errCh.forward(errInner)
		})
		// if the outer err is !nil then either the parent failed to schedule the
		// the action, or else it backgrounded the scheduling task.
		if errOuter != nil {
			errCh.forward(errOuter)
		}
	}()
	return errCh.Err()
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

func (p *processAdapter) Running() <-chan struct{} {
	if p != nil && p.parent != nil {
		return p.parent.Running()
	}
	return nil
}

func (p *processAdapter) OnError(ch <-chan error, f func(error)) <-chan struct{} {
	if p != nil && p.parent != nil {
		return p.parent.OnError(ch, f)
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

func ErrorChan(err error) <-chan error {
	if err == nil {
		return closedErrChan
	}
	ch := make(chan error, 1)
	ch <- err
	return ch
}

// invoke the f on action a. returns an illegal state error if f is nil.
func (f DoerFunc) Do(a Action) <-chan error {
	if f != nil {
		return f(a)
	}
	return ErrorChan(errIllegalState)
}
