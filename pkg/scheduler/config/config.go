package config

import (
	"io"
	"time"

	"code.google.com/p/gcfg"
)

const (
	DefaultInfoName                           = "Kubernetes"	   // intended to be mesos FrameworkInfo.Name
	DefaultOfferTTL                           = 5 * time.Second    // duration an offer is viable, prior to being expired
	DefaultOfferLingerTTL                     = 120  * time.Second // duration an expired offer lingers in history
	DefaultListenerDelay                      = 1 * time.Second    // duration between offer listener notifications
	DefaultUpdatesBacklog                     = 2048               // size of the pod updates channel
	DefaultFrameworkIdRefreshInterval         = 30 * time.Second   // interval we update the frameworkId stored in etcd
	DefaultInitialImplicitReconciliationDelay = 15 * time.Second   // wait this amount of time after initial registration before attempting implicit reconciliation
	DefaultExplicitReconciliationMaxBackoff   = 2 * time.Minute    // interval in between internal task status checks/updates
	DefaultExplicitReconciliationAbortTimeout = 30 * time.Second   // waiting period after attempting to cancel an ongoing reconciliation
	DefaultInitialPodBackoff                  = 1 * time.Second
	DefaultMaxPodBackoff                      = 60 * time.Second
	DefaultHttpHandlerTimeout                 = 10 * time.Second
)

// Example scheduler configuration file:
//
// [scheduler]
//  info-name        = Kubernetes
//	offer-ttl        = 5s
//  offer-linger-ttl = 2m

type ConfigWrapper struct {
	Scheduler Config
}

type Config struct {
	InfoName                           string          `gcfg:"info-name"`
	OfferTTL                           WrappedDuration `gcfg:"offer-ttl"`
	OfferLingerTTL                     WrappedDuration `gcfg:"offer-linger-ttl"`
	ListenerDelay                      WrappedDuration `gcfg:"listener-delay"`
	UpdatesBacklog                     int             `gcfg:"updates-backlog"`
	FrameworkIdRefreshInterval         WrappedDuration `gcfg:"listener-delay"`
	InitialImplicitReconciliationDelay WrappedDuration `gcfg:"initial-implicit-reconciliation-delay"`
	ExplicitReconciliationMaxBackoff   WrappedDuration `gcfg:"explicit-reconciliantion-max-backoff"`
	ExplicitReconciliationAbortTimeout WrappedDuration `gcfg:"explicit-reconciliantion-abort-timeout"`
	InitialPodBackoff                  WrappedDuration `gcfg:"initial-pod-backoff"`
	MaxPodBackoff                      WrappedDuration `gcfg:"max-pod-backoff"`
	HttpHandlerTimeout                 WrappedDuration `gcfg:"http-handler-timeout"`
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

func (c *Config) SetDefaults() {
	c.InfoName = DefaultInfoName
	c.OfferTTL = WrappedDuration{Duration: DefaultOfferTTL}
	c.OfferLingerTTL = WrappedDuration{Duration: DefaultOfferLingerTTL}
	c.ListenerDelay = WrappedDuration{Duration: DefaultListenerDelay}
	c.UpdatesBacklog = DefaultUpdatesBacklog
	c.FrameworkIdRefreshInterval = WrappedDuration{Duration: DefaultFrameworkIdRefreshInterval}
	c.InitialImplicitReconciliationDelay = WrappedDuration{Duration: DefaultInitialImplicitReconciliationDelay}
	c.ExplicitReconciliationMaxBackoff = WrappedDuration{Duration: DefaultExplicitReconciliationMaxBackoff}
	c.ExplicitReconciliationAbortTimeout = WrappedDuration{Duration: DefaultExplicitReconciliationAbortTimeout}
	c.InitialPodBackoff = WrappedDuration{Duration: DefaultInitialPodBackoff}
	c.MaxPodBackoff = WrappedDuration{Duration: DefaultMaxPodBackoff}
	c.HttpHandlerTimeout = WrappedDuration{Duration: DefaultHttpHandlerTimeout}
}

func CreateDefaultConfig() *Config {
	c := &Config{}
	c.SetDefaults()
	return c
}

func (c *Config) read(configReader io.Reader) error {
	wrapper := &ConfigWrapper{Scheduler: *c}
	if configReader != nil {
		if err := gcfg.ReadInto(wrapper, configReader); err != nil {
			return err
		}
	}
	return nil
}
