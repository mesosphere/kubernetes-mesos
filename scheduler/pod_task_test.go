package scheduler

import (
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/mesos/mesos-go/mesos"
)

const (
	t_min_cpu	= 128
	t_min_mem	= 128
)

func TestEmptyOffer(t *testing.T) {
	t.Parallel()
	task, err := newPodTask(&api.Pod{}, &mesos.ExecutorInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if ok := task.AcceptOffer(nil); ok {
		t.Fatalf("accepted nil offer")
	}
	if ok := task.AcceptOffer(&mesos.Offer{}); ok {
		t.Fatalf("accepted empty offer")
	}
}

func TestNoPortsInPodOrOffer(t *testing.T) {
	t.Parallel()
	task, _ := newPodTask(&api.Pod{}, &mesos.ExecutorInfo{})

	offer := &mesos.Offer{
		Resources: []*mesos.Resource{
			mesos.ScalarResource("cpus", 0.001),
			mesos.ScalarResource("mem", 0.001),
		},
	}
	if ok := task.AcceptOffer(offer); ok {
		t.Fatalf("accepted offer %v:", offer)
	}

	offer = &mesos.Offer{
		Resources: []*mesos.Resource{
			mesos.ScalarResource("cpus", t_min_cpu),
			mesos.ScalarResource("mem", t_min_mem),
		},
	}
	if ok := task.AcceptOffer(offer); !ok {
		t.Fatalf("did not accepted offer %v:", offer)
	}
}

func TestNoMatchingPorts(t *testing.T) {
	t.Parallel()
	pod := &api.Pod{}
	task, _ := newPodTask(pod, &mesos.ExecutorInfo{})

	offer := &mesos.Offer{
		Resources: []*mesos.Resource{
			mesos.ScalarResource("cpus", t_min_cpu),
			mesos.ScalarResource("mem", t_min_mem),
			rangeResource("ports", []uint64{1,1}),
		},
	}
	if ok := task.AcceptOffer(offer); !ok {
		t.Fatalf("did not accepted offer %v:", offer)
	}

	pod.DesiredState = api.PodState{
		Manifest: api.ContainerManifest{
			Containers: []api.Container{{
				Ports: []api.Port{{
					HostPort: 123,
				}},
			}},
		},
	}
	if ok := task.AcceptOffer(offer); ok {
		t.Fatalf("accepted offer %v:", offer)
	}

	pod.DesiredState.Manifest.Containers[0].Ports[0].HostPort = 1
	if ok := task.AcceptOffer(offer); !ok {
		t.Fatalf("did not accepted offer %v:", offer)
	}

	pod.DesiredState.Manifest.Containers[0].Ports[0].HostPort = 0
	if ok := task.AcceptOffer(offer); !ok {
		t.Fatalf("did not accepted offer %v:", offer)
	}

	offer.Resources = []*mesos.Resource{
		mesos.ScalarResource("cpus", t_min_cpu),
		mesos.ScalarResource("mem", t_min_mem),
	}
	if ok := task.AcceptOffer(offer); !ok {
		t.Fatalf("did not accepted offer %v:", offer)
	}

	pod.DesiredState.Manifest.Containers[0].Ports[0].HostPort = 1
	if ok := task.AcceptOffer(offer); ok {
		t.Fatalf("accepted offer %v:", offer)
	}
}
