package scheduler

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/testapi"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"

	mesos "github.com/mesos/mesos-go/mesosproto"
	"github.com/mesos/mesos-go/mesosutil"
	bindings "github.com/mesos/mesos-go/scheduler"
	"github.com/mesosphere/kubernetes-mesos/pkg/queue"
	schedcfg "github.com/mesosphere/kubernetes-mesos/pkg/scheduler/config"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/ha"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/podtask"
	"github.com/stretchr/testify/assert"

)

func makeTestServer(t *testing.T, namespace string, pods *api.PodList) (*httptest.Server) {
	podsHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(runtime.EncodeOrDie(testapi.Codec(), pods)))
	}

	mux := http.NewServeMux()
	mux.Handle(testapi.ResourcePath("pods", namespace, ""), http.HandlerFunc(podsHandler))
	mux.HandleFunc("/", func(res http.ResponseWriter, req *http.Request) {
		t.Errorf("unexpected request: %v", req.RequestURI)
		res.WriteHeader(http.StatusNotFound)
	})
	return httptest.NewServer(mux)
}

func TestPlugin_New(t *testing.T) {
	assert := assert.New(t)

	c := PluginConfig{}
	p := NewPlugin(&c)
	assert.NotNil(p)
}

// Create mock of pods ListWatch, usually listening on the apiserver pods watch endpoint
type MockPodsListWatch struct {
	ListWatch cache.ListWatch
	fakeWatcher *watch.FakeWatcher
	list api.PodList
}
func NewMockPodsListWatch(initialPodList api.PodList) *MockPodsListWatch {
	lw := MockPodsListWatch{
		fakeWatcher: watch.NewFake(),
		list: initialPodList,
	}
	lw.ListWatch = cache.ListWatch{
		WatchFunc: func (resourceVersion string) (watch.Interface, error) {
			return lw.fakeWatcher, nil
		},
		ListFunc: func() (runtime.Object, error) {
			return &lw.list, nil
		},
	}
	return &lw
}
func (lw *MockPodsListWatch) Add(pod *api.Pod) {
	lw.list.Items = append(lw.list.Items, *pod)
	lw.fakeWatcher.Add(pod)
}
func (lw *MockPodsListWatch) Modify(pod *api.Pod) {
	for i, otherPod := range lw.list.Items {
		if otherPod.Name == pod.Name {
			lw.list.Items[i] = *pod
			lw.fakeWatcher.Modify(pod)
			return
		}
	}
	log.Panicf("Cannot find pod %v to modify in MockPodsListWatch", pod.Name)
}
func (lw *MockPodsListWatch) Delete(pod *api.Pod) {
	for i, otherPod := range lw.list.Items {
		if otherPod.Name == pod.Name {
			lw.list.Items = append(lw.list.Items[:i], lw.list.Items[i+1:]...)
			lw.fakeWatcher.Delete(&otherPod)
			return
		}
	}
	log.Panicf("Cannot find pod %v to delete in MockPodsListWatch", pod.Name)
}

func NewTestPod(i int) *api.Pod {
	return &api.Pod{
		TypeMeta:   api.TypeMeta{APIVersion: testapi.Version()},
		ObjectMeta: api.ObjectMeta{
			Name: fmt.Sprintf("pod%d", i),
			Namespace: "default",
		},
		Spec: api.PodSpec{
			Containers: []api.Container{{Ports: []api.ContainerPort{}}},
		},
		Status: api.PodStatus{
			PodIP: fmt.Sprintf("1.2.3.%d", 4+i),
			Conditions: []api.PodCondition{
				{
					Type:   api.PodReady,
					Status: api.ConditionTrue,
				},
			},
		},
	}
}

func TestPlugin_NewFromScheduler(t *testing.T) {
	assert := assert.New(t)

	// create a fake pod watch
	podListWatch := NewMockPodsListWatch(api.PodList{})

	// create fake apiserver
	testApiServer := makeTestServer(t, api.NamespaceDefault, &podListWatch.list)
	defer testApiServer.Close()

	// create scheduler
	testScheduler := New(Config{
		Executor: mesosutil.NewExecutorInfo(
			mesosutil.NewExecutorID("executor-id"),
			mesosutil.NewCommandInfo("executor-cmd"),
		),
		Client: client.NewOrDie(&client.Config{Host: testApiServer.URL, Version: testapi.Version()}),
		PodsListWatch: &podListWatch.ListWatch,
		ScheduleFunc: FCFSScheduleFunc,
		Schedcfg: *schedcfg.CreateDefaultConfig(),
	})

	assert.NotNil(testScheduler.client, "client is nil")
	assert.NotNil(testScheduler.executor, "executor is nil")
	assert.NotNil(testScheduler.offers, "offer registry is nil")

	// create scheduler process
	schedulerProcess := ha.New(testScheduler)

	// get plugin config from it
	c := testScheduler.NewPluginConfig(schedulerProcess.Terminal(), http.DefaultServeMux)
	assert.NotNil(c)

	// create plugin
	p := NewPlugin(c)
	assert.NotNil(p)

	// run plugin
	p.Run(schedulerProcess.Terminal())

	// init scheduler
	err := testScheduler.Init(schedulerProcess.Master(), p, http.DefaultServeMux)
	assert.NoError(err)

	// elect master with mock driver
	driverFactory := ha.DriverFactory(func() (bindings.SchedulerDriver, error) {
		mockDriver := MockSchedulerDriver{}
		mockDriver.On("Start").Return(mesos.Status_DRIVER_RUNNING, nil)
		mockDriver.On("Join").Return(mesos.Status_DRIVER_STOPPED, nil)
		return &mockDriver, nil;
	})
	schedulerProcess.Elect(driverFactory)
	elected := schedulerProcess.Elected()

	// wait for being elected
	_ = <-elected

	// fake new, unscheduled pod
	pod1 := NewTestPod(1)
	podListWatch.Add(pod1)

	time.Sleep(2 * time.Second)

	// stop plugin
	schedulerProcess.End()
}

func TestDeleteOne_NonexistentPod(t *testing.T) {
	assert := assert.New(t)
	obj := &MockScheduler{}
	reg := podtask.NewInMemoryRegistry()
	obj.On("tasks").Return(reg)

	qr := newQueuer(nil)
	assert.Equal(0, len(qr.podQueue.List()))
	d := &deleter{
		api: obj,
		qr:  qr,
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
	reg := podtask.NewInMemoryRegistry()
	obj.On("tasks").Return(reg)

	pod := &Pod{Pod: &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			UID:       "foo0",
			Namespace: api.NamespaceDefault,
		}}}
	_, err := reg.Register(podtask.New(api.NewDefaultContext(), "bar", *pod.Pod, &mesos.ExecutorInfo{}))
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// preconditions
	qr := newQueuer(nil)
	qr.podQueue.Add(pod, queue.ReplaceExisting)
	assert.Equal(1, len(qr.podQueue.List()))
	_, found := qr.podQueue.Get("default/foo")
	assert.True(found)

	// exec & post conditions
	d := &deleter{
		api: obj,
		qr:  qr,
	}
	err = d.deleteOne(pod)
	assert.Nil(err)
	_, found = qr.podQueue.Get("foo0")
	assert.False(found)
	assert.Equal(0, len(qr.podQueue.List()))
	obj.AssertExpectations(t)
}

func TestDeleteOne_Running(t *testing.T) {
	assert := assert.New(t)
	obj := &MockScheduler{}
	reg := podtask.NewInMemoryRegistry()
	obj.On("tasks").Return(reg)

	pod := &Pod{Pod: &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "foo",
			UID:       "foo0",
			Namespace: api.NamespaceDefault,
		}}}
	task, err := reg.Register(podtask.New(api.NewDefaultContext(), "bar", *pod.Pod, &mesos.ExecutorInfo{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task.Set(podtask.Launched)
	err = reg.Update(task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// preconditions
	qr := newQueuer(nil)
	qr.podQueue.Add(pod, queue.ReplaceExisting)
	assert.Equal(1, len(qr.podQueue.List()))
	_, found := qr.podQueue.Get("default/foo")
	assert.True(found)

	obj.On("killTask", task.ID).Return(nil)

	// exec & post conditions
	d := &deleter{
		api: obj,
		qr:  qr,
	}
	err = d.deleteOne(pod)
	assert.Nil(err)
	_, found = qr.podQueue.Get("foo0")
	assert.False(found)
	assert.Equal(0, len(qr.podQueue.List()))
	obj.AssertExpectations(t)
}

func TestDeleteOne_badPodNaming(t *testing.T) {
	assert := assert.New(t)
	obj := &MockScheduler{}
	pod := &Pod{Pod: &api.Pod{}}
	d := &deleter{
		api: obj,
		qr:  newQueuer(nil),
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
