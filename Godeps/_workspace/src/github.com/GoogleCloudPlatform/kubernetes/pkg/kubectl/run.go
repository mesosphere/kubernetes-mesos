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

package kubectl

import (
	"strconv"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
)

type BasicReplicationController struct{}

func (BasicReplicationController) ParamNames() []GeneratorParam {
	return []GeneratorParam{
		{"labels", false},
		{"name", true},
		{"replicas", true},
		{"image", true},
		{"port", false},
	}
}

func (BasicReplicationController) Generate(params map[string]string) (runtime.Object, error) {
	// TODO: extract this flag to a central location.
	labelString, found := params["labels"]
	var labels map[string]string
	if found && len(labelString) > 0 {
		labels = ParseLabels(labelString)
	} else {
		labels = map[string]string{
			"run-container": params["name"],
		}
	}
	count, err := strconv.Atoi(params["replicas"])
	if err != nil {
		return nil, err
	}
	controller := api.ReplicationController{
		ObjectMeta: api.ObjectMeta{
			Name:   params["name"],
			Labels: labels,
		},
		Spec: api.ReplicationControllerSpec{
			Replicas: count,
			Selector: labels,
			Template: &api.PodTemplateSpec{
				ObjectMeta: api.ObjectMeta{
					Labels: labels,
				},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{
							Name:  params["name"],
							Image: params["image"],
						},
					},
				},
			},
		},
	}

	if len(params["port"]) > 0 {
		port, err := strconv.Atoi(params["port"])
		if err != nil {
			return nil, err
		}

		// Don't include the port if it was not specified.
		if port > 0 {
			controller.Spec.Template.Spec.Containers[0].Ports = []api.ContainerPort{
				{
					ContainerPort: port,
				},
			}
		}
	}
	return &controller, nil
}
