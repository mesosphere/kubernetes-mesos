package executor

import (
	"testing"
	"time"

	"github.com/golang/glog"
	bindings "github.com/mesos/mesos-go/executor"
)

type suicideTracker struct {
	suicideWatcher
	stops  uint32
	resets uint32
	timers uint32
	jumps  uint32
}

func (t *suicideTracker) Reset(d time.Duration) bool {
	defer func() { t.resets++ }()
	return t.suicideWatcher.Reset(d)
}

func (t *suicideTracker) Stop() bool {
	defer func() { t.stops++ }()
	return t.suicideWatcher.Stop()
}

func (t *suicideTracker) Next(d time.Duration, driver bindings.ExecutorDriver, f jumper) suicideWatcher {
	tracker := &suicideTracker{
		stops:  t.stops,
		resets: t.resets,
		jumps:  t.jumps,
		timers: t.timers + 1,
	}
	jumper := tracker.makeJumper(f)
	tracker.suicideWatcher = t.suicideWatcher.Next(d, driver, jumper)
	return tracker
}

func (t *suicideTracker) makeJumper(f jumper) jumper {
	return jumper(func(driver bindings.ExecutorDriver, cancel <-chan struct{}) {
		t.jumps++
		f(driver, cancel)
	})
}

func TestSuicide_zeroTimeout(t *testing.T) {
	defer glog.Flush()

	k := New(Config{})
	tracker := &suicideTracker{suicideWatcher: k.suicideWatch}
	k.suicideWatch = tracker

	ch := k.resetSuicideWatch(nil)

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for reset of suicide watch")
	}
	if tracker.stops != 0 {
		t.Fatalf("expected no stops since suicideWatchTimeout was never set")
	}
	if tracker.resets != 0 {
		t.Fatalf("expected no resets since suicideWatchTimeout was never set")
	}
	if tracker.timers != 0 {
		t.Fatalf("expected no timers since suicideWatchTimeout was never set")
	}
}

func TestSuicide_WithTasks(t *testing.T) {
	defer glog.Flush()

	k := New(Config{
		SuicideTimeout: 50 * time.Millisecond,
	})
	tracker := &suicideTracker{suicideWatcher: k.suicideWatch}
	k.suicideWatch = tracker

	k.tasks["foo"] = &kuberTask{} // prevent suicide attempts from succeeding

	select {
	case <-k.resetSuicideWatch(nil):
		if tracker.stops != 1 {
			t.Fatalf("expected suicide attempt to Stop() since there are registered tasks")
		}
		if tracker.resets != 0 {
			t.Fatalf("expected no resets since suicideWatchTimeout was never set")
		}
		if tracker.timers != 0 {
			t.Fatalf("expected no timers since suicideWatchTimeout was never set")
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("initial suicide watch setup failed")
	}

	delete(k.tasks, "foo") // zero remaining tasks
	k.suicideTimeout = 1500 * time.Millisecond

	// reset the suicide watch, which should actually start a timer now
	select {
	case <-k.resetSuicideWatch(nil):
		tracker = k.suicideWatch.(*suicideTracker)
		if tracker.stops != 1 {
			t.Fatalf("did not expect suicide attempt to Stop() since there are no registered tasks")
		}
		if tracker.resets != 1 {
			t.Fatalf("expected 1 resets instead of %d since suicideWatchTimeout was never set", tracker.resets)
		}
		if tracker.timers != 1 {
			t.Fatalf("expected 1 timers instead of %d since suicideWatchTimeout was never set", tracker.timers)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("2nd suicide watch setup failed")
	}

	k.lock.Lock()
	k.tasks["foo"] = &kuberTask{} // prevent suicide attempts from succeeding
	k.lock.Unlock()

	// reset the suicide watch, which should stop the existing timer
	select {
	case <-k.resetSuicideWatch(nil):
		tracker = k.suicideWatch.(*suicideTracker)
		if tracker.stops != 2 {
			t.Fatalf("expected 2 stops instead of %d since there are no registered tasks", tracker.stops)
		}
		if tracker.resets != 1 {
			t.Fatalf("expected 1 resets instead of %d since suicideWatchTimeout was never set", tracker.resets)
		}
		if tracker.timers != 1 {
			t.Fatalf("expected 1 timers instead of %d since suicideWatchTimeout was never set", tracker.timers)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("3rd suicide watch setup failed")
	}
}

/*
	const WORKERS = 1000
	var wg sync.WaitGroup
	wg.Add(WORKERS)

	start := time.Now()
	for i := 0; i < WORKERS; i++ {
		ch := k.resetSuicideWatch(nil)
		worker := i
		go func() {
			select {
			case <-ch:
				//t.Logf("COMPLETED worker %d", worker)
				wg.Done()
			case <-time.After(3 * time.Second):
				t.Errorf("timeout waiting for worker %d", worker)
			}
		}()
	}

	ch := make(chan struct{})
	go func() {
		defer close(ch)
		wg.Wait()
	}()

	select {
	case <-ch:
		t.Logf("completed with %d workers in %v", WORKERS, time.Since(start))
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for resets of suicide watch")
	}
}
*/
