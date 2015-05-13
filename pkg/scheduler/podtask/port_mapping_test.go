package podtask

import (
	"reflect"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	mesos "github.com/mesos/mesos-go/mesosproto"
)

func TestNewPortMapper(t *testing.T) {
	t.Parallel()

	pod := &api.Pod{ObjectMeta: api.ObjectMeta{Labels: map[string]string{}}}
	for label, want := range map[string]PortMapper{
		"fixed":    "fixed",
		"wildcard": "wildcard",
		"foobar":   "wildcard",
		"":         "wildcard",
	} {
		pod.Labels[PortMappingLabelKey] = label
		if got := NewPortMapper(pod); got != want {
			t.Errorf("label=%q got: %q, want: %q", label, got, want)
		}
	}
}

func TestPortMapper_PortMap(t *testing.T) {
	t.Parallel()

	for i, test := range []struct {
		PortMapper
		*T
		*mesos.Offer
		want []PortMapping
		err  error
	}{
		{"fixed", task(), offer(1, 1), []PortMapping{}, nil},
		{"fixed", task(123, 123), offer(1, 1), nil,
			&DuplicateHostPortError{PortMapping{0, 0, 123}, PortMapping{0, 1, 123}}},
		{"wildcard", task(), offer(), []PortMapping{}, nil},
		{"wildcard", task(), offer(1, 1), []PortMapping{}, nil},
		{"wildcard", task(123), offer(1, 1), nil, &PortAllocationError{"foo", []uint64{123}}},
		{"wildcard", task(0, 123), offer(1, 1), nil, &PortAllocationError{"foo", []uint64{123}}},
		{"wildcard", task(0, 1), offer(1, 1), nil, &PortAllocationError{"foo", nil}},
		{"wildcard", task(0, 1), offer(1, 2), []PortMapping{{0, 1, 1}, {0, 0, 2}}, nil},
	} {
		got, err := test.PortMap(test.T, test.Offer)
		if !reflect.DeepEqual(got, test.want) || !reflect.DeepEqual(err, test.err) {
			t.Errorf("\ntest #%d: %q\ngot:  (%+v, %#v)\nwant: (%+v, %#v)",
				i, test.PortMapper, got, err, test.want, test.err)
		}
	}
}

func task(hostports ...int) *T {
	t, _ := fakePodTask("foo")
	t.Pod.Spec = api.PodSpec{
		Containers: []api.Container{{
			Ports: make([]api.ContainerPort, len(hostports)),
		}},
	}
	for i, port := range hostports {
		t.Pod.Spec.Containers[0].Ports[i].HostPort = port
	}
	return t
}

func offer(ports ...uint64) *mesos.Offer {
	return &mesos.Offer{Resources: []*mesos.Resource{rangeResource("ports", ports)}}
}
