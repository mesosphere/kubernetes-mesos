package config

import (
	"time"
)

// default values to use when constructing mesos ExecutorInfo messages
const (
	DefaultInfoID         = "k8sm-executor"
	DefaultInfoSource     = "kubernetes"
	DefaultInfoName       = "Kubelet-Executor"
	DefaultSuicideTimeout = 20 * time.Minute
)
