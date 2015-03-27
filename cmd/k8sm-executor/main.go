package main

import (
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/mesosphere/kubernetes-mesos/pkg/executor/service"
	"github.com/mesosphere/kubernetes-mesos/pkg/version/verflag"
	"github.com/spf13/pflag"
)

func main() {
	s := service.NewKubeletExecutorServer()
	s.AddStandaloneFlags(pflag.CommandLine)

	util.InitFlags()
	util.InitLogs()
	defer util.FlushLogs()

	verflag.PrintAndExitIfRequested()

	s.Run(nil, pflag.CommandLine.Args())
}
