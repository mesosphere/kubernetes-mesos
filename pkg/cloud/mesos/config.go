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

var (
	config *Config
)

func getConfig() *Config {
	return config
}

func resetConfigToDefault() {
	config = createDefaultConfig()
}

func init() {
	resetConfigToDefault()
}

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
	MesosMaster            string          `gcfg:"mesos-master"`
	MesosHttpClientTimeout WrappedDuration `gcfg:"http-client-timeout"`
	StateCacheTTL          WrappedDuration `gcfg:"state-cache-ttl"`
}

type WrappedDuration struct {
	Duration time.Duration `gcfg:"duration"`
}

func (wd *WrappedDuration) UnmarshalText(data []byte) error {
	d, err := time.ParseDuration(string(data))
	if err == nil {
		wd.Duration = d
	}
	return err
}

func createDefaultConfig() *Config {
	return &Config{
		MesosMaster:            DefaultMesosMaster,
		MesosHttpClientTimeout: WrappedDuration{Duration: DefaultHttpClientTimeout},
		StateCacheTTL:          WrappedDuration{Duration: DefaultStateCacheTTL},
	}
}

func readConfig(configReader io.Reader) error {
	resetConfigToDefault()
	wrapper := &ConfigWrapper{Mesos_Cloud: *config}
	if configReader != nil {
		if err := gcfg.ReadInto(wrapper, configReader); err != nil {
			return err
		}
		config = &(wrapper.Mesos_Cloud)
	}
	return nil
}
