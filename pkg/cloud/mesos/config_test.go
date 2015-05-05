package mesos

import (
	"bytes"
	"testing"
	"time"

	log "github.com/golang/glog"
)

// test mesos.createDefaultConfig
func Test_createDefaultConfig(t *testing.T) {
	defer log.Flush()

	config := createDefaultConfig()

	if config.MesosMaster != DefaultMesosMaster {
		t.Fatalf("Default config has the expected MesosMaster value")
	}

	if config.MesosHttpClientTimeout.Duration != DefaultHttpClientTimeout {
		t.Fatalf("Default config has the expected MesosHttpClientTimeout value")
	}

	if config.StateCacheTTL.Duration != DefaultStateCacheTTL {
		t.Fatalf("Default config has the expected StateCacheTTL value")
	}
}

// test mesos.readConfig
func Test_readConfig(t *testing.T) {
	defer log.Flush()

	configString := `
[mesos-cloud]
	mesos-master        = leader.mesos:5050
	http-client-timeout = 500ms
	state-cache-ttl     = 1h`

	reader := bytes.NewBufferString(configString)

	config, err := readConfig(reader)

	if err != nil {
		t.Fatalf("Reading configuration does not yield an error: %#v", err)
	}

	if config.MesosMaster != "leader.mesos:5050" {
		t.Fatalf("Parsed config has the expected MesosMaster value")
	}

	if config.MesosHttpClientTimeout.Duration != time.Duration(500)*time.Millisecond {
		t.Fatalf("Parsed config has the expected MesosHttpClientTimeout value")
	}

	if config.StateCacheTTL.Duration != time.Duration(1)*time.Hour {
		t.Fatalf("Parsed config has the expected StateCacheTTL value")
	}
}
