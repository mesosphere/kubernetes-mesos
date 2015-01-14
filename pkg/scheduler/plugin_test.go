package scheduler

import (
	"sync"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/mesos/mesos-go/mesos"
	"github.com/mesosphere/kubernetes-mesos/pkg/queue"
	"github.com/stretchr/testify/assert"
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
func (m *MockScheduler) client() (f *client.Client) {
	args := m.Called()
	x := args.Get(0)
	if x != nil {
		f = x.(*client.Client)
	}
	return
}
func (m *MockScheduler) createPodTask(ctx api.Context, pod *api.Pod) (task *PodTask, err error) {
	args := m.Called(ctx, pod)
	x := args.Get(0)
	if x != nil {
		task = x.(*PodTask)
	}
	err = args.Error(1)
	return
}
func (m *MockScheduler) driver() (f mesos.SchedulerDriver) {
	args := m.Called()
	x := args.Get(0)
	if x != nil {
		f = x.(mesos.SchedulerDriver)
	}
	return
}
func (m *MockScheduler) getTask(taskId string) (task *PodTask, currentState stateType) {
	args := m.Called(taskId)
	x := args.Get(0)
	if x != nil {
		task = x.(*PodTask)
	}
	y := args.Get(1)
	currentState = y.(stateType)
	return
}
func (m *MockScheduler) offers() (f OfferRegistry) {
	args := m.Called()
	x := args.Get(0)
	if x != nil {
		f = x.(OfferRegistry)
	}
	return
}
func (m *MockScheduler) registerPodTask(tin *PodTask, ein error) (tout *PodTask, eout error) {
	args := m.Called(tin, ein)
	x := args.Get(0)
	if x != nil {
		tout = x.(*PodTask)
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
func (m *MockScheduler) unregisterPodTask(task *PodTask) {
	m.Called(task)
}

// @deprecated this is a placeholder for me to test the mock package
func TestNoSlavesYet(t *testing.T) {
	obj := &MockScheduler{}
	obj.On("slaveFor", "foo").Return(nil, false)
	obj.slaveFor("foo")
	obj.AssertExpectations(t)
}

func TestDeleteOneNonexistentPod(t *testing.T) {
	assert := assert.New(t)
	obj := &MockScheduler{}
	obj.On("taskForPod", "/pods/default/foo").Return("", false)
	d := &deleter{
		api:      obj,
		podQueue: queue.NewDelayFIFO(),
	}
	pod := &Pod{Pod: &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			Namespace: api.NamespaceDefault,
		}}}
	err := d.deleteOne(pod)
	assert.Equal(err, noSuchPodErr)
	obj.AssertExpectations(t)
}

func TestDeleteOnePendingPod(t *testing.T) {
	assert := assert.New(t)
	obj := &MockScheduler{}
	podKey := "/pods/default/foo"
	pod := &Pod{Pod: &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			UID:       "foo0",
			Namespace: api.NamespaceDefault,
		}}}
	task := &PodTask{ID: "bar", Pod: pod.Pod}
	obj.On("taskForPod", podKey).Return(task.ID, true)
	obj.On("getTask", task.ID).Return(task, statePending)
	obj.On("unregisterPodTask", task).Return()

	q := queue.NewDelayFIFO()
	q.Add(pod, queue.ReplaceExisting)
	assert.Equal(1, len(q.List()))
	t.Logf("pod.uid: %v\n", q.List()[0].(queue.UniqueID).GetUID())
	_, found := q.Get("foo0")
	assert.True(found)

	d := &deleter{
		api:      obj,
		podQueue: q,
	}
	err := d.deleteOne(pod)
	assert.Nil(err)
	_, found = q.Get("foo0")
	assert.False(found)
	assert.Equal(0, len(q.List()))
	obj.AssertExpectations(t)
}
