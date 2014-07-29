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

package apiserver

import (
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

// Operation represents an ongoing action which the server is performing.
type Operation struct {
	ID       string
	result   interface{}
	awaiting <-chan interface{}
	finished *time.Time
	lock     sync.Mutex
	notify   chan struct{}
}

// Operations tracks all the ongoing operations.
type Operations struct {
	// Access only using functions from atomic.
	lastID int64

	// 'lock' guards the ops map.
	lock sync.Mutex
	ops  map[string]*Operation
}

// NewOperations returns a new Operations repository.
func NewOperations() *Operations {
	ops := &Operations{
		ops: map[string]*Operation{},
	}
	go util.Forever(func() { ops.expire(10 * time.Minute) }, 5*time.Minute)
	return ops
}

// NewOperation adds a new operation. It is lock-free.
func (ops *Operations) NewOperation(from <-chan interface{}) *Operation {
	id := atomic.AddInt64(&ops.lastID, 1)
	op := &Operation{
		ID:       strconv.FormatInt(id, 10),
		awaiting: from,
		notify:   make(chan struct{}),
	}
	go op.wait()
	go ops.insert(op)
	return op
}

// Inserts op into the ops map.
func (ops *Operations) insert(op *Operation) {
	ops.lock.Lock()
	defer ops.lock.Unlock()
	ops.ops[op.ID] = op
}

// List operations for an API client.
func (ops *Operations) List() api.ServerOpList {
	ops.lock.Lock()
	defer ops.lock.Unlock()

	ids := []string{}
	for id := range ops.ops {
		ids = append(ids, id)
	}
	sort.StringSlice(ids).Sort()
	ol := api.ServerOpList{}
	for _, id := range ids {
		ol.Items = append(ol.Items, api.ServerOp{JSONBase: api.JSONBase{ID: id}})
	}
	return ol
}

// Get returns the operation with the given ID, or nil
func (ops *Operations) Get(id string) *Operation {
	ops.lock.Lock()
	defer ops.lock.Unlock()
	return ops.ops[id]
}

// Garbage collect operations that have finished longer than maxAge ago.
func (ops *Operations) expire(maxAge time.Duration) {
	ops.lock.Lock()
	defer ops.lock.Unlock()
	keep := map[string]*Operation{}
	limitTime := time.Now().Add(-maxAge)
	for id, op := range ops.ops {
		if !op.expired(limitTime) {
			keep[id] = op
		}
	}
	ops.ops = keep
}

// Waits forever for the operation to complete; call via go when
// the operation is created. Sets op.finished when the operation
// does complete, and closes the notify channel, in case there
// are any WaitFor() calls in progress.
// Does not keep op locked while waiting.
func (op *Operation) wait() {
	defer util.HandleCrash()
	result := <-op.awaiting

	op.lock.Lock()
	defer op.lock.Unlock()
	op.result = result
	finished := time.Now()
	op.finished = &finished
	close(op.notify)
}

// WaitFor waits for the specified duration, or until the operation finishes,
// whichever happens first.
func (op *Operation) WaitFor(timeout time.Duration) {
	select {
	case <-time.After(timeout):
	case <-op.notify:
	}
}

// Returns true if this operation finished before limitTime.
func (op *Operation) expired(limitTime time.Time) bool {
	op.lock.Lock()
	defer op.lock.Unlock()
	if op.finished == nil {
		return false
	}
	return op.finished.Before(limitTime)
}

// StatusOrResult returns status information or the result of the operation if it is complete,
// with a bool indicating true in the latter case.
func (op *Operation) StatusOrResult() (description interface{}, finished bool) {
	op.lock.Lock()
	defer op.lock.Unlock()

	if op.finished == nil {
		return api.Status{
			Status:  api.StatusWorking,
			Details: op.ID,
		}, false
	}
	return op.result, true
}
