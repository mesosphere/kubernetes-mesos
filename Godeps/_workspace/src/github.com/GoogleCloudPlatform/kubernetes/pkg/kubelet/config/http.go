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

// Reads the pod configuration from an HTTP GET response.
package config

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	"github.com/golang/glog"
)

type sourceURL struct {
	url      string
	hostname string
	updates  chan<- interface{}
	data     []byte
}

func NewSourceURL(url, hostname string, period time.Duration, updates chan<- interface{}) {
	config := &sourceURL{
		url:      url,
		hostname: hostname,
		updates:  updates,
		data:     nil,
	}
	glog.V(1).Infof("Watching URL %s", url)
	go util.Forever(config.run, period)
}

func (s *sourceURL) run() {
	if err := s.extractFromURL(); err != nil {
		glog.Errorf("Failed to read URL: %v", err)
	}
}

func (s *sourceURL) applyDefaults(pod *api.Pod) error {
	return applyDefaults(pod, s.url, false, s.hostname)
}

func (s *sourceURL) extractFromURL() error {
	resp, err := http.Get(s.url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("%v: %v", s.url, resp.Status)
	}
	if len(data) == 0 {
		// Emit an update with an empty PodList to allow HTTPSource to be marked as seen
		s.updates <- kubelet.PodUpdate{[]api.Pod{}, kubelet.SET, kubelet.HTTPSource}
		return fmt.Errorf("zero-length data received from %v", s.url)
	}
	// Short circuit if the manifest has not changed since the last time it was read.
	if bytes.Compare(data, s.data) == 0 {
		return nil
	}
	s.data = data

	// First try as if it's a single manifest
	parsed, manifest, pod, singleErr := tryDecodeSingleManifest(data, s.applyDefaults)
	if parsed {
		if singleErr != nil {
			// It parsed but could not be used.
			return singleErr
		}
		// It parsed!
		s.updates <- kubelet.PodUpdate{[]api.Pod{pod}, kubelet.SET, kubelet.HTTPSource}
		return nil
	}

	// That didn't work, so try an array of manifests.
	parsed, manifests, pods, multiErr := tryDecodeManifestList(data, s.applyDefaults)
	if parsed {
		if multiErr != nil {
			// It parsed but could not be used.
			return multiErr
		}
		// A single manifest that did not pass semantic validation will yield an empty
		// array of manifests (and no error) when unmarshaled as such.  In that case,
		// if the single manifest at least had a Version, we return the single-manifest
		// error (if any).
		if len(manifests) == 0 && len(manifest.Version) != 0 {
			return singleErr
		}
		// It parsed!
		s.updates <- kubelet.PodUpdate{pods.Items, kubelet.SET, kubelet.HTTPSource}
		return nil
	}

	// Parsing it as ContainerManifest(s) failed.
	// Try to parse it as Pod(s).

	// First try as it is a single pod.
	parsed, pod, singlePodErr := tryDecodeSinglePod(data, s.applyDefaults)
	if parsed {
		if singlePodErr != nil {
			// It parsed but could not be used.
			return singlePodErr
		}
		s.updates <- kubelet.PodUpdate{[]api.Pod{pod}, kubelet.SET, kubelet.HTTPSource}
		return nil
	}

	// That didn't work, so try a list of pods.
	parsed, pods, multiPodErr := tryDecodePodList(data, s.applyDefaults)
	if parsed {
		if multiPodErr != nil {
			// It parsed but could not be used.
			return multiPodErr
		}
		s.updates <- kubelet.PodUpdate{pods.Items, kubelet.SET, kubelet.HTTPSource}
		return nil
	}

	return fmt.Errorf("%v: received '%v', but couldn't parse as neither "+
		"single (%v: %+v) or multiple manifests (%v: %+v) nor "+
		"single (%v) or multiple pods (%v).\n",
		s.url, string(data), singleErr, manifest, multiErr, manifests,
		singlePodErr, multiPodErr)
}
