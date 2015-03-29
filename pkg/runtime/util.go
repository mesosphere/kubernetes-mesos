package runtime

import (
	"os"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

type Signal <-chan struct{}

/* TODO(jdef) not very useful without execution conditional on errors
// upon receiving signal sig invoke function f and immediately return a signal
// that indicates f's completion.
func (sig Signal) Then(f func()) Signal {
	if sig == nil {
		return nil
	}
	ch := make(chan struct{})
	On(sig, func() {
		defer close(ch)
		f()
	})
	return Signal(ch)
}
*/

// execute a callback function after the specified signal chan closes.
// immediately returns a signal that indicates f's completion.
func On(sig <-chan struct{}, f func()) Signal {
	if sig == nil {
		return nil
	}
	return Go(func() {
		<-sig
		if f != nil {
			f()
		}
	})
}

func OnOSSignal(sig <-chan os.Signal, f func(os.Signal)) Signal {
	if sig == nil {
		return nil
	}
	return Go(func() {
		if s, ok := <-sig; ok && f != nil {
			f(s)
		}
	})
}

// spawn a goroutine to execute a func, immediately returns a chan that closes
// upon completion of the func. returns a nil signal chan if the given func is nil.
func Go(f func()) Signal {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		defer util.HandleCrash()
		if f != nil {
			f()
		}
	}()
	return Signal(ch)
}
