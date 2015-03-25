package proc

type Action func()

type Context interface {
	// end (terminate) the process
	End()

	// return a signal chan that will close upon the termination of this process
	Done() <-chan struct{}
}

type Doer interface {
	// execute some action in the context of the current process. actions
	// executed via this func are to be executed in a concurrency-safe manner:
	// no two actions should execute at the same time.
	//
	// errors are generated if the action cannot be executed (not by the execution
	// of the action) and may be tested with, for example, IsActionNotAllowedError.
	Do(Action) error
}

// adapter func for Doer interface
type DoerFunc func(Action) error

func (f DoerFunc) Do(a Action) error {
	return f(a)
}

type Process interface {
	Context
	Doer
}

type ProcessInit interface {
	Process

	// begin process accounting and spawn background routines
	Begin()
}
