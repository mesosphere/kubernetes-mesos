package main

//TODO(jdef) how to eventually support mesos running with other k8s cloud providers?
import (
	_ "github.com/mesosphere/kubernetes-mesos/pkg/cloud/mesos"
)
