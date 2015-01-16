package scheduler

import (
	"testing"

	proto "code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/mesos/mesos-go/mesos"
	"github.com/mesosphere/kubernetes-mesos/pkg/queue"
	"github.com/stretchr/testify/assert"
)

func TestDeleteOne_NonexistentPod(t *testing.T) {
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

func TestDeleteOne_PendingPod(t *testing.T) {
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

	// set expectations
	obj.On("taskForPod", podKey).Return(task.ID, true)
	obj.On("getTask", task.ID).Return(task, statePending)
	obj.On("unregisterPodTask", task).Return()

	// preconditions
	q := queue.NewDelayFIFO()
	q.Add(pod, queue.ReplaceExisting)
	assert.Equal(1, len(q.List()))
	t.Logf("pod.uid: %v\n", q.List()[0].(queue.UniqueID).GetUID())
	_, found := q.Get("foo0")
	assert.True(found)

	// exec & post conditions
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

func TestDeleteOne_Running(t *testing.T) {
	assert := assert.New(t)
	obj := &MockScheduler{}
	drv := &MockSchedulerDriver{}

	podKey := "/pods/default/foo"
	pod := &Pod{Pod: &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			UID:       "foo0",
			Namespace: api.NamespaceDefault,
		}}}
	task := &PodTask{ID: "bar", Pod: pod.Pod, launched: true}

	// set expectations
	obj.On("taskForPod", podKey).Return(task.ID, true)
	obj.On("getTask", task.ID).Return(task, statePending)
	obj.On("driver").Return(drv)
	drv.On("KillTask", &mesos.TaskID{Value: proto.String(task.ID)}).Return(nil)

	// preconditions
	q := queue.NewDelayFIFO()
	q.Add(pod, queue.ReplaceExisting)
	assert.Equal(1, len(q.List()))
	t.Logf("pod.uid: %v\n", q.List()[0].(queue.UniqueID).GetUID())
	_, found := q.Get("foo0")
	assert.True(found)

	// exec & post conditions
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

func TestDeleteOne_Unknown(t *testing.T) {
	assert := assert.New(t)
	obj := &MockScheduler{}

	podKey := "/pods/default/foo"
	pod := &Pod{Pod: &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			UID:       "foo0",
			Namespace: api.NamespaceDefault,
		}}}
	taskId := "bar123"

	// set expectations
	obj.On("taskForPod", podKey).Return(taskId, true)
	obj.On("getTask", taskId).Return(nil, stateUnknown)

	// preconditions
	q := queue.NewDelayFIFO()
	assert.Equal(0, len(q.List()))

	// exec & post conditions
	d := &deleter{
		api:      obj,
		podQueue: q,
	}
	err := d.deleteOne(pod)
	assert.Equal(err, noSuchTaskErr)
	assert.Equal(0, len(q.List()))
	obj.AssertExpectations(t)
}

func TestDeleteOne_badPodNaming(t *testing.T) {
	assert := assert.New(t)
	obj := &MockScheduler{}
	pod := &Pod{Pod: &api.Pod{}}
	q := queue.NewDelayFIFO()
	d := &deleter{
		api:      obj,
		podQueue: q,
	}

	err := d.deleteOne(pod)
	assert.NotNil(err)

	pod.Pod.ObjectMeta.Name = "foo"
	err = d.deleteOne(pod)
	assert.NotNil(err)

	pod.Pod.ObjectMeta.Name = ""
	pod.Pod.ObjectMeta.Namespace = "bar"
	err = d.deleteOne(pod)
	assert.NotNil(err)

	obj.AssertExpectations(t)
}
