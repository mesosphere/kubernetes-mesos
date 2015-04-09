package runtime

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func Test_LatchAcquireBasic(t *testing.T) {
	var x Latch
	if !x.Acquire() {
		t.Fatalf("expected first acquire to succeed")
	}
	if x.Acquire() {
		t.Fatalf("expected second acquire to fail")
	}
	if x.Acquire() {
		t.Fatalf("expected third acquire to fail")
	}
}

func Test_LatchAcquireConcurrent(t *testing.T) {
	var x Latch
	const NUM = 10
	ch := make(chan struct{})
	var success int32
	var wg sync.WaitGroup
	wg.Add(NUM)
	for i := 0; i < NUM; i++ {
		go func() {
			defer wg.Done()
			<-ch
			if x.Acquire() {
				atomic.AddInt32(&success, 1)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(ch)
	wg.Wait()
	if success != 1 {
		t.Fatalf("expected single acquire to succeed instead of %d", success)
	}
}
