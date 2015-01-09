package scheduler

import (
	"fmt"
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
func (k *KubernetesScheduler) enqueuePods() {
	go util.Forever(func() {
		for {
			pod := k.podStore.Pop().(*Pod)
			if pod.Status.Host != "" {
				// skip updates for pods that are already scheduled
				// TODO(jdef): we should probably send update messages to the executor task
				continue
			}
			now := time.Now()
			pod.deadline = &now
			// use ReplaceExisting because we are always pushing the latest state
			k.podQueue.Offer(pod.GetUID(), pod, queue.ReplaceExisting)
		}
	}, 1*time.Second)
}

// implementation of scheduling plugin's NextPod func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) yield() *api.Pod {
	kpod := k.podQueue.Pop().(*Pod)
	return kpod.Pod
}

// implementation of scheduling plugin's Error func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) handleSchedulingError(backoff *podBackoff, pod *api.Pod, err error) {

	if err == noSuchPodErr {
		log.V(2).Infof("Not rescheduling non-existent pod %v", pod.Name)
		return
	}

	log.Infof("Error scheduling %v: %v; retrying", pod.Name, err)
	defer util.HandleCrash()

	ctx := api.WithNamespace(api.NewDefaultContext(), pod.Namespace)
	// default upstream scheduler passes pod.Name as binding.PodID
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
		//TODO(jdef): implement a real backoff duration here
		delay := 7 * time.Second
		// use KeepExisting in case the pod has already been updated (can happen if binding fails
		// due to constraint voilations); we don't want to overwrite a newer entry with stale data.
		k.podQueue.Add(pod.UID, &Pod{Pod: pod, delay: &delay}, queue.KeepExisting)
	default:
		log.V(2).Infof("Task is no longer pending, aborting reschedule for pod %v", podKey)
	}

	/*
		// Retry asynchronously.
		// Note that this is extremely rudimentary and we need a more real error handling path.
		go func() {
			// did we error out because if non-matching offers? if so, register an offer
			// listener to be notified if/when a matching offer comes in.
			var offersAvailable <-chan empty
			if err == noSuitableOffersErr {
				offersAvailable = k.offers.Listen(podKey, func(offer *mesos.Offer) bool {
					k.RLock()
					defer k.RUnlock()
					if taskId, ok := k.podToTask[podKey]; ok {
						switch task, state := k.getTask(taskId); state {
						case statePending:
							return task.AcceptOffer(offer)
						}
					}
					return false
				})
			}
			backoff.wait(podKey, offersAvailable)

			// Get the pod again; it may have changed/been scheduled already.
			pod, err = k.client.Pods(pod.Namespace).Get(pod.Name)
			if err != nil {
				log.Warningf("Failed to get pod %v for retry: %v; abandoning", podKey, err)

				// avoid potential pod leak..
				k.Lock()
				defer k.Unlock()
				if taskId, exists := k.podToTask[podKey]; exists {
					delete(k.pendingTasks, taskId)
					delete(k.podToTask, podKey)
				}
				return
			}
			if pod.Status.Host == "" {
				// ensure that the pod hasn't been deleted while we were trying to schedule it
				log.V(3).Infof("ensure that the pod hasn't been deleted while we're trying to reschedule it: %v", podKey)
				k.Lock()
				defer k.Unlock()

				if taskId, exists := k.podToTask[podKey]; exists {
					if task, ok := k.pendingTasks[taskId]; ok && !task.hasAcceptedOffer() {
						// "pod" now refers to a Pod instance that is not pointed to by the PodTask, so update our records
						task.Pod = pod
						k.podQueue.Add(pod.UID, &delayPod{pod, 5*time.Second}) //TODO(jdef) use backoff here
					} else {
						// this state shouldn't really be possible, so I'm warning if we ever see it
						log.Errorf("Scheduler detected pod no longer pending: %v, will not re-queue; possible offer leak", podKey)
					}
				} else {
					log.Infof("Scheduler detected deleted pod: %v, will not re-queue", podKey)
				}
			} else {
				log.Warningf("Pod already rescheduled? Aborting %+v", pod)
			}
		}()
	*/
}

// currently monitors for "pod deleted" events, upon which handlePodDeleted()
// is invoked.
func (k *KubernetesScheduler) monitorPodEvents() {
	for {
		entry := <-k.updates
		log.V(3).Infof("Received pod entry: %+v", entry)

		pod := entry.Value().(*Pod)
		if !entry.Is(queue.DELETE_EVENT) {
			continue
		}
		if err := k.handlePodDeleted(pod); err != nil {
			log.Error(err)
		}
	}
}

func (k *KubernetesScheduler) handlePodDeleted(pod *Pod) error {
	ctx := api.WithNamespace(api.NewDefaultContext(), pod.Namespace)
	podKey, err := makePodKey(ctx, pod.Name)
	if err != nil {
		return err
	}

	log.V(2).Infof("pod deleted: %v", podKey)

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

	// TODO(jdef) when we start up there may already be pods in the registry
	// that we don't have tasks created for -- need to reconcile these.

	// Watch and queue pods that need scheduling.
	cache.NewReflector(k.createAllPodsLW(), &api.Pod{}, k.podStore).Run()
	k.enqueuePods()

	go util.Forever(func() {
		k.monitorPodEvents()
	}, 1*time.Second)

	podBackoff := podBackoff{
		perPodBackoff: map[string]*backoffEntry{},
		clock:         realClock{},
	}
	return &plugin.Config{
		MinionLister: nil,
		Algorithm:    k,
		Binder:       k,
		NextPod: func() *api.Pod {
			return k.yield()
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
