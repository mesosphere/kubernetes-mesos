// +build unit_test

package service

import (
	"testing"
	"time"
)

type fakeSchedulerProcess struct {
	doneFunc     func() <-chan struct{}
	failoverFunc func() <-chan struct{}
}

func (self *fakeSchedulerProcess) Terminal() <-chan struct{} {
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

func (self *fakeSchedulerProcess) End() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
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

func Test_validateLeadershipTransition(t *testing.T) {
	if err := validateLeadershipTransition("1_name-1", ""); err != nil {
		t.Errorf("expected validation to pass when current id is empty")
	}

	if err := validateLeadershipTransition("1_name-1", "1_name-2"); err != nil {
		t.Errorf("expected validation to pass when desired group equals current group")
	}

	if err := validateLeadershipTransition("0_name-1", ""); err == nil {
		t.Errorf("expected validation to fail when desired group is 0 and current id is empty")
	}

	if err := validateLeadershipTransition("0_name-1", "1_name-2"); err == nil {
		t.Errorf("expected validation to fail when desired group is 0 and current id exists")
	}

	if err := validateLeadershipTransition("1_name-1", "2_name-2"); err == nil {
		t.Errorf("expected validation to fail when desired group does not equals current group")
	}
}