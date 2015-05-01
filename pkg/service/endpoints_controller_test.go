package service

import (
	"testing"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
)

func TestFindMappedPort(t *testing.T) {
	pod := &api.Pod{}
	port, err := findMappedPort(pod, 80)
	if err == nil {
		t.Fatalf("expected error since port 80 is not mapped")
	}
	port, err = findMappedPortName(pod, "foo")
	if err == nil {
		t.Fatalf("expected error since port 'foo' is not mapped")
	}

	pod.Annotations = make(map[string]string)
	pod.Annotations["k8s.mesosphere.io/port_80"] = "123"
	pod.Annotations["k8s.mesosphere.io/portName_foo"] = "456"

	port, err = findMappedPort(pod, 80)
	if err != nil {
		t.Fatalf("expected that port 80 is mapped")
	}
	if port != 123 {
		t.Fatalf("expected mapped port == 123")
	}

	port, err = findMappedPortName(pod, "foo")
	if err != nil {
		t.Fatalf("expected that port 80 is mapped")
	}
	if port != 456 {
		t.Fatalf("expected mapped port == 456")
	}
}
