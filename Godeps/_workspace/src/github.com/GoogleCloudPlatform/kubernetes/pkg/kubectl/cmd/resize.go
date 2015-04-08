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
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/controller"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl/cmd/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/wait"
	"github.com/spf13/cobra"
)

const (
	resize_long = `Set a new size for a Replication Controller.

Resize also allows users to specify one or more preconditions for the resize action.
If --current-replicas or --resource-version is specified, it is validated before the
resize is attempted, and it is guaranteed that the precondition holds true when the
resize is sent to the server.`
	resize_example = `// Resize replication controller named 'foo' to 3.
$ kubectl resize --replicas=3 replicationcontrollers foo

// If the replication controller named foo's current size is 2, resize foo to 3.
$ kubectl resize --current-replicas=2 --replicas=3 replicationcontrollers foo`

	retryFrequency = controller.DefaultSyncPeriod / 100
	retryTimeout   = 10 * time.Second
)

func (f *Factory) NewCmdResize(out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "resize [--resource-version=version] [--current-replicas=count] --replicas=COUNT RESOURCE ID",
		Short:   "Set a new size for a Replication Controller.",
		Long:    resize_long,
		Example: resize_example,
		Run: func(cmd *cobra.Command, args []string) {
			err := RunResize(f, out, cmd, args)
			util.CheckErr(err)
		},
	}
	cmd.Flags().String("resource-version", "", "Precondition for resource version. Requires that the current resource version match this value in order to resize.")
	cmd.Flags().Int("current-replicas", -1, "Precondition for current size. Requires that the current size of the replication controller match this value in order to resize.")
	cmd.Flags().Int("replicas", -1, "The new desired number of replicas. Required.")
	return cmd
}

func RunResize(f *Factory, out io.Writer, cmd *cobra.Command, args []string) error {
	count := util.GetFlagInt(cmd, "replicas")
	if len(args) != 2 || count < 0 {
		return util.UsageError(cmd, "--replicas=COUNT RESOURCE ID")
	}

	cmdNamespace, err := f.DefaultNamespace()
	if err != nil {
		return err
	}

	mapper, _ := f.Object()
	// TODO: use resource.Builder instead
	mapping, namespace, name, err := util.ResourceFromArgs(cmd, args, mapper, cmdNamespace)
	if err != nil {
		return err
	}

	resizer, err := f.Resizer(mapping)
	if err != nil {
		return err
	}

	resourceVersion := util.GetFlagString(cmd, "resource-version")
	currentSize := util.GetFlagInt(cmd, "current-replicas")
	precondition := &kubectl.ResizePrecondition{currentSize, resourceVersion}
	cond := kubectl.ResizeCondition(resizer, precondition, namespace, name, uint(count))

	msg := "resized"
	if err = wait.Poll(retryFrequency, retryTimeout, cond); err != nil {
		msg = fmt.Sprintf("Failed to resize controller in spite of retrying for %s", retryTimeout)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "%s\n", msg)
	return nil
}
