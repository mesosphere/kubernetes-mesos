package mesos

import (
	"net"
	"reflect"
	"testing"
)

func TestIPAddress(t *testing.T) {
	c := &MesosCloud{}
	expected4 := net.IPv4(127, 0, 0, 1)
	ip, err := c.ipAddress("127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(ip, expected4) {
		t.Fatalf("expected %#v instead of %#v", expected4, ip)
	}

	expected6 := net.ParseIP("::1")
	if expected6 == nil {
		t.Fatalf("failed to parse ipv6 ::1")
	}
	ip, err = c.ipAddress("::1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(ip, expected6) {
		t.Fatalf("expected %#v instead of %#v", expected6, ip)
	}

	ip, err = c.ipAddress("localhost")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(ip, expected4) && !reflect.DeepEqual(ip, expected6) {
		t.Fatalf("expected %#v or %#v instead of %#v", expected4, expected6, ip)
	}

	_, err = c.ipAddress("")
	if err != noHostNameSpecified {
		t.Fatalf("expected error noHostNameSpecified but got none")
	}
}
