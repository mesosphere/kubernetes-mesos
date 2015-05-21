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

	const pod = "foo"

	for i, test := range []struct {
		PortMapper
		api.Container
		Ranges
		want []PortMapping
		err  error
	}{
		{"fixed", container(), Ranges{}, []PortMapping(nil), nil},
		{"fixed", container(1, 2), Ranges{}, nil, &PortAllocationError{pod, []uint64{2, 1}}},
		{"fixed", container(0, 124, 123), Ranges{{100, 150}}, []PortMapping{
			{pod, 0, 1, 124}, {pod, 0, 2, 123}}, nil},
		{"fixed", container(123, 123), Ranges{{100, 150}}, nil,
			&DuplicateHostPortError{PortMapping{pod, 0, 1, 123}, PortMapping{pod, 0, 0, 123}}},
		{"wildcard", container(123, 123), Ranges{{100, 150}}, nil,
			&DuplicateHostPortError{PortMapping{pod, 0, 1, 123}, PortMapping{pod, 0, 0, 123}}},
		{"wildcard", container(), Ranges{}, []PortMapping(nil), nil},
		{"wildcard", container(123), Ranges{{1, 1}}, nil, &PortAllocationError{pod, []uint64{123}}},
		{"wildcard", container(0, 0, 1), Ranges{{1, 10}}, []PortMapping{{pod, 0, 2, 1}, {pod, 0, 0, 2}, {pod, 0, 1, 3}}, nil},
	} {
		got, err := test.PortMap(test.Ranges, pod, test.Container)
		if !reflect.DeepEqual(got, test.want) || !reflect.DeepEqual(err, test.err) {
			t.Errorf("\ntest #%d: %q\ngot:  (%+v, %#v)\nwant: (%+v, %#v)",
				i, test.PortMapper, got, err, test.want, test.err)
		}
	}
}

// container returns a api.Container with the given hostports. used for testing only.
func container(hostports ...int) api.Container {
	container := api.Container{Ports: make([]api.ContainerPort, len(hostports))}
	for i, port := range hostports {
		container.Ports[i].HostPort = port
	}
	return container
}
