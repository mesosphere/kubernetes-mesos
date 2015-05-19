package podtask

import (
	"reflect"
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
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

	task, _ := fakePodTask("foo")
	ports := func(hostports ...int) *T {
		task.Pod.Spec = api.PodSpec{
			Containers: []api.Container{{
				Ports: make([]api.ContainerPort, len(hostports)),
			}},
		}
		for i, port := range hostports {
			task.Pod.Spec.Containers[0].Ports[i].HostPort = port
		}
		return task
	}

	for i, test := range []struct {
		PortMapper
		*T
		Ranges
		want []PortMapping
		err  error
	}{
		{"fixed", ports(), Ranges{{1, 1}}, []PortMapping{}, nil},
		{"fixed", ports(123, 123), Ranges{{1, 1}}, nil, &DuplicateHostPortError{
			PortMapping{&task.Pod, &task.Pod.Spec.Containers[0], 0, 123},
			PortMapping{&task.Pod, &task.Pod.Spec.Containers[0], 1, 123},
		}},
		{"wildcard", ports(), Ranges{}, []PortMapping{}, nil},
		{"wildcard", ports(), Ranges{{1, 1}}, []PortMapping{}, nil},
		{"wildcard", ports(123), Ranges{{1, 1}}, nil, &PortAllocationError{&task.Pod, []uint64{123}}},
		{"wildcard", ports(0, 123), Ranges{{1, 1}}, nil, &PortAllocationError{&task.Pod, []uint64{123}}},
		{"wildcard", ports(0, 1), Ranges{{1, 1}}, nil, &PortAllocationError{&task.Pod, nil}},
		{"wildcard", ports(0, 1), Ranges{{1, 2}}, []PortMapping{
			{&task.Pod, &task.Pod.Spec.Containers[0], 1, 1},
			{&task.Pod, &task.Pod.Spec.Containers[0], 0, 2},
		}, nil},
	} {
		got, err := test.PortMap(test.T, test.Ranges)
		if !reflect.DeepEqual(got, test.want) || !reflect.DeepEqual(err, test.err) {
			t.Errorf("\ntest #%d: %q\ngot:  (%+v, %#v)\nwant: (%+v, %#v)",
				i, test.PortMapper, got, err, test.want, test.err)
		}
	}
}
