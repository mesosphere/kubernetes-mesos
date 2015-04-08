package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/mesosphere/kubernetes-mesos/pkg/executor/service"
	"github.com/mesosphere/kubernetes-mesos/pkg/hyperkube"
	"github.com/mesosphere/kubernetes-mesos/pkg/version/verflag"
	"github.com/spf13/pflag"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	s := service.NewKubeletExecutorServer()
	s.AddStandaloneFlags(pflag.CommandLine)

	util.InitFlags()
	util.InitLogs()
	defer util.FlushLogs()

	verflag.PrintAndExitIfRequested()

	if err := s.Run(hyperkube.Nil(), pflag.CommandLine.Args()); err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		os.Exit(1)
	}
}
