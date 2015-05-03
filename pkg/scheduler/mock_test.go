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
func (m *MockScheduler) offers() (f offers.Registry) {
	args := m.Called()
	x := args.Get(0)
	if x != nil {
		f = x.(offers.Registry)
	}
	return
}
func (m *MockScheduler) tasks() (f podtask.Registry) {
	args := m.Called()
	x := args.Get(0)
	if x != nil {
		f = x.(podtask.Registry)
	}
	return
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
func (m *MockSchedulerDriver) RequestResources(r []*mesos.Request) (mesos.Status, error) {
	args := m.Called(r)
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) ReconcileTasks(statuses []*mesos.TaskStatus) (mesos.Status, error) {
	args := m.Called(statuses)
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) LaunchTasks(offerIds []*mesos.OfferID, ti []*mesos.TaskInfo, f *mesos.Filters) (mesos.Status, error) {
	args := m.Called(offerIds, ti, f)
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) KillTask(tid *mesos.TaskID) (mesos.Status, error) {
	args := m.Called(tid)
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) DeclineOffer(oid *mesos.OfferID, f *mesos.Filters) (mesos.Status, error) {
	args := m.Called(oid, f)
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) ReviveOffers() (mesos.Status, error) {
	args := m.Called()
	return status(args, 0), args.Error(0)
}
func (m *MockSchedulerDriver) SendFrameworkMessage(eid *mesos.ExecutorID, sid *mesos.SlaveID, s string) (mesos.Status, error) {
	args := m.Called(eid, sid, s)
	return status(args, 0), args.Error(1)
}
func (m *MockSchedulerDriver) Destroy() {
	m.Called()
}
func (m *MockSchedulerDriver) Wait() {
	m.Called()
}
