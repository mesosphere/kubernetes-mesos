package proc

import (
	"fmt"
	"sync"
	"testing"
	"time"

	log "github.com/golang/glog"
	"github.com/mesosphere/kubernetes-mesos/pkg/runtime"
)

// logs a testing.Fatalf if the elapsed time d passes before signal chan done is closed
func fatalAfter(t *testing.T, done <-chan struct{}, d time.Duration, msg string, args ...interface{}) {
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf(msg, args...)
	}
}

// logs a testing.Fatalf if the signal chan closes before the elapsed time d passes
func fatalOn(t *testing.T, done <-chan struct{}, d time.Duration, msg string, args ...interface{}) {
	select {
	case <-done:
		t.Fatalf(msg, args...)
	case <-time.After(d):
	}
}

func TestProc_manyEndings(t *testing.T) {
	p := New()
	const COUNT = 20
	var wg sync.WaitGroup
	wg.Add(COUNT)
	for i := 0; i < COUNT; i++ {
		runtime.Go(p.End).Then(wg.Done)
	}
	fatalAfter(t, runtime.Go(wg.Wait), 1*time.Second, "timed out waiting for loose End()s")
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}

func TestProc_neverBegun(t *testing.T) {
	p := New()
	fatalOn(t, p.Done(), 500*time.Millisecond, "expected to time out waiting for process death")
}

func TestProc_halflife(t *testing.T) {
	p := New()
	p.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}

func TestProc_beginTwice(t *testing.T) {
	p := New()
	p.Begin()
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected panic because Begin() was invoked more than once")
			}
		}()
		p.Begin() // should be ignored
	}()
	p.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}

func TestProc_singleAction(t *testing.T) {
	p := New()
	p.Begin()
	scheduled := make(chan struct{})
	called := make(chan struct{})

	go func() {
		log.Infof("do'ing deferred action")
		defer close(scheduled)
		err := p.Do(func() {
			defer close(called)
			log.Infof("deferred action invoked")
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}()

	fatalAfter(t, scheduled, 1*time.Second, "timed out waiting for deferred action to be scheduled")
	fatalAfter(t, called, 1*time.Second, "timed out waiting for deferred action to be invoked")

	p.End()

	fatalAfter(t, p.Done(), 2*time.Second, "timed out waiting for process death")
}

func TestProc_multiAction(t *testing.T) {
	p := New()
	p.Begin()
	const COUNT = 10
	var called sync.WaitGroup
	called.Add(COUNT)

	// test FIFO property
	next := 0
	for i := 0; i < COUNT; i++ {
		log.Infof("do'ing deferred action %d", i)
		idx := i
		err := p.Do(func() {
			defer called.Done()
			log.Infof("deferred action invoked")
			if next != idx {
				t.Fatalf("expected index %d instead of %d", idx, next)
			}
			next++
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	fatalAfter(t, runtime.Go(called.Wait), 1*time.Second, "timed out waiting for deferred actions to be invoked")

	p.End()

	fatalAfter(t, p.Done(), 2*time.Second, "timed out waiting for process death")
}

func TestProc_goodLifecycle(t *testing.T) {
	p := New()
	p.Begin()
	p.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}

func TestProc_doWithDeadProc(t *testing.T) {
	p := New()
	p.Begin()
	p.End()

	errUnexpected := fmt.Errorf("unexpected execution of delegated action")
	decorated := DoWith(p, DoerFunc(func(_ Action) error {
		return errUnexpected
	}))

	decorated.Do(func() {})
	fatalAfter(t, decorated.Done(), 1*time.Second, "timed out waiting for process death")
}

func TestProc_doWith(t *testing.T) {
	p := New()
	p.Begin()

	delegated := false
	decorated := DoWith(p, DoerFunc(func(a Action) error {
		delegated = true
		a()
		return nil
	}))

	executed := make(chan struct{})
	decorated.Do(func() {
		defer close(executed)
		if !delegated {
			t.Fatalf("expected delegated execution")
		}
	})

	fatalAfter(t, executed, 1*time.Second, "timed out waiting deferred execution")

	decorated.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}
