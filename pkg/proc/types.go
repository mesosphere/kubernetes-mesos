package proc

type Action func()

type Lifecycle interface {
	// begin the lifecycle
	Begin()

	// end (terminate) the lifecycle
	End()

	// return a signal chan that will close upon the termination of this lifecycle
	Done() <-chan struct{}
}

type Doer interface {
	// execute some action in the context of the current lifecycle. actions
	// executed via this func are to be executed in a concurrency-safe manner:
	// no two actions should execute at the same time. invocations of this func
	// will block until the given action is executed. invocations of this func
	// may take higher priority than invocations of DoLater.
	Do(Action)

	// execute some action in the context of the current lifecycle. actions
	// executed via this func are to be executed in a concurrency-safe manner:
	// no two actions should execute at the same time. invocations of this func
	// should never block.
	DoLater(Action)
}

type Process interface {
	Lifecycle
	Doer
}
