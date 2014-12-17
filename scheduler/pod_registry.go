package scheduler

/*

import (
	"fmt"
	"strings"

	"code.google.com/p/goprotobuf/proto"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/mesos"
)

// Create a pod based on a specification; DOES NOT schedule it onto a specific machine,
// instead the pod is queued for scheduling.
func (k *KubernetesScheduler) CreatePod(ctx api.Context, pod *api.Pod) error {
	log.V(2).Infof("Create pod: '%v'\n", pod)
	pod.Status.Phase = api.PodPending
	pod.Status.Host = ""

	// TODO(jdef) should we make a copy of the pod object instead of just assuming that the caller is
	// well behaved and will not change the state of the object it has given to us?
	task, err := newPodTask(ctx, pod, k.executor)
	if err != nil {
		return err
	}

	k.Lock()
	defer k.Unlock()

	if _, ok := k.podToTask[task.podKey]; ok {
		return fmt.Errorf("Pod %s already launched. Please choose a unique pod name", task.podKey)
	}

	k.podQueue.Add(task.podKey, pod)
	k.podToTask[task.podKey] = task.ID
	k.pendingTasks[task.ID] = task

	return nil
}

// Get a specific pod. It's *very* important to return a clone of the Pod that
// we've saved because our caller will likely modify it.
func (k *KubernetesScheduler) GetPod(ctx api.Context, id string) (*api.Pod, error) {
	log.V(2).Infof("Get pod '%s'\n", id)

	podKey, err := makePodKey(ctx, id)
	if err != nil {
		return nil, err
	}

	k.RLock()
	defer k.RUnlock()

	taskId, exists := k.podToTask[podKey]
	if !exists {
		return nil, fmt.Errorf("Could not resolve pod '%s' to task id", podKey)
	}

	switch task, state := k.getTask(taskId); state {
	case statePending:
		log.V(5).Infof("Pending Pod '%s': %v", podKey, task.Pod)
		podCopy := *task.Pod
		return &podCopy, nil
	case stateRunning:
		log.V(5).Infof("Running Pod '%s': %v", podKey, task.Pod)
		podCopy := *task.Pod
		return &podCopy, nil
	case stateFinished:
		return nil, fmt.Errorf("Pod '%s' is finished", podKey)
	case stateUnknown:
		return nil, fmt.Errorf("Unknown Pod %v", podKey)
	default:
		return nil, fmt.Errorf("Unexpected task state %v for task %v", state, taskId)
	}
}

// ListPods obtains a list of pods that match selector.
func (k *KubernetesScheduler) ListPodsPredicate(ctx api.Context, filter func(*api.Pod) bool) (*api.PodList, error) {
	k.RLock()
	defer k.RUnlock()
	return k.listPods(ctx, filter)
}

// ListPods obtains a list of pods that match selector.
func (k *KubernetesScheduler) ListPods(ctx api.Context, selector labels.Selector) (*api.PodList, error) {
	log.V(2).Infof("List pods for '%v'\n", selector)
	k.RLock()
	defer k.RUnlock()
	return k.listPods(ctx, func(pod *api.Pod) bool {
		return selector.Matches(labels.Set(pod.Labels))
	})
}

// assumes that caller has already locked around scheduler state
func (k *KubernetesScheduler) listPods(ctx api.Context, filter func(*api.Pod) bool) (*api.PodList, error) {
	prefix := makePodListKey(ctx) + "/"
	result := []api.Pod{}
	for _, task := range k.runningTasks {
		if !strings.HasPrefix(task.podKey, prefix) {
			continue
		}
		pod := task.Pod
		if filter(pod) {
			result = append(result, *pod)
		}
	}
	for _, task := range k.pendingTasks {
		if !strings.HasPrefix(task.podKey, prefix) {
			continue
		}
		pod := task.Pod
		if filter(pod) {
			result = append(result, *pod)
		}
	}

	// TODO(nnielsen): Wire up check in finished tasks. (jdef) not sure how many
	// finished tasks are really appropriate to return here. finished tasks do not
	// have a TTL in the finishedTasks ring and I don't think we want to return
	// hundreds of finished tasks here.

	matches := &api.PodList{Items: result}
	log.V(5).Infof("Returning pods: '%v'\n", matches)

	return matches, nil
}

// Update an existing pod.
func (k *KubernetesScheduler) UpdatePod(ctx api.Context, pod *api.Pod) error {
	// TODO(yifan): Need to send a special message to the slave/executor.
	// TODO(nnielsen): Pod updates not yet supported by kubelet.
	return fmt.Errorf("Not implemented: UpdatePod")
}

// Delete an existing pod.
func (k *KubernetesScheduler) DeletePod(ctx api.Context, id string) error {
	log.V(2).Infof("Delete pod '%s'\n", id)

	podKey, err := makePodKey(ctx, id)
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

func (k *KubernetesScheduler) WatchPods(ctx api.Context, resourceVersion string, filter func(*api.Pod) bool) (watch.Interface, error) {
	return nil, nil
}

*/
