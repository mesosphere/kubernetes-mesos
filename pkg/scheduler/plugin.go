package scheduler

import (
	"fmt"
	"sync"
	"time"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/meta"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/envvars"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	algorithm "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
	plugin "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/scheduler"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	"github.com/mesosphere/kubernetes-mesos/pkg/queue"
	"gopkg.in/v1/yaml"
)

// implements binding.Registry, launches the pod-associated-task in mesos
func (k *KubernetesScheduler) Bind(binding *api.Binding) error {

	ctx := api.WithNamespace(api.NewDefaultContext(), binding.Namespace)

	// default upstream scheduler passes pod.Name as binding.PodID
	podKey, err := makePodKey(ctx, binding.PodID)
	if err != nil {
		return err
	}

	k.Lock()
	defer k.Unlock()

	taskId, exists := k.podToTask[podKey]
	if !exists {
		log.Infof("Could not resolve pod %s to task id", podKey)
		return noSuchPodErr
	}

	task, exists := k.pendingTasks[taskId]
	if !exists {
		// in this case it's likely that the pod has been deleted between Schedule
		// and Bind calls
		log.Infof("No pending task for pod %s", podKey)
		return noSuchPodErr
	}

	// sanity check: ensure that the task hasAcceptedOffer(), it's possible that between
	// Schedule() and now that the offer for this task was rescinded or invalidated.
	// ((we should never see this here))
	if !task.hasAcceptedOffer() {
		return fmt.Errorf("task has not accepted a valid offer, pod %v", podKey)
	}

	// By this time, there is a chance that the slave is disconnected.
	offerId := task.GetOfferId()
	if offer, ok := k.offers.Get(offerId); !ok || offer.HasExpired() {
		// already rescinded or timed out or otherwise invalidated
		task.Offer.Release()
		task.ClearTaskInfo()
		return fmt.Errorf("failed prior to launchTask due to expired offer, pod %v", podKey)
	}

	if err = k.prepareTaskForLaunch(ctx, binding.Host, task); err == nil {
		log.V(2).Infof("Attempting to bind %v to %v", binding.PodID, binding.Host)
		if err = k.client.Post().Namespace(api.Namespace(ctx)).Path("bindings").Body(binding).Do().Error(); err == nil {
			log.V(2).Infof("Launching task : %v", task)
			taskList := []*mesos.TaskInfo{task.TaskInfo}
			if err = k.driver.LaunchTasks(task.Offer.Details().Id, taskList, nil); err == nil {
				k.offers.Invalidate(offerId)
				task.Pod.Status.Host = binding.Host
				task.Launched = true
				return nil
			}
		}
	}
	task.Offer.Release()
	task.ClearTaskInfo()
	return fmt.Errorf("Failed to launch task for pod %s: %v", podKey, err)
}

func (k *KubernetesScheduler) prepareTaskForLaunch(ctx api.Context, machine string, task *PodTask) error {
	pod, err := k.client.Pods(api.Namespace(ctx)).Get(task.Pod.Name)
	if err != nil {
		return err
	}

	//HACK(jdef): adapted from https://github.com/GoogleCloudPlatform/kubernetes/blob/release-0.6/pkg/registry/pod/bound_pod_factory.go
	envVars, err := k.getServiceEnvironmentVariables(ctx)
	if err != nil {
		return err
	}

	boundPod := &api.BoundPod{}
	if err := api.Scheme.Convert(pod, boundPod); err != nil {
		return err
	}
	for ix, container := range boundPod.Spec.Containers {
		boundPod.Spec.Containers[ix].Env = append(container.Env, envVars...)
	}
	// Make a dummy self link so that references to this bound pod will work.
	boundPod.SelfLink = "/api/v1beta1/boundPods/" + boundPod.Name

	// update the boundPod here to pick up things like environment variables that
	// pod containers will use for service discovery. the kubelet-executor uses this
	// boundPod to instantiate the pods and this is the last update we make before
	// firing up the pod.
	task.TaskInfo.Data, err = yaml.Marshal(&boundPod)
	if err != nil {
		log.V(2).Infof("Failed to marshal the updated boundPod")
		return err
	}
	return nil
}

// getServiceEnvironmentVariables populates a list of environment variables that are use
// in the container environment to get access to services.
// HACK(jdef): adapted from https://github.com/GoogleCloudPlatform/kubernetes/blob/release-0.6/pkg/registry/pod/bound_pod_factory.go
func (k *KubernetesScheduler) getServiceEnvironmentVariables(ctx api.Context) ([]api.EnvVar, error) {
	var result []api.EnvVar
	services, err := k.client.Services(api.Namespace(ctx)).List(labels.Everything())
	if err != nil {
		return result, err
	}
	return envvars.FromServices(services), nil
}

// Schedule implements the Scheduler interface of the Kubernetes.
// It returns the selectedMachine's name and error (if there's any).
func (k *KubernetesScheduler) Schedule(pod api.Pod, unused algorithm.MinionLister) (string, error) {
	log.Infof("Try to schedule pod %v\n", pod.Name)
	ctx := api.WithNamespace(api.NewDefaultContext(), pod.Namespace)

	// default upstream scheduler passes pod.Name as binding.PodID
	podKey, err := makePodKey(ctx, pod.Name)
	if err != nil {
		return "", err
	}

	k.Lock()
	defer k.Unlock()

	if taskID, ok := k.podToTask[podKey]; !ok {
		// There's a bit of a potential race here, a pod could have been yielded() but
		// and then before we get *here* it could be deleted. We use meta to index the pod
		// in the store since that's what k8s client/cache/reflector does.
		meta, err := meta.Accessor(&pod)
		if err != nil {
			log.Warningf("aborting Schedule(), unable to understand pod object %+v", &pod)
			return "", noSuchPodErr
		}
		if deleted := k.podStore.Poll(meta.Name(), queue.DELETE_EVENT); deleted {
			// avoid scheduling a pod that's been deleted between yield() and Schedule()
			log.Infof("aborting Schedule(), pod has been deleted %+v", &pod)
			return "", noSuchPodErr
		}

		task, err := newPodTask(ctx, &pod, k.executor)
		if err != nil {
			return "", err
		}
		k.podToTask[task.podKey] = task.ID
		k.pendingTasks[task.ID] = task
		return k.doSchedule(task)
	} else {
		if task, found := k.pendingTasks[taskID]; !found {
			// TODO(jdef): this is quite exceptional: we should avoid requeueing the pod for
			// scheduling in the error handler if we see an error like this.
			return "", fmt.Errorf("Task %s is not pending, nothing to schedule", taskID)
		} else if task.Launched {
			return "", fmt.Errorf("Task %s has already been launched, aborting schedule", taskID)
		} else {
			return k.doSchedule(task)
		}
	}
}

// Call ScheduleFunc and subtract some resources, returning the name of the machine the task is scheduled on
func (k *KubernetesScheduler) doSchedule(task *PodTask) (string, error) {
	offer, err := k.scheduleFunc(k.offers, k.slaves, task)
	if err != nil {
		return "", err
	}
	slaveId := offer.Details().GetSlaveId().GetValue()
	if slave, ok := k.slaves[slaveId]; !ok {
		// not much sense in Release()ing the offer here since its owner died
		offer.Release()
		k.offers.Invalidate(offer.Details().Id.GetValue())
		task.ClearTaskInfo()
		return "", fmt.Errorf("Slave disappeared (%v) while scheduling task %v", slaveId, task.ID)
	} else {
		task.FillTaskInfo(offer)
		return slave.HostName, nil
	}
}

// watch for unscheduled pods and queue them up for scheduling
func (k *KubernetesScheduler) enqueuePods(lock sync.Locker) {
	enqueuePod := func() {
		sleepTime := 0 * time.Second
		defer func() { time.Sleep(sleepTime) }()

		lock.Lock()
		defer lock.Unlock()

		// limit blocking here for short intervals so that scheduling
		// may proceed even if there have been no recent pod changes
		p := k.podStore.Await(200 * time.Millisecond)
		if p == nil {
			sleepTime = 1 * time.Second
			return
		}

		pod := p.(*Pod)
		if pod.Status.Host != "" {
			k.podQueue.Delete(pod.GetUID())
		} else {
			// use ReplaceExisting because we are always pushing the latest state
			now := time.Now()
			pod.deadline = &now
			k.podQueue.Offer(pod.GetUID(), pod, queue.ReplaceExisting)
			log.V(3).Infof("queued pod for scheduling: %v", pod.Pod.Name)
		}
	}
	go util.Forever(func() {
		log.Info("Watching for newly created pods")
		for {
			enqueuePod()
		}
	}, 1*time.Second)
}

// implementation of scheduling plugin's NextPod func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) yield(lock sync.Locker) (pod *api.Pod) {
	log.V(2).Info("attempting to yield a pod")
	pop := func() (found bool) {
		sleepTime := 0 * time.Second
		defer func() { time.Sleep(sleepTime) }()

		lock.Lock()
		defer lock.Unlock()

		// limit blocking here to short intervals so that enqueuePods
		// can feed us pods for scheduling
		kpod := k.podQueue.Await(200 * time.Millisecond)
		if kpod == nil {
			sleepTime = 1 * time.Second
			return
		}

		pod = kpod.(*Pod).Pod
		if meta, err := meta.Accessor(pod); err != nil {
			log.Warningf("yield unable to understand pod object %+v, will skip", pod)
		} else if !k.podStore.Poll(meta.Name(), queue.POP_EVENT) {
			log.V(1).Infof("yield popped a transitioning pod, skipping: %+v", pod)
		} else if pod.Status.Host != "" {
			// should never happen if enqueuePods is filtering properly
			log.Warningf("yield popped an already-scheduled pod, skipping: %+v", pod)
		} else {
			found = true
		}
		return
	}
	for !pop() {
	}
	return
}

// implementation of scheduling plugin's Error func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) handleSchedulingError(backoff *podBackoff, pod *api.Pod, schedulingErr error) {

	if schedulingErr == noSuchPodErr {
		log.V(2).Infof("Not rescheduling non-existent pod %v", pod.Name)
		return
	}

	log.Infof("Error scheduling %v: %v; retrying", pod.Name, schedulingErr)
	defer util.HandleCrash()

	// default upstream scheduler passes pod.Name as binding.PodID
	ctx := api.WithNamespace(api.NewDefaultContext(), pod.Namespace)
	podKey, err := makePodKey(ctx, pod.Name)
	if err != nil {
		log.Errorf("Failed to construct pod key, aborting scheduling for pod %v: %v", pod.Name, err)
		return
	}

	backoff.gc()
	k.RLock()
	defer k.RUnlock()

	taskId, exists := k.podToTask[podKey]
	if !exists {
		// if we don't have a mapping here any more then someone deleted the pod
		log.V(2).Infof("Could not resolve pod to task, aborting pod reschdule: %s", podKey)
		return
	}

	switch task, state := k.getTask(taskId); state {
	case statePending:
		if task.Launched {
			log.V(2).Infof("Skipping re-scheduling for already-launched pod %v", podKey)
			return
		}
		offersAvailable := queue.BreakChan(nil)
		if schedulingErr == noSuitableOffersErr {
			log.V(3).Infof("adding backoff breakout handler for pod %v", podKey)
			offersAvailable = queue.BreakChan(k.offers.Listen(podKey, func(offer *mesos.Offer) bool {
				k.RLock()
				defer k.RUnlock()
				switch task, state := k.getTask(taskId); state {
				case statePending:
					return !task.Launched && task.AcceptOffer(offer)
				}
				return false
			}))
		}
		delay := backoff.getBackoff(podKey)

		// use KeepExisting in case the pod has already been updated (can happen if binding fails
		// due to constraint voilations); we don't want to overwrite a newer entry with stale data.
		k.podQueue.Add(pod.UID, &Pod{Pod: pod, delay: &delay, notify: offersAvailable}, queue.KeepExisting)
	default:
		log.V(2).Infof("Task is no longer pending, aborting reschedule for pod %v", podKey)
	}
}

// currently monitors for "pod deleted" events, upon which handlePodDeleted()
// is invoked.
func (k *KubernetesScheduler) monitorPodEvents() {
	go util.Forever(func() {
		for {
			entry := <-k.updates
			pod := entry.Value().(*Pod)
			if !entry.Is(queue.DELETE_EVENT) {
				continue
			}
			if err := k.handlePodDeleted(pod); err != nil {
				log.Error(err)
			}
		}
	}, 1*time.Second)
}

func (k *KubernetesScheduler) handlePodDeleted(pod *Pod) error {
	ctx := api.WithNamespace(api.NewDefaultContext(), pod.Namespace)
	podKey, err := makePodKey(ctx, pod.Name)
	if err != nil {
		return err
	}

	log.V(2).Infof("pod deleted: %v", podKey)

	// order is important here: we want to make sure we have the lock before
	// removing the pod from the scheduling queue. this makes the concurrent
	// execution of scheduler-error-handling and delete-handling easier to
	// reason about.
	k.Lock()
	defer k.Unlock()

	// prevent the scheduler from attempting to pop this; it's also possible that
	// it's concurrently being scheduled (somewhere between pod scheduling and
	// binding) - if so, then we'll end up removing it from pendingTasks which
	// will abort Bind()ing
	k.podQueue.Delete(pod.GetUID())

	taskId, exists := k.podToTask[podKey]
	if !exists {
		return fmt.Errorf("Could not resolve pod '%s' to task id", podKey)
	}

	// determine if the task has already been launched to mesos, if not then
	// cleanup is easier (podToTask,pendingTasks) since there's no state to sync
	var killTaskId *mesos.TaskID
	task, state := k.getTask(taskId)

	switch state {
	case statePending:
		if !task.Launched {
			// we've been invoked in between Schedule() and Bind()
			if task.hasAcceptedOffer() {
				task.Offer.Release()
				task.ClearTaskInfo()
			}
			delete(k.podToTask, podKey)
			delete(k.pendingTasks, taskId)
			return nil
		}
		fallthrough
	case stateRunning:
		killTaskId = &mesos.TaskID{Value: proto.String(task.ID)}
	default:
		return fmt.Errorf("Cannot kill pod '%s': pod not found", podKey)
	}
	// signal to watchers that the related pod is going down
	task.Pod.Status.Host = ""
	return k.driver.KillTask(killTaskId)
}

// Create creates a scheduler and all support functions.
func (k *KubernetesScheduler) NewPluginConfig() *plugin.Config {

	// Watch and queue pods that need scheduling.
	cache.NewReflector(k.createAllPodsLW(), &api.Pod{}, k.podStore).Run()

	// lock that guards critial sections that involve transferring pods from
	// the store (cache) to the scheduling queue; its purpose is to maintain
	// an ordering (vs interleaving) of operations that's easier to reason about.
	var transferLock sync.Mutex

	k.enqueuePods(&transferLock)
	k.monitorPodEvents()

	podBackoff := podBackoff{
		perPodBackoff: map[string]*backoffEntry{},
		clock:         realClock{},
	}
	return &plugin.Config{
		MinionLister: nil,
		Algorithm:    k,
		Binder:       k,
		NextPod: func() *api.Pod {
			return k.yield(&transferLock)
		},
		Error: func(pod *api.Pod, err error) {
			k.handleSchedulingError(&podBackoff, pod, err)
		},
	}
}

type listWatch struct {
	client        *client.Client
	fieldSelector labels.Selector
	resource      string
}

func (lw *listWatch) List() (runtime.Object, error) {
	return lw.client.
		Get().
		Path(lw.resource).
		SelectorParam("fields", lw.fieldSelector).
		Do().
		Get()
}

func (lw *listWatch) Watch(resourceVersion string) (watch.Interface, error) {
	return lw.client.
		Get().
		Path("watch").
		Path(lw.resource).
		SelectorParam("fields", lw.fieldSelector).
		Param("resourceVersion", resourceVersion).
		Watch()
}

// createAllPodsLW returns a listWatch that finds all pods
func (k *KubernetesScheduler) createAllPodsLW() *listWatch {
	return &listWatch{
		client:        k.client,
		fieldSelector: labels.Everything(),
		resource:      "pods",
	}
}

// Consumes *api.Pod, produces *Pod; the k8s reflector wants to push *api.Pod
// objects at us, but we want to store more flexible (Pod) type defined in
// this package. The adapter implementation facilitates this. It's a little
// hackish since the object type going in is different than the object type
// coming out -- you've been warned.
type podStoreAdapter struct {
	*queue.HistoricalFIFO
}

func (psa *podStoreAdapter) Add(id string, obj interface{}) {
	pod := obj.(*api.Pod)
	psa.HistoricalFIFO.Add(id, &Pod{Pod: pod})
}

func (psa *podStoreAdapter) Update(id string, obj interface{}) {
	pod := obj.(*api.Pod)
	psa.HistoricalFIFO.Update(id, &Pod{Pod: pod})
}

// Replace will delete the contents of the store, using instead the
// given map. This store implementation does NOT take ownership of the map.
func (psa *podStoreAdapter) Replace(idToObj map[string]interface{}) {
	newmap := map[string]interface{}{}
	for k, v := range idToObj {
		pod := v.(*api.Pod)
		newmap[k] = &Pod{Pod: pod}
	}
	psa.HistoricalFIFO.Replace(newmap)
}

func (p *Pod) Copy() queue.Copyable {
	if p == nil {
		return nil
	}
	//TODO(jdef) we may need a better "deep-copy" implementation
	pod := *(p.Pod)
	return &Pod{Pod: &pod}
}

func (p *Pod) GetUID() string {
	return p.UID
}

func (dp *Pod) Deadline() (time.Time, bool) {
	if dp.deadline != nil {
		return *(dp.deadline), true
	}
	return time.Time{}, false
}

func (dp *Pod) GetDelay() time.Duration {
	if dp.delay != nil {
		return *(dp.delay)
	}
	return 0
}

func (p *Pod) Breaker() queue.BreakChan {
	return p.notify
}
