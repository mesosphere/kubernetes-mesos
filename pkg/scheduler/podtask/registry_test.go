package podtask

import (
	"testing"

	"github.com/stretchr/testify/assert"
	mesos "github.com/mesos/mesos-go/mesosproto"
	"github.com/mesos/mesos-go/mesosutil"
)

func TestInMemoryRegistry_RegisterGetUnregister(t *testing.T) {
	assert := assert.New(t)

	registry := NewInMemoryRegistry()

	// it's empty at the beginning
	tasks := registry.List(func(t *T) bool { return true })
	assert.Empty(tasks)

	// add a task
	a, _ := fakePodTask("a")
	a_clone, err := registry.Register(a, nil)
	assert.NoError(err)
	assert.Equal(a_clone.ID, a.ID)
	assert.Equal(a_clone.podKey, a.podKey)

	// add another task
	b, _ := fakePodTask("b")
	b_clone, err := registry.Register(b, nil)
	assert.NoError(err)
	assert.Equal(b_clone.ID, b.ID)
	assert.Equal(b_clone.podKey, b.podKey)

	// find tasks in the registry
	tasks = registry.List(func(t *T) bool { return true })
	assert.Len(tasks, 2)
	assert.Contains(tasks, a_clone)
	assert.Contains(tasks, b_clone)

	tasks = registry.List(func(t *T) bool { return t.ID == a.ID })
	assert.Len(tasks, 1)
	assert.Contains(tasks, a_clone)

	task, _ := registry.ForPod(a.podKey)
	assert.NotNil(task)
	assert.Equal(task.ID, a.ID)

	task, _ = registry.ForPod(b.podKey)
	assert.NotNil(task)
	assert.Equal(task.ID, b.ID)

	task, _ = registry.ForPod("no-pod-key")
	assert.Nil(task)

	task, _ = registry.Get(a.ID)
	assert.NotNil(task)
	assert.Equal(task.ID, a.ID)

	task, _ = registry.Get("unknown-task-id")
	assert.Nil(task)

	// re-add a task
	a_clone, err = registry.Register(a, nil)
	assert.Error(err)
	assert.Nil(a_clone)

	// re-add a task with another podKey, but same task id
	another_a := a.Clone()
	another_a.podKey = "another-pod"
	another_a_clone, err := registry.Register(another_a, nil)
	assert.Error(err)
	assert.Nil(another_a_clone)

	// re-add a task with another task ID, but same podKey
	another_b := b.Clone()
	another_b.ID = "another-task-id"
	another_b_clone, err := registry.Register(another_b, nil)
	assert.Error(err)
	assert.Nil(another_b_clone)

	// unregister a task
	registry.Unregister(b)

	tasks = registry.List(func(t *T) bool { return true })
	assert.Len(tasks, 1)
	assert.Contains(tasks, a)

	// unregister a task not registered
	unregistered_task, _ := fakePodTask("unregistered-task")
	registry.Unregister(unregistered_task)
}

func fakeStatusUpdate(taskId string, state mesos.TaskState) *mesos.TaskStatus {
	status := mesosutil.NewTaskStatus(mesosutil.NewTaskID(taskId), state)
	status.Data = []byte("{}") // empty json
	return status
}

func TestInMemoryRegistry_State(t *testing.T) {
	assert := assert.New(t)

	registry := NewInMemoryRegistry()

	// add a task
	a, _ := fakePodTask("a")
	a_clone, err := registry.Register(a, nil)
	assert.NoError(err)
	assert.Equal(a.State, a_clone.State)

	// update the status
	assert.Equal(a_clone.State,StatePending)
	a_clone, state := registry.UpdateStatus(fakeStatusUpdate(a.ID, mesos.TaskState_TASK_RUNNING))
	assert.Equal(state, StatePending) // old state
	assert.Equal(a_clone.State, StateRunning) // new state

	// update unknown task
	unknown_clone, state := registry.UpdateStatus(fakeStatusUpdate("unknown-task-id", mesos.TaskState_TASK_RUNNING))
	assert.Nil(unknown_clone)
	assert.Equal(state, StateUnknown)
}
