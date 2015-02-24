package scheduler

import (
	"sync"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	mesos "github.com/mesos/mesos-go/mesosproto"
	"github.com/mesosphere/kubernetes-mesos/pkg/offers"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/podtask"
	"github.com/stretchr/testify/mock"
)

// implements SchedulerInterface
type MockScheduler struct {
	sync.RWMutex
	mock.Mock
}

func (m *MockScheduler) slaveFor(id string) (slave *Slave, ok bool) {
	args := m.Called(id)
	x := args.Get(0)
	if x != nil {
		slave = x.(*Slave)
	}
	ok = args.Bool(1)
	return
}
func (m *MockScheduler) algorithm() (f PodScheduleFunc) {
	args := m.Called()
	x := args.Get(0)
	if x != nil {
		f = x.(PodScheduleFunc)
	}
	return
}
func (m *MockScheduler) createPodTask(ctx api.Context, pod *api.Pod) (task *podtask.T, err error) {
	args := m.Called(ctx, pod)
	x := args.Get(0)
	if x != nil {
		task = x.(*podtask.T)
	}
	err = args.Error(1)
	return
}
func (m *MockScheduler) getTask(taskId string) (task *podtask.T, currentState podtask.StateType) {
	args := m.Called(taskId)
	x := args.Get(0)
	if x != nil {
		task = x.(*podtask.T)
	}
	y := args.Get(1)
	currentState = y.(podtask.StateType)
	return
}
func (m *MockScheduler) offers() (f offers.Registry) {
	args := m.Called()
	x := args.Get(0)
	if x != nil {
		f = x.(offers.Registry)
	}
	return
}
func (m *MockScheduler) registerPodTask(tin *podtask.T, ein error) (tout *podtask.T, eout error) {
	args := m.Called(tin, ein)
	x := args.Get(0)
	if x != nil {
		tout = x.(*podtask.T)
	}
	eout = args.Error(1)
	return
}
func (m *MockScheduler) taskForPod(podID string) (taskID string, ok bool) {
	args := m.Called(podID)
	taskID = args.String(0)
	ok = args.Bool(1)
	return
}
func (m *MockScheduler) unregisterPodTask(task *podtask.T) {
	m.Called(task)
}
func (m *MockScheduler) killTask(taskId string) error {
	args := m.Called(taskId)
	return args.Error(0)
}
func (m *MockScheduler) launchTask(task *podtask.T) error {
	args := m.Called(task)
	return args.Error(0)
}

// @deprecated this is a placeholder for me to test the mock package
func TestNoSlavesYet(t *testing.T) {
	obj := &MockScheduler{}
	obj.On("slaveFor", "foo").Return(nil, false)
	obj.slaveFor("foo")
	obj.AssertExpectations(t)
}

/*-----------------------------------------------------------------------------
 |
 |   this really belongs in the mesos-go package, but that's being updated soon
 |   any way so just keep it here for now unless we *really* need it there.
 |
 \-----------------------------------------------------------------------------

// Scheduler defines the interfaces that needed to be implemented.
type Scheduler interface {
        Registered(SchedulerDriver, *FrameworkID, *MasterInfo)
        Reregistered(SchedulerDriver, *MasterInfo)
        Disconnected(SchedulerDriver)
        ResourceOffers(SchedulerDriver, []*Offer)
        OfferRescinded(SchedulerDriver, *OfferID)
        StatusUpdate(SchedulerDriver, *TaskStatus)
        FrameworkMessage(SchedulerDriver, *ExecutorID, *SlaveID, string)
        SlaveLost(SchedulerDriver, *SlaveID)
        ExecutorLost(SchedulerDriver, *ExecutorID, *SlaveID, int)
        Error(SchedulerDriver, string)
}
*/

func status(args mock.Arguments, at int) (val mesos.Status) {
	if x := args.Get(at); x != nil {
		val = x.(mesos.Status)
	}
	return
}

type MockSchedulerDriver struct {
	mock.Mock
}

func (m *MockSchedulerDriver) Init() error {
	args := m.Called()
	return args.Error(0)
}
func (m *MockSchedulerDriver) Start() (mesos.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) Stop(b bool) (mesos.Status, error) {
	args := m.Called(b)
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) Abort() (mesos.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) Join() (mesos.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) Run() (mesos.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) RequestResources(r []*mesos.Request) error {
	args := m.Called(r)
	return args.Error(0)
}
func (m *MockSchedulerDriver) LaunchTasks(oid *mesos.OfferID, ti []*mesos.TaskInfo, f *mesos.Filters) error {
	args := m.Called(oid, ti, f)
	return args.Error(0)
}
func (m *MockSchedulerDriver) KillTask(tid *mesos.TaskID) error {
	args := m.Called(tid)
	return args.Error(0)
}
func (m *MockSchedulerDriver) DeclineOffer(oid *mesos.OfferID, f *mesos.Filters) error {
	args := m.Called(oid, f)
	return args.Error(0)
}
func (m *MockSchedulerDriver) ReviveOffers() error {
	args := m.Called()
	return args.Error(0)
}
func (m *MockSchedulerDriver) SendFrameworkMessage(eid *mesos.ExecutorID, sid *mesos.SlaveID, s string) error {
	args := m.Called(eid, sid, s)
	return args.Error(0)
}
func (m *MockSchedulerDriver) Destroy() {
	m.Called()
}
func (m *MockSchedulerDriver) Wait() {
	m.Called()
}
