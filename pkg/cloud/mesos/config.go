package mesos

import (
	"io"
	"time"

	"code.google.com/p/gcfg"
)

const (
	DefaultMesosMaster       = "localhost:5050"
	DefaultHttpClientTimeout = time.Duration(10) * time.Second
	DefaultStateCacheTTL     = time.Duration(5) * time.Second
)

// Example Mesos cloud provider configuration file:
//
// [mesos-cloud]
//  mesos-master        = leader.mesos:5050
//	http-client-timeout = 500ms
//	state-cache-ttl     = 1h

type ConfigWrapper struct {
	Mesos_Cloud Config
}

type Config struct {
	MesosMaster            string   `gcfg:"mesos-master"`
	MesosHttpClientTimeout Duration `gcfg:"http-client-timeout"`
	StateCacheTTL          Duration `gcfg:"state-cache-ttl"`
}

type Duration struct {
	Duration time.Duration `gcfg:"duration"`
}

func (d *Duration) UnmarshalText(data []byte) error {
	underlying, err := time.ParseDuration(string(data))
	if err == nil {
		d.Duration = underlying
	}
	return err
}

func createDefaultConfig() *Config {
	return &Config{
		MesosMaster:            DefaultMesosMaster,
		MesosHttpClientTimeout: Duration{Duration: DefaultHttpClientTimeout},
		StateCacheTTL:          Duration{Duration: DefaultStateCacheTTL},
	}
}

func readConfig(configReader io.Reader) (*Config, error) {
	config := createDefaultConfig()
	wrapper := &ConfigWrapper{Mesos_Cloud: *config}
	if configReader != nil {
		if err := gcfg.ReadInto(wrapper, configReader); err != nil {
			return nil, err
		}
		config = &(wrapper.Mesos_Cloud)
	}
	return config, nil
}
