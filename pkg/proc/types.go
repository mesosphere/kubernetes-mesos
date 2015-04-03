package proc

type Action func()

type Context interface {
	// end (terminate) the execution context
	End()

	// return a signal chan that will close upon the termination of this process
	Done() <-chan struct{}
}

type Doer interface {
	// execute some action in some context. actions are to be executed in a
	// concurrency-safe manner: no two actions should execute at the same time.
	// errors are generated if the action cannot be executed (not by the execution
	// of the action) and should be testable with the error API of this package,
	// for example, IsProcessTerminated.
	Do(Action) error
}

// adapter func for Doer interface
type DoerFunc func(Action) error

// invoke the f on action a. returns an illegal state error if f is nil.
func (f DoerFunc) Do(a Action) error {
	if f != nil {
		return f(a)
	}
	return errIllegalState
}

type Process interface {
	Context
	Doer
}

type ProcessInit interface {
	Process

	// begin process accounting and spawn requisite background routines
	Begin()
}
