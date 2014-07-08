package scheduler

import (
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	kubernetes "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/mesosphere/kubernetes-mesos/3rdparty/github.com/mesosphere/mesos-go/mesos"
)

// A Kubernete Scheduler that runs on top of Mesos.
type KubernetesScheduler struct {
}

// New create a new KubernetesScheduler.
func New() *KubernetesScheduler {
	return &KubernetesScheduler{}
}

// Mesos scheduler interfaces.
func (k *KubernetesScheduler) Registered(mesos.SchedulerDriver, *mesos.FrameworkID, *mesos.MasterInfo) {
}
func (k *KubernetesScheduler) Reregistered(mesos.SchedulerDriver, *mesos.MasterInfo) {}
func (k *KubernetesScheduler) Disconnected(mesos.SchedulerDriver)                    {}
func (k *KubernetesScheduler) ResourceOffers(mesos.SchedulerDriver, []*mesos.Offer)  {}
func (k *KubernetesScheduler) OfferRescinded(mesos.SchedulerDriver, *mesos.OfferID)  {}
func (k *KubernetesScheduler) StatusUpdate(mesos.SchedulerDriver, *mesos.TaskStatus) {}
func (k *KubernetesScheduler) FrameworkMessage(mesos.SchedulerDriver, *mesos.ExecutorID, *mesos.SlaveID, string) {
}
func (k *KubernetesScheduler) SlaveLost(mesos.SchedulerDriver, *mesos.SlaveID) {}
func (k *KubernetesScheduler) ExecutorLost(mesos.SchedulerDriver, *mesos.ExecutorID, *mesos.SlaveID, int) {
}
func (k *KubernetesScheduler) Error(mesos.SchedulerDriver, string) {}

// Schedule implements the Scheduler interface of the Kubernetes.
func (k *KubernetesScheduler) Schedule(api.Pod, kubernetes.MinionLister) (selectedMaching string, err error) {
	return "", nil
}
