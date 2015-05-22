package executor

import (
	"flag"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/dockertools"
	"github.com/golang/glog"
	bindings "github.com/mesos/mesos-go/executor"
	"github.com/mesos/mesos-go/mesosproto"
	mutil "github.com/mesos/mesos-go/mesosutil"
	"github.com/stretchr/testify/mock"
)

var test_v = flag.Int("test-v", 0, "test -v")

type suicideTracker struct {
	suicideWatcher
	stops  uint32
	resets uint32
	timers uint32
	jumps  *uint32
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

func (t *suicideTracker) makeJumper(_ jumper) jumper {
	return jumper(func(driver bindings.ExecutorDriver, cancel <-chan struct{}) {
		glog.Warningln("jumping?!")
		if t.jumps != nil {
			atomic.AddUint32(t.jumps, 1)
		}
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

	jumps := uint32(0)
	tracker := &suicideTracker{suicideWatcher: k.suicideWatch, jumps: &jumps}
	k.suicideWatch = tracker

	k.tasks["foo"] = &kuberTask{} // prevent suicide attempts from succeeding

	// call reset with a nil timer
	glog.Infoln("resetting suicide watch with 1 task")
	select {
	case <-k.resetSuicideWatch(nil):
		tracker = k.suicideWatch.(*suicideTracker)
		if tracker.stops != 1 {
			t.Fatalf("expected suicide attempt to Stop() since there are registered tasks")
		}
		if tracker.resets != 0 {
			t.Fatalf("expected no resets since")
		}
		if tracker.timers != 0 {
			t.Fatalf("expected no timers since")
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("initial suicide watch setup failed")
	}

	delete(k.tasks, "foo") // zero remaining tasks
	k.suicideTimeout = 1500 * time.Millisecond
	suicideStart := time.Now()

	// reset the suicide watch, which should actually start a timer now
	glog.Infoln("resetting suicide watch with 0 tasks")
	select {
	case <-k.resetSuicideWatch(nil):
		tracker = k.suicideWatch.(*suicideTracker)
		if tracker.stops != 1 {
			t.Fatalf("did not expect suicide attempt to Stop() since there are no registered tasks")
		}
		if tracker.resets != 1 {
			t.Fatalf("expected 1 resets instead of %d", tracker.resets)
		}
		if tracker.timers != 1 {
			t.Fatalf("expected 1 timers instead of %d", tracker.timers)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("2nd suicide watch setup failed")
	}

	k.lock.Lock()
	k.tasks["foo"] = &kuberTask{} // prevent suicide attempts from succeeding
	k.lock.Unlock()

	// reset the suicide watch, which should stop the existing timer
	glog.Infoln("resetting suicide watch with 1 task")
	select {
	case <-k.resetSuicideWatch(nil):
		tracker = k.suicideWatch.(*suicideTracker)
		if tracker.stops != 2 {
			t.Fatalf("expected 2 stops instead of %d since there are registered tasks", tracker.stops)
		}
		if tracker.resets != 1 {
			t.Fatalf("expected 1 resets instead of %d", tracker.resets)
		}
		if tracker.timers != 1 {
			t.Fatalf("expected 1 timers instead of %d", tracker.timers)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("3rd suicide watch setup failed")
	}

	k.lock.Lock()
	delete(k.tasks, "foo") // allow suicide attempts to schedule
	k.lock.Unlock()

	// reset the suicide watch, which should reset a stopped timer
	glog.Infoln("resetting suicide watch with 0 tasks")
	select {
	case <-k.resetSuicideWatch(nil):
		tracker = k.suicideWatch.(*suicideTracker)
		if tracker.stops != 2 {
			t.Fatalf("expected 2 stops instead of %d since there are no registered tasks", tracker.stops)
		}
		if tracker.resets != 2 {
			t.Fatalf("expected 2 resets instead of %d", tracker.resets)
		}
		if tracker.timers != 1 {
			t.Fatalf("expected 1 timers instead of %d", tracker.timers)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("4th suicide watch setup failed")
	}

	sinceWatch := time.Since(suicideStart)
	time.Sleep(3*time.Second - sinceWatch) // give the first timer to misfire (it shouldn't since Stop() was called)

	if j := atomic.LoadUint32(&jumps); j != 1 {
		t.Fatalf("expected 1 jumps instead of %d since stop was called", j)
	} else {
		glog.Infoln("jumps verified") // glog so we get a timestamp
	}
}

type MockExecutorDriver struct {
	mock.Mock
}

func (m MockExecutorDriver) Start() (mesosproto.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(1)
}

func (m MockExecutorDriver) Stop() (mesosproto.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(1)
}

func (m MockExecutorDriver) Abort() (mesosproto.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(1)
}

func (m MockExecutorDriver) Join() (mesosproto.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(1)
}

func (m MockExecutorDriver) Run() (mesosproto.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(1)
}

func (m MockExecutorDriver) SendStatusUpdate(taskStatus *mesosproto.TaskStatus) (mesosproto.Status, error) {
	args := m.Called(taskStatus)
	return status(args, 0), args.Error(1)
}

func (m MockExecutorDriver) SendFrameworkMessage(msg string) (mesosproto.Status, error) {
	args := m.Called(m)
	return status(args, 0), args.Error(1)
}

func status(args mock.Arguments, at int) (val mesosproto.Status) {
	if x := args.Get(at); x != nil {
		val = x.(mesosproto.Status)
	}
	return
}

func TestExecutorNew(t *testing.T) {
	flag.Lookup("v").Value.Set(fmt.Sprint(*test_v))

	mockDriver := MockExecutorDriver{}
	executor := New(Config{
		Docker: dockertools.ConnectToDockerOrDie("fake://"),
	})
	executor.Init(mockDriver)

	executorID := mutil.NewExecutorID("mock executor")
	commandInfo := mutil.NewCommandInfo("mock commandInfo")
	executorInfo := mutil.NewExecutorInfo(executorID, commandInfo)
	slaveID := mutil.NewSlaveID("mock slaveID")

	frameworkID := mutil.NewFrameworkID("mock frameworkID")
	frameworkInfo := mutil.NewFrameworkInfo("mock user", "mock name", frameworkID)

	hostname := "mock.host.name"
	port := int32(0)
	slaveInfo := mesosproto.SlaveInfo{
		Hostname: &hostname,
		Port:     &port,
		Id:       slaveID,
	}

	executor.Registered(mockDriver, executorInfo, frameworkInfo, &slaveInfo)
}
