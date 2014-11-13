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

// copied from k8s: plugin/pkg/scheduler/factory/factory.go
package scheduler

import "sync"
import "time"
import log "github.com/golang/glog"

type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

type backoffEntry struct {
	backoff    time.Duration
	lastUpdate time.Time
}

type podBackoff struct {
	perPodBackoff map[string]*backoffEntry
	lock          sync.Mutex
	clock         clock
}

func (p *podBackoff) getEntry(podID string) *backoffEntry {
	p.lock.Lock()
	defer p.lock.Unlock()
	entry, ok := p.perPodBackoff[podID]
	if !ok {
		entry = &backoffEntry{backoff: 1 * time.Second}
		p.perPodBackoff[podID] = entry
	}
	entry.lastUpdate = p.clock.Now()
	return entry
}

// TODO(jdef): allow for pluggable backoff sequences, strategies here. For
// example we may want to use a fibonacci backoff sequence for certain cluster
// sizes. Or, perhaps we'll allow per-pod backoff factors at some point (ala
// marathon).
func (p *podBackoff) getBackoff(podID string) time.Duration {
	entry := p.getEntry(podID)
	duration := entry.backoff
	entry.backoff *= 2
	if entry.backoff > 60*time.Second {
		entry.backoff = 60 * time.Second
	}
	log.V(3).Infof("Backing off %s for pod %s", duration.String(), podID)
	return duration
}

func (p *podBackoff) wait(podID string, done <-chan struct{}) {
	select {
	case <-time.After(p.getBackoff(podID)):
	case <-done:
	}
}

func (p *podBackoff) gc() {
	p.lock.Lock()
	defer p.lock.Unlock()
	now := p.clock.Now()
	for podID, entry := range p.perPodBackoff {
		if now.Sub(entry.lastUpdate) > 60*time.Second {
			delete(p.perPodBackoff, podID)
		}
	}
}
