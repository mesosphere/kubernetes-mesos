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

func TestProc_singleAction(t *testing.T) {
	p := New()
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
	p.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}

func TestProc_doWithDeadProc(t *testing.T) {
	p := New()
	p.End()

	errUnexpected := fmt.Errorf("unexpected execution of delegated action")
	decorated := DoWith(p, DoerFunc(func(_ Action) <-chan error {
		return ErrorChan(errUnexpected)
	}))

	decorated.Do(func() {})
	fatalAfter(t, decorated.Done(), 1*time.Second, "timed out waiting for process death")
}

func TestProc_doWith(t *testing.T) {
	p := New()

	delegated := false
	decorated := DoWith(p, DoerFunc(func(a Action) <-chan error {
		delegated = true
		a()
		return nil
	}))

	executed := make(chan struct{})
	err := decorated.Do(func() {
		defer close(executed)
		if !delegated {
			t.Fatalf("expected delegated execution")
		}
	})
	if err == nil {
		t.Fatalf("expected !nil error chan")
	}

	fatalAfter(t, executed, 1*time.Second, "timed out waiting deferred execution")
	fatalAfter(t, decorated.OnError(err, func(e error) {
		t.Fatalf("unexpected error: %v", err)
	}), 1*time.Second, "timed out waiting for doer result")

	decorated.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}

func TestProc_doWithNestedTwice(t *testing.T) {
	p := New()

	delegated := false
	decorated := DoWith(p, DoerFunc(func(a Action) <-chan error {
		a()
		return nil
	}))

	decorated2 := DoWith(decorated, DoerFunc(func(a Action) <-chan error {
		delegated = true
		a()
		return nil
	}))

	executed := make(chan struct{})
	err := decorated2.Do(func() {
		defer close(executed)
		if !delegated {
			t.Fatalf("expected delegated execution")
		}
	})
	if err == nil {
		t.Fatalf("expected !nil error chan")
	}

	fatalAfter(t, executed, 1*time.Second, "timed out waiting deferred execution")
	fatalAfter(t, decorated2.OnError(err, func(e error) {
		t.Fatalf("unexpected error: %v", err)
	}), 1*time.Second, "timed out waiting for doer result")

	decorated2.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}

func TestProc_doWithNestedErrorPropagation(t *testing.T) {
	p := New()

	delegated := false
	decorated := DoWith(p, DoerFunc(func(a Action) <-chan error {
		a()
		return nil
	}))

	expectedErr := fmt.Errorf("expecting this")
	errOnce := NewErrorOnce(p.Done())
	decorated2 := DoWith(decorated, DoerFunc(func(a Action) <-chan error {
		delegated = true
		a()
		errOnce.Report(fmt.Errorf("unexpected error in decorator2"))
		return ErrorChan(fmt.Errorf("another unexpected error in decorator2"))
	}))

	executed := make(chan struct{})
	err := decorated2.Do(func() {
		defer close(executed)
		if !delegated {
			t.Fatalf("expected delegated execution")
		}
		errOnce.Report(expectedErr)
	})
	if err == nil {
		t.Fatalf("expected !nil error chan")
	}
	errOnce.Send(err)

	foundError := false
	fatalAfter(t, executed, 1*time.Second, "timed out waiting deferred execution")
	fatalAfter(t, decorated2.OnError(errOnce.Err(), func(e error) {
		if e != expectedErr {
			t.Fatalf("unexpected error: %v", err)
		} else {
			foundError = true
		}
	}), 1*time.Second, "timed out waiting for doer result")

	if !foundError {
		t.Fatalf("expected a propagated error")
	}

	decorated2.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}

func runDelegationTest(t *testing.T, p Process, name string) {
	var decorated Process
	decorated = p

	const DEPTH = 100
	var wg sync.WaitGroup
	wg.Add(DEPTH)
	y := 0

	for x := 1; x <= DEPTH; x++ {
		x := x
		nextp := DoWith(decorated, DoerFunc(func(a Action) <-chan error {
			if x == 1 {
				t.Logf("delegate chain invoked for " + name)
			}
			y++
			if y != x {
				t.Fatalf("out of order delegated execution")
			}
			defer wg.Done()
			a()
			return nil
		}))
		decorated = nextp
	}

	executed := make(chan struct{})
	err := decorated.Do(func() {
		defer close(executed)
		if y != DEPTH {
			t.Fatalf("expected delegated execution")
		}
		t.Logf("executing deferred action: " + name)
	})
	if err == nil {
		t.Fatalf("expected !nil error chan")
	}

	fatalAfter(t, executed, 1*time.Second, "timed out waiting deferred execution")
	fatalAfter(t, decorated.OnError(err, func(e error) {
		t.Fatalf("unexpected error: %v", err)
	}), 1*time.Second, "timed out waiting for doer result")
}

func TestProc_doWithNestedX(t *testing.T) {
	p := New()
	runDelegationTest(t, p, "nested")
	p.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}

// intended to be run with -race
func TestProc_doWithNestedXConcurrent(t *testing.T) {
	p := New()
	var wg sync.WaitGroup
	const CONC = 20
	wg.Add(CONC)
	for i := 0; i < CONC; i++ {
		i := i
		runtime.Go(func() { runDelegationTest(t, p, fmt.Sprintf("nested%d", i)) }).Then(wg.Done)
	}
	ch := runtime.Go(wg.Wait)
	fatalAfter(t, ch, 2*time.Second, "timed out waiting for concurrent delegates")

	p.End()
	fatalAfter(t, p.Done(), 1*time.Second, "timed out waiting for process death")
}
