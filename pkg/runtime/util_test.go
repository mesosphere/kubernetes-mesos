package runtime

import (
	"testing"
	"time"
)

func TestUntil(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	Until(func() {
		t.Fatal("should not have been invoked")
	}, 0, ch)

	//--
	ch = make(chan struct{})
	called := make(chan struct{})
	After(func() {
		Until(func() {
			called <- struct{}{}
		}, 0, ch)
	}).Then(func() { close(called) })

	<-called
	close(ch)
	<-called

	//--
	ch = make(chan struct{})
	called = make(chan struct{})
	running := make(chan struct{})
	After(func() {
		Until(func() {
			close(running)
			called <- struct{}{}
		}, 2*time.Second, ch)
	}).Then(func() { close(called) })

	<-running
	close(ch)
	<-called // unblock the goroutine
	now := time.Now()

	<-called
	if time.Since(now) > 1800*time.Millisecond {
		t.Fatalf("Until should not have waited the full timeout period since we closed the stop chan")
	}
}
