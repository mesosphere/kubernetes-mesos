package proc

// something that executes in the context of a process
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
	Do(Action) <-chan error
}

// adapter func for Doer interface
type DoerFunc func(Action) <-chan error

type Process interface {
	Context
	Doer

	// see top level OnError func. this implementation will terminate upon the arrival of
	// an error (and subsequently invoke the error handler, if given) or else the termination
	// of the process (testable via IsProcessTerminated).
	OnError(<-chan error, func(error)) <-chan struct{}

	// return a signal chan that will close once the process is ready to run actions
	Running() <-chan struct{}
}

// this is an error promise. if we ever start building out support for other promise types it will probably
// make sense to group them in some sort of "promises" package.
type ErrorOnce interface {
	// return a chan that only ever sends one error, either obtained via Report() or Forward()
	Err() <-chan error

	// reports the given error via Err(), but only if no other errors have been reported or forwarded
	Report(error)
	Reportf(string, ...interface{})

	// waits for an error on the incoming chan, the result of which is later obtained via Err() (if no
	// other errors have been reported or forwarded)
	forward(<-chan error)

	// non-blocking, spins up a goroutine that reports an error (if any) that occurs on the error chan.
	Send(<-chan error) ErrorOnce
}
