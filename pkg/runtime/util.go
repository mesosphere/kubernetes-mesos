package runtime

import (
	"os"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

type Signal <-chan struct{}

// upon receiving signal sig invoke function f and immediately return a signal
// that indicates f's completion. used to chain handler funcs, for example:
//    On(job.Done(), response.Send).Then(wg.Done)
func (sig Signal) Then(f func()) Signal {
	if sig == nil {
		return nil
	}
	return On(sig, f)
}

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

// periodically execute the given function, stopping once stopCh is closed.
// this func blocks until stopCh is closed, it's intended to be run as a goroutine.
func Until(f func(), period time.Duration, stopCh <-chan struct{}) {
	if f == nil {
		return
	}
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		func() {
			defer util.HandleCrash()
			f()
		}()
		select {
		case <-stopCh:
		case <-time.After(period):
		}
	}
}
