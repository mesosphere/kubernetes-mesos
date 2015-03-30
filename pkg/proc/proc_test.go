package proc

import (
	"sync"
	"testing"
	"time"

	log "github.com/golang/glog"
)

func TestProc_manyEndings(t *testing.T) {
	p := New()
	p.End()
	p.End()
	p.End()
	p.End()
	p.End()
	select {
	case <-p.Done():
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for process death")
	}
}

func TestProc_neverBegun(t *testing.T) {
	p := New()
	select {
	case <-p.Done():
		t.Fatalf("expected to time out waiting for process death")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestProc_halflife(t *testing.T) {
	p := New()
	p.End()
	select {
	case <-p.Done():
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for process death")
	}
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
	select {
	case <-p.Done():
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for process death")
	}
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

	select {
	case <-scheduled:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for deferred action to be scheduled")
	}

	select {
	case <-called:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for deferred action to be invoked")
	}

	p.End()

	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for process death")
	}
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

	ch := make(chan struct{})
	go func() {
		defer close(ch)
		called.Wait()
	}()
	select {
	case <-ch:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for deferred actions to be invoked")
	}

	p.End()

	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for process death")
	}
}

func TestProc_goodLifecycle(t *testing.T) {
	p := New()
	p.Begin()
	p.End()
	select {
	case <-p.Done():
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for process death")
	}
}
