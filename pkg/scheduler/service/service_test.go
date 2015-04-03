// +build unit_test

package service

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/mesosphere/kubernetes-mesos/pkg/runtime"
)

type fakeSchedulerProcess struct {
	doneFunc     func() <-chan struct{}
	failoverFunc func() <-chan struct{}
	ended        runtime.Latch
}

func (self *fakeSchedulerProcess) Done() <-chan struct{} {
	if self == nil || self.doneFunc == nil {
		return nil
	}
	return self.doneFunc()
}

func (self *fakeSchedulerProcess) Failover() <-chan struct{} {
	if self == nil || self.failoverFunc == nil {
		return nil
	}
	return self.failoverFunc()
}

func (self *fakeSchedulerProcess) End() {
	self.ended.Acquire()
}

func makeFailoverSigChan() <-chan os.Signal {
	return nil
}

func makeDisownedProcAttr() *syscall.SysProcAttr {
	return nil
}

func Test_awaitFailoverDone(t *testing.T) {
	done := make(chan struct{})
	p := &fakeSchedulerProcess{
		doneFunc: func() <-chan struct{} { return done },
	}
	ss := &SchedulerServer{}
	failoverHandlerCalled := false
	failoverFailedHandler := func() error {
		failoverHandlerCalled = true
		return nil
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- ss.awaitFailover(p, failoverFailedHandler)
	}()
	close(done)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for failover")
	}
	if failoverHandlerCalled {
		t.Fatalf("unexpected call to failover handler")
	}
}

func Test_awaitFailoverDoneFailover(t *testing.T) {
	ch := make(chan struct{})
	p := &fakeSchedulerProcess{
		doneFunc:     func() <-chan struct{} { return ch },
		failoverFunc: func() <-chan struct{} { return ch },
	}
	ss := &SchedulerServer{}
	failoverHandlerCalled := false
	failoverFailedHandler := func() error {
		failoverHandlerCalled = true
		return nil
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- ss.awaitFailover(p, failoverFailedHandler)
	}()
	close(ch)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for failover")
	}
	if !failoverHandlerCalled {
		t.Fatalf("expected call to failover handler")
	}
}
