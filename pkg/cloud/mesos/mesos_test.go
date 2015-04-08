package mesos

import (
	"bytes"
	"testing"
	"time"

	log "github.com/golang/glog"
)

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
