package scheduler

import (
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/mesos/mesos-go/mesos"
)

const (
	t_min_cpu = 128
	t_min_mem = 128
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

func TestDefaultHostPortMatching(t *testing.T) {
	t.Parallel()
	pod := &api.Pod{}
	task, _ := newPodTask(pod, &mesos.ExecutorInfo{})

	offer := &mesos.Offer{
		Resources: []*mesos.Resource{
			rangeResource("ports", []uint64{1, 1}),
		},
	}
	mapping, err := defaultHostPortMapping(task, offer)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) > 0 {
		t.Fatalf("Found mappings for a pod without ports: %v", pod)
	}

	//--
	pod.DesiredState = api.PodState{
		Manifest: api.ContainerManifest{
			Containers: []api.Container{{
				Ports: []api.Port{{
					HostPort: 123,
				}, {
					HostPort: 123,
				}},
			}},
		},
	}
	task, _ = newPodTask(pod, &mesos.ExecutorInfo{})
	_, err = defaultHostPortMapping(task, offer)
	if err, _ := err.(*DuplicateHostPortError); err == nil {
		t.Fatal("Expected duplicate port error")
	} else if err.m1.offerPort != 123 {
		t.Fatal("Expected duplicate host port 123")
	}
}

func TestAcceptOfferPorts(t *testing.T) {
	t.Parallel()
	pod := &api.Pod{}
	task, _ := newPodTask(pod, &mesos.ExecutorInfo{})

	offer := &mesos.Offer{
		Resources: []*mesos.Resource{
			mesos.ScalarResource("cpus", t_min_cpu),
			mesos.ScalarResource("mem", t_min_mem),
			rangeResource("ports", []uint64{1, 1}),
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

func TestWildcardHostPortMatching(t *testing.T) {
	t.Parallel()
	pod := &api.Pod{}
	task, _ := newPodTask(pod, &mesos.ExecutorInfo{})

	offer := &mesos.Offer{}
	mapping, err := wildcardHostPortMapping(task, offer)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) > 0 {
		t.Fatalf("Found mappings for an empty offer and a pod without ports: %v", pod)
	}

	//--
	offer = &mesos.Offer{
		Resources: []*mesos.Resource{
			rangeResource("ports", []uint64{1, 1}),
		},
	}
	mapping, err = wildcardHostPortMapping(task, offer)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) > 0 {
		t.Fatalf("Found mappings for a pod without ports: %v", pod)
	}

	//--
	pod.DesiredState = api.PodState{
		Manifest: api.ContainerManifest{
			Containers: []api.Container{{
				Ports: []api.Port{{
					HostPort: 123,
				}},
			}},
		},
	}
	task, _ = newPodTask(pod, &mesos.ExecutorInfo{})
	mapping, err = wildcardHostPortMapping(task, offer)
	if err, _ := err.(*PortAllocationError); err == nil {
		t.Fatal("Expected port allocation error")
	} else if !(len(err.Ports) == 1 && err.Ports[0] == 123) {
		t.Fatal("Expected port allocation error for host port 123")
	}

	//--
	pod.DesiredState = api.PodState{
		Manifest: api.ContainerManifest{
			Containers: []api.Container{{
				Ports: []api.Port{{
					HostPort: 0,
				}, {
					HostPort: 123,
				}},
			}},
		},
	}
	task, _ = newPodTask(pod, &mesos.ExecutorInfo{})
	mapping, err = wildcardHostPortMapping(task, offer)
	if err, _ := err.(*PortAllocationError); err == nil {
		t.Fatal("Expected port allocation error")
	} else if !(len(err.Ports) == 1 && err.Ports[0] == 123) {
		t.Fatal("Expected port allocation error for host port 123")
	}

	//--
	pod.DesiredState = api.PodState{
		Manifest: api.ContainerManifest{
			Containers: []api.Container{{
				Ports: []api.Port{{
					HostPort: 0,
				}, {
					HostPort: 1,
				}},
			}},
		},
	}
	task, _ = newPodTask(pod, &mesos.ExecutorInfo{})
	mapping, err = wildcardHostPortMapping(task, offer)
	if err, _ := err.(*PortAllocationError); err == nil {
		t.Fatal("Expected port allocation error")
	} else if len(err.Ports) != 0 {
		t.Fatal("Expected port allocation error for wildcard port")
	}

	//--
	offer = &mesos.Offer{
		Resources: []*mesos.Resource{
			rangeResource("ports", []uint64{1, 2}),
		},
	}
	mapping, err = wildcardHostPortMapping(task, offer)
	if err != nil {
		t.Fatal(err)
	} else if len(mapping) != 2 {
		t.Fatal("Expected both ports allocated")
	}
	valid := 0
	for _, entry := range mapping {
		if entry.cindex == 0 && entry.pindex == 0 && entry.offerPort == 2 {
			valid++
		}
		if entry.cindex == 0 && entry.pindex == 1 && entry.offerPort == 1 {
			valid++
		}
	}
	if valid < 2 {
		t.Fatalf("Expected 2 valid port mappings, not %d", valid)
	}
}
