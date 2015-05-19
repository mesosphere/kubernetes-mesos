package scheduler

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/testapi"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"

	mesos "github.com/mesos/mesos-go/mesosproto"
	"github.com/mesos/mesos-go/mesosutil"
	bindings "github.com/mesos/mesos-go/scheduler"
	"github.com/mesosphere/kubernetes-mesos/pkg/queue"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/ha"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/podtask"
	"github.com/stretchr/testify/assert"
)

type serverResponse struct {
	statusCode int
	obj        interface{}
}

/* @sttts: newPodList and makeTestServer are copies from endpoint_controller_test.go.
 *         They should be moved to testapi or somewhere else probably.
 */
func newPodList(nPods int, nPorts int) *api.PodList {
	pods := []api.Pod{}
	for i := 0; i < nPods; i++ {
		p := api.Pod{
			TypeMeta:   api.TypeMeta{APIVersion: testapi.Version()},
			ObjectMeta: api.ObjectMeta{Name: fmt.Sprintf("pod%d", i)},
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
		for j := 0; j < nPorts; j++ {
			p.Spec.Containers[0].Ports = append(p.Spec.Containers[0].Ports, api.ContainerPort{ContainerPort: 8080 + j})
		}
		pods = append(pods, p)
	}
	return &api.PodList{
		TypeMeta: api.TypeMeta{APIVersion: testapi.Version(), Kind: "PodList"},
		Items:    pods,
	}
}

func makeTestServer(t *testing.T, namespace string, podResponse, serviceResponse, endpointsResponse serverResponse) (*httptest.Server, *util.FakeHandler) {
	fakePodHandler := util.FakeHandler{
		StatusCode:   podResponse.statusCode,
		ResponseBody: runtime.EncodeOrDie(testapi.Codec(), podResponse.obj.(runtime.Object)),
	}
	fakeServiceHandler := util.FakeHandler{
		StatusCode:   serviceResponse.statusCode,
		ResponseBody: runtime.EncodeOrDie(testapi.Codec(), serviceResponse.obj.(runtime.Object)),
	}
	fakeEndpointsHandler := util.FakeHandler{
		StatusCode:   endpointsResponse.statusCode,
		ResponseBody: runtime.EncodeOrDie(testapi.Codec(), endpointsResponse.obj.(runtime.Object)),
	}

	mux := http.NewServeMux()
	mux.Handle(testapi.ResourcePath("pods", namespace, ""), &fakePodHandler)
	mux.Handle(testapi.ResourcePath("services", "", ""), &fakeServiceHandler)
	mux.Handle(testapi.ResourcePath("endpoints", namespace, ""), &fakeEndpointsHandler)
	mux.Handle(testapi.ResourcePath("endpoints/", namespace, ""), &fakeEndpointsHandler)
	mux.HandleFunc("/", func(res http.ResponseWriter, req *http.Request) {
		t.Errorf("unexpected request: %v", req.RequestURI)
		res.WriteHeader(http.StatusNotFound)
	})
	return httptest.NewServer(mux), &fakeEndpointsHandler
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
func (lw *MockPodsListWatch) Add(pod api.Pod) {
	lw.list.Items = append(lw.list.Items, pod)
	lw.fakeWatcher.Add(&pod)
}
func (lw *MockPodsListWatch) Modify(pod api.Pod) {
	for i, otherPod := range lw.list.Items {
		if otherPod.Name == pod.Name {
			lw.list.Items[i] = pod
			lw.fakeWatcher.Modify(&pod)
			return
		}
	}
	log.Panicf("Cannot find pod %v to modify in MockPodsListWatch", pod.Name)
}
func (lw *MockPodsListWatch) Delete(pod api.Pod) {
	for i, otherPod := range lw.list.Items {
		if otherPod.Name == pod.Name {
			lw.list.Items = append(lw.list.Items[:i], lw.list.Items[i+1:]...)
			lw.fakeWatcher.Delete(&otherPod)
			return
		}
	}
	log.Panicf("Cannot find pod %v to delete in MockPodsListWatch", pod.Name)
}

func TestPlugin_NewFromScheduler(t *testing.T) {
	assert := assert.New(t)

	// create fake apiserver
	testApiServer, _ := makeTestServer(t, api.NamespaceDefault,
		serverResponse{http.StatusOK, newPodList(0, 0)},
		serverResponse{http.StatusOK, &api.ServiceList{}},
		serverResponse{http.StatusOK, &api.Endpoints{}})
	defer testApiServer.Close()

	// create a fake pod watch
	podListWatch := NewMockPodsListWatch(api.PodList{})

	// create scheduler
	testScheduler := New(Config{
		Executor: mesosutil.NewExecutorInfo(
			mesosutil.NewExecutorID("executor-id"),
			mesosutil.NewCommandInfo("executor-cmd"),
		),
		Client: client.NewOrDie(&client.Config{Host: testApiServer.URL, Version: testapi.Version()}),
		PodsListWatch: &podListWatch.ListWatch,
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
