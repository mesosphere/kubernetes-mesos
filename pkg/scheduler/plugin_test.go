package scheduler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/testapi"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/record"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"

	log "github.com/golang/glog"
	mesos "github.com/mesos/mesos-go/mesosproto"
	util "github.com/mesos/mesos-go/mesosutil"
	bindings "github.com/mesos/mesos-go/scheduler"
	"github.com/mesosphere/kubernetes-mesos/pkg/executor/messages"
	"github.com/mesosphere/kubernetes-mesos/pkg/queue"
	schedcfg "github.com/mesosphere/kubernetes-mesos/pkg/scheduler/config"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/ha"
	"github.com/mesosphere/kubernetes-mesos/pkg/scheduler/podtask"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// A apiserver mock which partially mocks the pods API
type TestServer struct {
	server *httptest.Server
	Stats map[string]uint
	lock sync.Mutex
}
func NewTestServer(t *testing.T, namespace string, pods *api.PodList) *TestServer {
	ts := TestServer{
		Stats: map[string]uint{},
	}
	mux := http.NewServeMux()

	mux.HandleFunc(testapi.ResourcePath("pods", namespace, ""), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(runtime.EncodeOrDie(testapi.Codec(), pods)))
	})

	mux.HandleFunc(testapi.ResourcePath("pods", namespace, "") + "/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.SplitN(r.URL.Path, "/", 5)[4]

		// update statistics for this pod
		ts.lock.Lock()
		defer ts.lock.Unlock()
		ts.Stats[name] = ts.Stats[name] + 1

		for _, p := range pods.Items {
			if p.Name == name {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(runtime.EncodeOrDie(testapi.Codec(), &p)))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("/", func(res http.ResponseWriter, req *http.Request) {
		t.Errorf("unexpected request: %v", req.RequestURI)
		res.WriteHeader(http.StatusNotFound)
	})

	ts.server = httptest.NewServer(mux)
	return &ts
}

// Create mock of pods ListWatch, usually listening on the apiserver pods watch endpoint
type MockPodsListWatch struct {
	ListWatch   cache.ListWatch
	fakeWatcher *watch.FakeWatcher
	list        api.PodList
}
func NewMockPodsListWatch(initialPodList api.PodList) *MockPodsListWatch {
	lw := MockPodsListWatch{
		fakeWatcher: watch.NewFake(),
		list:        initialPodList,
	}
	lw.ListWatch = cache.ListWatch{
		WatchFunc: func(resourceVersion string) (watch.Interface, error) {
			return lw.fakeWatcher, nil
		},
		ListFunc: func() (runtime.Object, error) {
			return &lw.list, nil
		},
	}
	return &lw
}
func (lw *MockPodsListWatch) Add(pod *api.Pod, notify bool) {
	lw.list.Items = append(lw.list.Items, *pod)
	if notify {
		lw.fakeWatcher.Add(pod)
	}
}
func (lw *MockPodsListWatch) Modify(pod *api.Pod, notify bool) {
	for i, otherPod := range lw.list.Items {
		if otherPod.Name == pod.Name {
			lw.list.Items[i] = *pod
			if notify {
				lw.fakeWatcher.Modify(pod)
			}
			return
		}
	}
	log.Fatalf("Cannot find pod %v to modify in MockPodsListWatch", pod.Name)
}
func (lw *MockPodsListWatch) Delete(pod *api.Pod, notify bool) {
	for i, otherPod := range lw.list.Items {
		if otherPod.Name == pod.Name {
			lw.list.Items = append(lw.list.Items[:i], lw.list.Items[i+1:]...)
			if notify {
				lw.fakeWatcher.Delete(&otherPod)
			}
			return
		}
	}
	log.Fatalf("Cannot find pod %v to delete in MockPodsListWatch", pod.Name)
}

// Create a pod with a given index, requiring one port
func NewTestPod(i int) *api.Pod {
	name := fmt.Sprintf("pod%d", i)
	return &api.Pod{
		TypeMeta: api.TypeMeta{APIVersion: testapi.Version()},
		ObjectMeta: api.ObjectMeta{
			Name:      name,
			Namespace: "default",
			SelfLink:  fmt.Sprintf("http://1.2.3.4/api/v1beta1/pods/%v", i),
		},
		Spec: api.PodSpec{
			Containers: []api.Container{
				{
					Ports: []api.ContainerPort{
						{
							ContainerPort: 8000 + i,
							Protocol:      api.ProtocolTCP,
						},
					},
				},
			},
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

// Offering some cpus and memory and the 8000-9000 port range
func NewTestOffer(i int) *mesos.Offer {
	hostname := fmt.Sprintf("h%d", i)
	cpus := util.NewScalarResource("cpus", 3.75)
	mem := util.NewScalarResource("mem", 940)
	var port8000 uint64 = 8000
	var port9000 uint64 = 9000
	ports8000to9000 := mesos.Value_Range{Begin: &port8000, End: &port9000}
	ports := util.NewRangesResource("ports", []*mesos.Value_Range{&ports8000to9000})
	return &mesos.Offer{
		Id:        util.NewOfferID(fmt.Sprintf("offer%d", i)),
		Hostname:  &hostname,
		SlaveId:   util.NewSlaveID(hostname),
		Resources: []*mesos.Resource{cpus, mem, ports},
	}
}

// Add assertions to reason about event streams
type EventPredicate func(e *api.Event) bool
type EventAssertions struct {
	assert.Assertions
}
func (a *EventAssertions) Event(pred EventPredicate, msgAndArgs ...interface{}) bool {
	// parse msgAndArgs: first possibly a duration, otherwise a format string with further args
	timeout := time.Second * 2
	msg := "event not received"
	msgArgStart := 0
	if len(msgAndArgs) > 0 {
		switch msgAndArgs[0].(type) {
		case time.Duration:
			timeout = msgAndArgs[0].(time.Duration)
			msgArgStart += 1
		}
	}
	if len(msgAndArgs) > msgArgStart {
		msg = fmt.Sprintf(msgAndArgs[msgArgStart].(string), msgAndArgs[msgArgStart+1:]...)
	}

	// watch events
	result := make(chan struct{})
	matched := false
	eventWatch := record.GetEvents(func(e *api.Event) {
		if matched {
			return
		}
		if pred(e) {
			log.V(3).Infof("found asserted event for reason '%v': %v", e.Reason, e.Message)
			matched = true
			result <- struct{}{}
		} else {
			log.V(5).Infof("ignoring not-asserted event for reason '%v': %v", e.Reason, e.Message)
		}
	})
	defer eventWatch.Stop()

	// wait for watch to match or timeout
	select {
	case <-result:
		return true
	case <-time.After(timeout):
		return a.Fail(msg)
	}
}
func (a *EventAssertions) EventWithReason(reason string, msgAndArgs ...interface{}) bool {
	return a.Event(func(e *api.Event) bool {
		return e.Reason == reason
	}, msgAndArgs...)
}
func (a *EventAssertions) EventuallyTrue(timeout time.Duration, fn func() bool, msgAndArgs ...interface{}) bool {
	start := time.Now()
	for {
		if fn() {
			return true
		}
		if time.Now().Sub(start) > timeout {
			if len(msgAndArgs) > 0 {
				return a.Fail(msgAndArgs[0].(string), msgAndArgs[1:]...)
			} else {
				return a.Fail("predicate fn has not been true after %v", timeout.String())
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Extend the MockSchedulerDriver with a blocking Join method
type StatefullMockSchedulerDriver struct {
	MockSchedulerDriver
	stopped chan struct{}
	aborted chan struct{}
	status  mesos.Status
}
func (m *StatefullMockSchedulerDriver) implementationCalled(arguments ...interface{}) {
	// get the calling function's name
	pc, _, _, ok := goruntime.Caller(1)
	if !ok {
		panic("Couldn't get the caller information")
	}
	functionPath := goruntime.FuncForPC(pc).Name()
	parts := strings.Split(functionPath, ".")
	functionName := parts[len(parts)-1]

	// only register the call, not expected call processing
	m.Calls = append(m.Calls, mock.Call{functionName, arguments, make([]interface{}, 0), 0, nil, nil})
}
func (m *StatefullMockSchedulerDriver) Start() (mesos.Status, error) {
	m.implementationCalled()
	if m.status != mesos.Status_DRIVER_NOT_STARTED {
		return m.status, errors.New("cannot start driver which isn't in status NOT_STARTED")
	}
	m.status = mesos.Status_DRIVER_RUNNING
	return m.status, nil
}
func (m *StatefullMockSchedulerDriver) Stop(b bool) (mesos.Status, error) {
	m.implementationCalled(b)
	close(m.stopped)
	m.status = mesos.Status_DRIVER_STOPPED
	return m.status, nil
}
func (m *StatefullMockSchedulerDriver) Abort() (mesos.Status, error) {
	m.implementationCalled()
	close(m.aborted)
	m.status = mesos.Status_DRIVER_ABORTED
	return m.status, nil
}
func (m *StatefullMockSchedulerDriver) Join() (mesos.Status, error) {
	m.implementationCalled()
	select {
	case <-m.stopped:
		log.Info("JoinableMockSchedulerDriver stopped")
		return mesos.Status_DRIVER_STOPPED, nil
	case <-m.aborted:
		log.Info("JoinableMockSchedulerDriver aborted")
		return mesos.Status_DRIVER_ABORTED, nil
	}
	return mesos.Status_DRIVER_ABORTED, errors.New("unknown reason for join")
}
func (m *StatefullMockSchedulerDriver) CallsFor(methodName string) []*mock.Call {
	methodCalls := []*mock.Call{}
	for _, c := range m.Calls {
		if c.Method == methodName {
			methodCalls = append(methodCalls, &c)
		}
	}
	return methodCalls
}

// Create mesos.TaskStatus for a given task
func newTaskStatusForTask(task *mesos.TaskInfo, state mesos.TaskState) *mesos.TaskStatus {
	healthy := state == mesos.TaskState_TASK_RUNNING
	ts := float64(time.Now().Nanosecond()) / 1000000000.0
	source := mesos.TaskStatus_SOURCE_EXECUTOR
	return &mesos.TaskStatus{
		TaskId:     task.TaskId,
		State:      &state,
		SlaveId:    task.SlaveId,
		ExecutorId: task.Executor.ExecutorId,
		Timestamp:  &ts,
		Healthy:    &healthy,
		Source:     &source,
		Data:       task.Data,
	}
}

// Test to create the scheduler plugin with an empty plugin config
func TestPlugin_New(t *testing.T) {
	assert := assert.New(t)

	c := PluginConfig{}
	p := NewPlugin(&c)
	assert.NotNil(p)
}

// Test to create the scheduler plugin with the config returned by the scheduler,
// and play through the whole life cycle of the plugin while creating pods, deleting
// and failing them.
func TestPlugin_LifeCycle(t *testing.T) {
	assert := &EventAssertions{*assert.New(t)}

	// create a fake pod watch. We use that below to submit new pods to the scheduler
	podListWatch := NewMockPodsListWatch(api.PodList{})

	// create fake apiserver
	testApiServer := NewTestServer(t, api.NamespaceDefault, &podListWatch.list)
	defer testApiServer.server.Close()

	// create scheduler
	testScheduler := New(Config{
		Executor: util.NewExecutorInfo(
			util.NewExecutorID("executor-id"),
			util.NewCommandInfo("executor-cmd"),
		),
		Client:       client.NewOrDie(&client.Config{Host: testApiServer.server.URL, Version: testapi.Version()}),
		ScheduleFunc: FCFSScheduleFunc,
		Schedcfg:     *schedcfg.CreateDefaultConfig(),
	})

	assert.NotNil(testScheduler.client, "client is nil")
	assert.NotNil(testScheduler.executor, "executor is nil")
	assert.NotNil(testScheduler.offers, "offer registry is nil")

	// create scheduler process
	schedulerProcess := ha.New(testScheduler)

	// get plugin config from it
	c := testScheduler.NewPluginConfig(schedulerProcess.Terminal(), http.DefaultServeMux, func() *cache.ListWatch {
		return &podListWatch.ListWatch
	})
	assert.NotNil(c)

	// create plugin
	p := NewPlugin(c)
	assert.NotNil(p)

	// run plugin
	p.Run(schedulerProcess.Terminal())
	defer schedulerProcess.End()

	// init scheduler
	err := testScheduler.Init(schedulerProcess.Master(), p, http.DefaultServeMux)
	assert.NoError(err)

	// create mock mesos scheduler driver
	mockDriver := StatefullMockSchedulerDriver{
		status: mesos.Status_DRIVER_NOT_STARTED,
	}
	var launchTasks_taskInfos []*mesos.TaskInfo

	mockDriver.On("ReconcileTasks", mock.AnythingOfType("[]*mesosproto.TaskStatus")).Return(mockDriver.status, nil)
	mockDriver.On("SendFrameworkMessage",
		mock.AnythingOfType("*mesosproto.ExecutorID"),
		mock.AnythingOfType("*mesosproto.SlaveID"),
		mock.AnythingOfType("string"),
	).Return(mockDriver.status, nil)
	mockDriver.On("LaunchTasks",
		mock.AnythingOfType("[]*mesosproto.OfferID"),
		mock.AnythingOfType("[]*mesosproto.TaskInfo"),
		mock.AnythingOfType("*mesosproto.Filters"),
	).Return(mockDriver.status, nil).Run(func(args mock.Arguments) {
		launchTasks_taskInfos = args.Get(1).([]*mesos.TaskInfo)
	})
	mockDriver.On("KillTask",
		mock.AnythingOfType("*mesosproto.TaskID"),
	).Return(mockDriver.status, nil)

	// elect master with mock driver
	driverFactory := ha.DriverFactory(func() (bindings.SchedulerDriver, error) {
		return &mockDriver, nil
	})
	schedulerProcess.Elect(driverFactory)
	elected := schedulerProcess.Elected()

	// driver will be started
	assert.EventuallyTrue(time.Second, func() bool { return len(mockDriver.CallsFor("Start")) > 0 })

	// tell scheduler to be registered
	testScheduler.Registered(
		&mockDriver,
		util.NewFrameworkID("kubernetes-id"),
		util.NewMasterInfo("master-id", (192<<24)+(168<<16)+(0<<8)+1, 5050),
	)

	// wait for being elected
	_ = <-elected

	// fake new, unscheduled pod
	pod1 := NewTestPod(1)
	podListWatch.Add(pod1, true) // notify watchers

	// wait for failedScheduling event because there is no offer
	assert.EventWithReason("failedScheduling", "failedScheduling event not received")

	// add some matching offer
	offers1 := []*mesos.Offer{NewTestOffer(1)}
	testScheduler.ResourceOffers(nil, offers1)

	// and wait for scheduled pod
	assert.EventWithReason("scheduled")
	mockDriver.AssertNumberOfCalls(t, "LaunchTasks", 1)
	assert.Equal(1, len(launchTasks_taskInfos))

	// report back that the task has been staged by mesos
	launchedTask := launchTasks_taskInfos[0]
	testScheduler.StatusUpdate(&mockDriver, newTaskStatusForTask(launchedTask, mesos.TaskState_TASK_STAGING))

	// report back that the task has been started by mesos
	testScheduler.StatusUpdate(&mockDriver, newTaskStatusForTask(launchedTask, mesos.TaskState_TASK_RUNNING))

	// report back that the task has been lost
	mockDriver.AssertNumberOfCalls(t, "SendFrameworkMessage", 0)
	testScheduler.StatusUpdate(&mockDriver, newTaskStatusForTask(launchedTask, mesos.TaskState_TASK_LOST))

	// and wait that framework message is sent to executor
	mockDriver.AssertNumberOfCalls(t, "SendFrameworkMessage", 1)

	// start another pod
	podNum := 1
	startPod := func (offers []*mesos.Offer) *api.Pod {
		podNum = podNum + 1
		launchTasksCallsBefore := len(mockDriver.CallsFor("LaunchTasks"))
		pod := NewTestPod(podNum)
		podListWatch.Add(pod, true) // notify watchers
		testScheduler.ResourceOffers(&mockDriver, offers)
		assert.EventWithReason("scheduled")
		mockDriver.AssertNumberOfCalls(t, "LaunchTasks", launchTasksCallsBefore + 1)
		return pod
	}
	pod := startPod(offers1)
	launchedTask = launchTasks_taskInfos[0]
	testScheduler.StatusUpdate(&mockDriver, newTaskStatusForTask(launchedTask, mesos.TaskState_TASK_STAGING))
	testScheduler.StatusUpdate(&mockDriver, newTaskStatusForTask(launchedTask, mesos.TaskState_TASK_RUNNING))

	// stop it again via the apiserver mock
	killTaskCallsBefore := len(mockDriver.CallsFor("KillTask"))
	podListWatch.Delete(pod, true) // notify watchers

	// and wait for the driver killTask call with the correct TaskId
	assert.EventuallyTrue(time.Second, func() bool { return len(mockDriver.CallsFor("KillTask")) > killTaskCallsBefore })
	assert.Equal(*launchedTask.TaskId, *(mockDriver.CallsFor("KillTask")[killTaskCallsBefore].Arguments.Get(0).(*mesos.TaskID)), "expected same TaskID as during launch")

	// report back that the task is finished
	testScheduler.StatusUpdate(&mockDriver, newTaskStatusForTask(launchedTask, mesos.TaskState_TASK_FINISHED))

	// start pods:
	// - which are failing while binding,
	// - leading to reconciliation
	// - with different states on the apiserver

	failPodFromExecutor := func (task *mesos.TaskInfo) {
		beforePodLookups := testApiServer.Stats[pod.Name]
		status := newTaskStatusForTask(task, mesos.TaskState_TASK_FAILED)
		message := messages.CreateBindingFailure
		status.Message = &message
		testScheduler.StatusUpdate(&mockDriver, status)
		assert.EventuallyTrue(time.Second, func () bool {
			return testApiServer.Stats[pod.Name] == beforePodLookups + 1
		}, "expect that reconcilePod will access apiserver for pod %v", pod.Name)
	}

	// 1. with pod deleted from the apiserver
	pod = startPod(offers1)
	podListWatch.Delete(pod, false) // not notifying the watchers
	failPodFromExecutor(launchTasks_taskInfos[0])

	// 2. with pod still on the apiserver, not bound
	pod = startPod(offers1)
	failPodFromExecutor(launchTasks_taskInfos[0])

	// 3. with pod still on the apiserver, bound i.e. host!=""
	pod = startPod(offers1)
	pod.Status.Host = *offers1[0].Hostname
	podListWatch.Modify(pod, false) // not notifying the watchers
	failPodFromExecutor(launchTasks_taskInfos[0])

	// 4. with pod still on the apiserver, bound i.e. host!="", notified wie ListWatch
	pod = startPod(offers1)
	pod.Status.Host = *offers1[0].Hostname
	podListWatch.Modify(pod, true) // not notifying the watchers
	time.Sleep(time.Second / 2)
	failPodFromExecutor(launchTasks_taskInfos[0])
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
