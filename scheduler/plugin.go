package scheduler

import (
	"fmt"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	algorithm "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	plugin "github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/scheduler"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
	"gopkg.in/v1/yaml"
)

// implements binding.Registry, launches the pod-associated-task in mesos
func (k *KubernetesScheduler) Bind(binding *api.Binding) error {

	// HACK(jdef): infer context from binding namespace. i wonder if this will create
	// problems down the line. the pod.Registry interface accepts Context so it's a
	// little strange that the scheduler interface does not
	ctx := api.NewDefaultContext()
	if len(binding.Namespace) != 0 {
		ctx = api.WithNamespace(ctx, binding.Namespace)
	}
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

	if err = k.prepareTaskForLaunch(binding.Host, task); err == nil {
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

func (k *KubernetesScheduler) prepareTaskForLaunch(machine string, task *PodTask) error {
	boundPod, err := k.boundPodFactory.MakeBoundPod(machine, task.Pod)
	if err != nil {
		log.V(2).Infof("Failed to generate an updated boundPod")
		return err
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

// Schedule implements the Scheduler interface of the Kubernetes.
// It returns the selectedMachine's name and error (if there's any).
func (k *KubernetesScheduler) Schedule(pod api.Pod, unused algorithm.MinionLister) (string, error) {
	log.Infof("Try to schedule pod %v\n", pod.Name)

	// HACK(jdef): infer context from pod namespace. i wonder if this will create
	// problems down the line. the pod.Registry interface accepts Context so it's a
	// little strange that the scheduler interface does not. see Bind()
	ctx := api.NewDefaultContext()
	if len(pod.Namespace) != 0 {
		ctx = api.WithNamespace(ctx, pod.Namespace)
	}
	// default upstream scheduler passes pod.Name as binding.PodID
	podKey, err := makePodKey(ctx, pod.Name)
	if err != nil {
		return "", err
	}

	k.Lock()
	defer k.Unlock()

	if taskID, ok := k.podToTask[podKey]; !ok {
		return "", fmt.Errorf("Pod %s cannot be resolved to a task", podKey)
	} else {
		if task, found := k.pendingTasks[taskID]; !found {
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

	// HACK(jdef): refresh the pod data via the client, updates things like selflink that
	// the upstream scheduling controller expects to have. Will not need this once we divorce
	// scheduling from the apiserver (soon I hope)
	pod, err := k.client.Pods(pod.Namespace).Get(pod.Name)
	if err != nil {
		log.Warningf("Failed to refresh pod %v, attempting to continue: %v", pod.Name, err)
	}

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
		// HACK(jdef): infer context from pod namespace. i wonder if this will create
		// problems down the line. the pod.Registry interface accepts Context so it's a
		// little strange that the scheduler interface does not. see Bind()
		ctx := api.NewDefaultContext()
		if len(pod.Namespace) != 0 {
			ctx = api.WithNamespace(ctx, pod.Namespace)
		}
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
			}
			delete(k.podToTask, podKey)
			return
		}
		if pod.Status.Host == "" {
			// ensure that the pod hasn't been deleted while we were trying to schedule it
			k.Lock()
			defer k.Unlock()

			if taskId, exists := k.podToTask[podKey]; exists {
				if task, ok := k.pendingTasks[taskId]; ok && !task.hasAcceptedOffer() {
					// "pod" now refers to a Pod instance that is not pointed to by the PodTask, so update our records
					// TODO(jdef) not sure that this is strictly necessary since once the pod is schedule, only the ID is
					// passed around in the Pod.Registry API
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

// Create creates a scheduler and all support functions.
func (k *KubernetesScheduler) NewPluginConfig() *plugin.Config {

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
