/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl/cmd/util"
	libutil "github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/spf13/cobra"
)

const (
	log_example = `// Returns snapshot of ruby-container logs from pod 123456-7890.
$ kubectl log 123456-7890 ruby-container

// Starts streaming of ruby-container logs from pod 123456-7890.
$ kubectl log -f 123456-7890 ruby-container`
)

func selectContainer(pod *api.Pod, in io.Reader, out io.Writer) string {
	fmt.Fprintf(out, "Please select a container:\n")
	options := libutil.StringSet{}
	for ix := range pod.Spec.Containers {
		fmt.Fprintf(out, "[%d] %s\n", ix+1, pod.Spec.Containers[ix].Name)
		options.Insert(pod.Spec.Containers[ix].Name)
	}
	for {
		var input string
		fmt.Fprintf(out, "> ")
		fmt.Fscanln(in, &input)
		if options.Has(input) {
			return input
		}
		ix, err := strconv.Atoi(input)
		if err == nil && ix > 0 && ix <= len(pod.Spec.Containers) {
			return pod.Spec.Containers[ix-1].Name
		}
		fmt.Fprintf(out, "Invalid input: %s", input)
	}
}

func (f *Factory) NewCmdLog(out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "log [-f] POD [CONTAINER]",
		Short:   "Print the logs for a container in a pod.",
		Long:    "Print the logs for a container in a pod. If the pod has only one container, the container name is optional.",
		Example: log_example,
		Run: func(cmd *cobra.Command, args []string) {
			err := RunLog(f, out, cmd, args)
			util.CheckErr(err)
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "Specify if the logs should be streamed.")
	cmd.Flags().Bool("interactive", true, "If true, prompt the user for input when required. Default true.")
	return cmd
}

func RunLog(f *Factory, out io.Writer, cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return util.UsageError(cmd, "POD is required for log")
	}

	if len(args) > 2 {
		return util.UsageError(cmd, "log POD [CONTAINER]")
	}

	namespace, err := f.DefaultNamespace()
	if err != nil {
		return err
	}
	client, err := f.Client()
	if err != nil {
		return err
	}

	podID := args[0]

	pod, err := client.Pods(namespace).Get(podID)
	if err != nil {
		return err
	}

	var container string
	if len(args) == 1 {
		if len(pod.Spec.Containers) != 1 {
			return fmt.Errorf("POD %s has more than one container; please specify the container to print logs for", pod.ObjectMeta.Name)
		}
		container = pod.Spec.Containers[0].Name
	} else {
		container = args[1]
	}

	follow := false
	if util.GetFlagBool(cmd, "follow") {
		follow = true
	}

	readCloser, err := client.RESTClient.Get().
		Prefix("proxy").
		Resource("minions").
		Name(pod.Status.Host).
		Suffix("containerLogs", namespace, podID, container).
		Param("follow", strconv.FormatBool(follow)).
		Stream()
	if err != nil {
		return err
	}

	defer readCloser.Close()
	_, err = io.Copy(out, readCloser)
	return err
}
