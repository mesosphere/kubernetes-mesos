package scheduler

import (
	"fmt"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
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
		return fmt.Errorf("Could not resolve pod '%s' to task id", podKey)
	}

	task, exists := k.pendingTasks[taskId]
	if !exists {
		return fmt.Errorf("Pod Task does not exist %v\n", taskId)
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
		log.V(2).Infof("Launching task : %v", task)
		taskList := []*mesos.TaskInfo{task.TaskInfo}
		if err = k.driver.LaunchTasks(task.Offer.Details().Id, taskList, nil); err == nil {
			// we *intentionally* do not record our binding to etcd since we're not using bindings
			// to manage pod lifecycle
			task.Pod.Status.Host = binding.Host
			task.Launched = true
			k.offers.Invalidate(offerId)
			return nil
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

	// TODO(jdef): there's a bit of a race here, a pod could have been yielded() but
	// and then before we get *here* it could be deleted. Unwittingly we'd end up
	// rescheduling it because there's no way to tell that it had recently been deleted.
	// Should we implement a lingering/delay-queueish thing here, like we have for offers,
	// so that we can avoid rescheduling deleted things?

	// TODO(jdef): once we detect the above condition, we'll error out - but we need to
	// avoid requeueing the pod for scheduling for this particular error

	k.Lock()
	defer k.Unlock()

	if taskID, ok := k.podToTask[podKey]; !ok {
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

// implementation of scheduling plugin's NextPod func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) yield() *api.Pod {
	// blocking...
	pod := k.podQueue.Pop().(*api.Pod)
	log.V(2).Infof("About to try and schedule pod %v\n", pod.Name)
	return pod
}

// implementation of scheduling plugin's Error func; see plugin/pkg/scheduler
func (k *KubernetesScheduler) handleSchedulingError(backoff *podBackoff, pod *api.Pod, err error) {
	log.Infof("Error scheduling %v: %v; retrying", pod.Name, err)
	backoff.gc()

	// Retry asynchronously.
	// Note that this is extremely rudimentary and we need a more real error handling path.
	go func() {
		defer util.HandleCrash()
		ctx := api.WithNamespace(api.NewDefaultContext(), pod.Namespace)

		// default upstream scheduler passes pod.Name as binding.PodID
		podKey, err := makePodKey(ctx, pod.Name)
		if err != nil {
			log.Errorf("Failed to build pod key, will not attempt to reschedule pod %v: %v", pod.Name, err)
			return
		}
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
			k.Lock()
			defer k.Unlock()

			if taskId, exists := k.podToTask[podKey]; exists {
				if task, ok := k.pendingTasks[taskId]; ok && !task.hasAcceptedOffer() {
					// "pod" now refers to a Pod instance that is not pointed to by the PodTask, so update our records
					task.Pod = pod
					k.podQueue.Add(podKey, pod)
				} else {
					// this state shouldn't really be possible, so I'm warning if we ever see it
					log.Errorf("Scheduler detected pod no longer pending: %v, will not re-queue; possible offer leak", podKey)
				}
			} else {
				log.Infof("Scheduler detected deleted pod: %v, will not re-queue", podKey)
			}
		}
	}()
}

func (k *KubernetesScheduler) handlePodDeleted(pod *api.Pod) error {
	ctx := api.WithNamespace(api.NewDefaultContext(), pod.Namespace)
	podKey, err := makePodKey(ctx, pod.Name)
	if err != nil {
		return err
	}

	k.Lock()
	defer k.Unlock()

	// prevent the scheduler from attempting to pop this; it's also possible that
	// it's concurrently being scheduled (somewhere between pod scheduling and
	// binding) - if so, then we'll end up removing it from pendingTasks which
	// will abort Bind()ing
	k.podQueue.Delete(podKey)

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
	cache.NewReflector(k.createUnassignedPodLW(), &api.Pod{}, k.podQueue).Run()

	// Watch for deleted pods
	cache.NewReflector(k.createAllPodsLW(), &api.Pod{}, &deleteStore{cache.NewStore(), deleteStoreFn(k.handlePodDeleted)}).Run()

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

// createUnassignedPodLW returns a listWatch that finds all pods that need to be
// scheduled.
func (k *KubernetesScheduler) createUnassignedPodLW() *listWatch {
	return &listWatch{
		client:        k.client,
		fieldSelector: labels.Set{"DesiredState.Host": ""}.AsSelector(),
		resource:      "pods",
	}
}

type deleteStoreFn func(*api.Pod) error

type deleteStore struct {
	cache.Store
	deleteFn deleteStoreFn
}

func (d *deleteStore) Delete(id string) {
	obj, exists := d.Store.Get(id)
	d.Store.Delete(id)
	if exists {
		pod, ok := obj.(*api.Pod)
		if !ok {
			log.Warningf("Expected a pod object, not %v", obj)
			return
		}
		go func() {
			defer util.HandleCrash()
			if err := d.deleteFn(pod); err != nil {
				log.Error(err)
			}
		}()
	}
}

// createAllPodsLW returns a listWatch that finds all pods
func (k *KubernetesScheduler) createAllPodsLW() *listWatch {
	return &listWatch{
		client:        k.client,
		fieldSelector: labels.Everything(),
		resource:      "pods",
	}
}
