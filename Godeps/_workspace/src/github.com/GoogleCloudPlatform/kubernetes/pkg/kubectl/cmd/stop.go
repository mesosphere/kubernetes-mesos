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

	cmdutil "github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl/cmd/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl/resource"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/spf13/cobra"
)

const (
	stop_long = `Gracefully shut down a resource by id or filename.

Attempts to shut down and delete a resource that supports graceful termination.
If the resource is resizable it will be resized to 0 before deletion.`
	stop_example = `// Shut down foo.
$ kubectl stop replicationcontroller foo

// Stop pods and services with label name=myLabel.
$ kubectl stop pods,services -l name=myLabel

// Shut down the service defined in service.json
$ kubectl stop -f service.json

// Shut down all resources in the path/to/resources directory
$ kubectl stop -f path/to/resources`
)

func (f *Factory) NewCmdStop(out io.Writer) *cobra.Command {
	flags := &struct {
		Filenames util.StringList
	}{}
	cmd := &cobra.Command{
		Use:     "stop (-f FILENAME | RESOURCE (ID | -l label | --all))",
		Short:   "Gracefully shut down a resource by id or filename.",
		Long:    stop_long,
		Example: stop_example,
		Run: func(cmd *cobra.Command, args []string) {
			cmdNamespace, err := f.DefaultNamespace()
			cmdutil.CheckErr(err)
			mapper, typer := f.Object()
			r := resource.NewBuilder(mapper, typer, f.ClientMapperForCommand()).
				ContinueOnError().
				NamespaceParam(cmdNamespace).RequireNamespace().
				ResourceTypeOrNameArgs(false, args...).
				FilenameParam(flags.Filenames...).
				SelectorParam(cmdutil.GetFlagString(cmd, "selector")).
				SelectAllParam(cmdutil.GetFlagBool(cmd, "all")).
				Flatten().
				Do()
			cmdutil.CheckErr(r.Err())

			r.Visit(func(info *resource.Info) error {
				reaper, err := f.Reaper(info.Mapping)
				cmdutil.CheckErr(err)
				if _, err := reaper.Stop(info.Namespace, info.Name); err != nil {
					return err
				}
				fmt.Fprintf(out, "%s/%s\n", info.Mapping.Resource, info.Name)
				return nil
			})
		},
	}
	cmd.Flags().VarP(&flags.Filenames, "filename", "f", "Filename, directory, or URL to file of resource(s) to be stopped")
	cmd.Flags().StringP("selector", "l", "", "Selector (label query) to filter on")
	cmd.Flags().Bool("all", false, "[-all] to select all the specified resources")
	return cmd
}
