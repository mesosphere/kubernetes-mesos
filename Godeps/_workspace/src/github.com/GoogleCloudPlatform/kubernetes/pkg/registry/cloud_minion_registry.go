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

package registry

import (
	"fmt"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
)

type CloudMinionRegistry struct {
	cloud   cloudprovider.Interface
	matchRE string
}

func MakeCloudMinionRegistry(cloud cloudprovider.Interface, matchRE string) (*CloudMinionRegistry, error) {
	return &CloudMinionRegistry{
		cloud:   cloud,
		matchRE: matchRE,
	}, nil
}

func (c *CloudMinionRegistry) List() ([]string, error) {
	instances, ok := c.cloud.Instances()
	if !ok {
		return nil, fmt.Errorf("cloud doesn't support instances")
	}

	return instances.List(c.matchRE)
}

func (c *CloudMinionRegistry) Insert(minion string) error {
	return fmt.Errorf("unsupported")
}

func (c *CloudMinionRegistry) Delete(minion string) error {
	return fmt.Errorf("unsupported")
}

func (c *CloudMinionRegistry) Contains(minion string) (bool, error) {
	instances, err := c.List()
	if err != nil {
		return false, err
	}
	for _, name := range instances {
		if name == minion {
			return true, nil
		}
	}
	return false, nil
}
