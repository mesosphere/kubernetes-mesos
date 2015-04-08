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

package dockertools

import (
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/container"
)

type DockerCache interface {
	GetPods() ([]*container.Pod, error)
	ForceUpdateIfOlder(time.Time) error
}

func NewDockerCache(client DockerInterface) (DockerCache, error) {
	return &dockerCache{
		client:        client,
		updatingCache: false,
	}, nil
}

// dockerCache is a default implementation of DockerCache interface
type dockerCache struct {
	// The underlying docker client used to update the cache.
	client DockerInterface

	// Mutex protecting all of the following fields.
	lock sync.Mutex
	// Last time when cache was updated.
	cacheTime time.Time
	// The content of the cache.
	pods []*container.Pod
	// Whether the background thread updating the cache is running.
	updatingCache bool
	// Time when the background thread should be stopped.
	updatingThreadStopTime time.Time
}

// Ensure that dockerCache abides by the DockerCache interface.
var _ DockerCache = new(dockerCache)

func (d *dockerCache) GetPods() ([]*container.Pod, error) {
	d.lock.Lock()
	defer d.lock.Unlock()
	if time.Since(d.cacheTime) > 2*time.Second {
		pods, err := GetPods(d.client, false)
		if err != nil {
			return pods, err
		}
		d.pods = pods
		d.cacheTime = time.Now()
	}
	// Stop refreshing thread if there were no requests within last 2 seconds.
	d.updatingThreadStopTime = time.Now().Add(time.Duration(2) * time.Second)
	if !d.updatingCache {
		d.updatingCache = true
		go d.startUpdatingCache()
	}
	return d.pods, nil
}

func (d *dockerCache) ForceUpdateIfOlder(minExpectedCacheTime time.Time) error {
	d.lock.Lock()
	defer d.lock.Unlock()
	if d.cacheTime.Before(minExpectedCacheTime) {
		pods, err := GetPods(d.client, false)
		if err != nil {
			return err
		}
		d.pods = pods
		d.cacheTime = time.Now()
	}
	return nil
}

func (d *dockerCache) startUpdatingCache() {
	run := true
	for run {
		time.Sleep(100 * time.Millisecond)
		pods, err := GetPods(d.client, false)
		cacheTime := time.Now()
		if err != nil {
			continue
		}

		d.lock.Lock()
		if time.Now().After(d.updatingThreadStopTime) {
			d.updatingCache = false
			run = false
		}
		d.pods = pods
		d.cacheTime = cacheTime
		d.lock.Unlock()
	}
}
