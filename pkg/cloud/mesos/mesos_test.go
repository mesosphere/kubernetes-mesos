package mesos

import (
	"bytes"
	"net"
	"reflect"
	"testing"
	"time"

	log "github.com/golang/glog"
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

// test mesos.newMesosCloud with no config
func Test_newMesosCloud_NoConfig(t *testing.T) {
	defer log.Flush()

	resetConfigToDefault()

	mesosCloud, err := newMesosCloud(nil)

	if err != nil {
		t.Fatalf("Creating a new Mesos cloud provider without config does not yield an error: %#v", err)
	}

	if mesosCloud.client.httpClient.Timeout != time.Duration(10)*time.Second {
		t.Fatalf("Creating a new Mesos cloud provider without config does not yield an error: %#v", err)
	}

	if mesosCloud.client.state.ttl != time.Duration(5)*time.Second {
		t.Fatalf("Mesos client with default config has the expected state cache TTL value")
	}
}

// test mesos.newMesosCloud with custom config
func Test_newMesosCloud_WithConfig(t *testing.T) {
	defer log.Flush()

	resetConfigToDefault()

	configString := `
[mesos-cloud]
	http-client-timeout = 500ms
	state-cache-ttl = 1h`

	reader := bytes.NewBufferString(configString)

	mesosCloud, err := newMesosCloud(reader)

	if err != nil {
		t.Fatalf("Creating a new Mesos cloud provider with a custom config does not yield an error: %#v", err)
	}

	if mesosCloud.client.httpClient.Timeout != time.Duration(500)*time.Millisecond {
		t.Fatalf("Mesos client with a custom config has the expected HTTP client timeout value")
	}

	if mesosCloud.client.state.ttl != time.Duration(1)*time.Hour {
		t.Fatalf("Mesos client with a custom config has the expected state cache TTL value")
	}
}
