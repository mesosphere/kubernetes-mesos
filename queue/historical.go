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

// evolution of: https://github.com/GoogleCloudPlatform/kubernetes/blob/release-0.6/pkg/client/cache/fifo.go
package queue

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/config"
)

type EventType int

const (
	ADD_EVENT EventType = iota
	UPDATE_EVENT
	DELETE_EVENT
	POP_EVENT
)

type Entry struct {
	Value UniqueCopyable
	Event EventType
}

type Copyable interface {
	// return an independent copy (deep clone) of the current object
	Copy() Copyable
}

type UniqueID interface {
	GetUID() string
}

type UniqueCopyable interface {
	Copyable
	UniqueID
}

func (e *Entry) Copy() Copyable {
	return &Entry{Value: e.Value.Copy().(UniqueCopyable), Event: e.Event}
}

// deliver a message
type pigeon func(msg *Entry)

func dead(msg *Entry) {
	// intentionally blank
}

// HistoricalFIFO receives adds and updates from a Reflector, and puts them in a queue for
// FIFO order processing. If multiple adds/updates of a single item happen while
// an item is in the queue before it has been processed, it will only be
// processed once, and when it is processed, the most recent version will be
// processed. This can't be done with a channel.
type HistoricalFIFO struct {
	lock    sync.RWMutex
	cond    sync.Cond
	items   map[string]*Entry // We depend on the property that items in the queue are in the set.
	queue   []string
	carrier pigeon // may be dead, but never nil
}

// panics if obj doesn't implement UniqueCopyable; otherwise returns the same, typecast object
func checkType(obj interface{}) UniqueCopyable {
	if v, ok := obj.(UniqueCopyable); !ok {
		panic(fmt.Sprintf("Illegal object type, expected UniqueCopyable: %T", obj))
	} else {
		return v
	}
}

// Add inserts an item, and puts it in the queue. The item is only enqueued
// if it doesn't already exist in the set.
func (f *HistoricalFIFO) Add(id string, v interface{}) {
	obj := checkType(v)
	notifications := []*Entry(nil)
	defer func() {
		for _, e := range notifications {
			f.carrier(e)
		}
	}()

	f.lock.Lock()
	defer f.lock.Unlock()

	if entry, exists := f.items[id]; !exists {
		f.queue = append(f.queue, id)
	} else {
		if entry.Event == DELETE_EVENT || entry.Event == POP_EVENT {
			f.queue = append(f.queue, id)
		}
	}
	notifications = f.merge(id, obj)
	f.cond.Broadcast()
}

// Update is the same as Add in this implementation.
func (f *HistoricalFIFO) Update(id string, obj interface{}) {
	f.Add(id, obj)
}

// Add the item to the store, but only if there exists a prior entry for
// for the object in the store whose event type matches that given, and then
// only enqueued if it doesn't already exist in the set.
func (f *HistoricalFIFO) Readd(id string, v interface{}, t EventType) {
	obj := checkType(v)
	notifications := []*Entry(nil)
	defer func() {
		for _, e := range notifications {
			f.carrier(e)
		}
	}()

	f.lock.Lock()
	defer f.lock.Unlock()

	if entry, exists := f.items[id]; exists {
		if entry.Event != t {
			return
		} else if entry.Event == DELETE_EVENT || entry.Event == POP_EVENT {
			f.queue = append(f.queue, id)
		}
	}
	notifications = f.merge(id, obj)
	f.cond.Broadcast()
}

// Delete removes an item. It doesn't add it to the queue, because
// this implementation assumes the consumer only cares about the objects,
// not the order in which they were created/added.
func (f *HistoricalFIFO) Delete(id string) {
	deleteEvent := (*Entry)(nil)
	defer func() {
		f.carrier(deleteEvent)
	}()

	f.lock.Lock()
	defer f.lock.Unlock()
	entry, exists := f.items[id]
	if exists {
		//TODO(jdef): set a timer to expunge the history for this object
		//or else, simply do garbage collection every n'th merge(), removing
		//expired DELETE entries
		deleteEvent = &Entry{Value: entry.Value, Event: DELETE_EVENT}
		f.items[id] = deleteEvent
	}
}

// List returns a list of all the items.
func (f *HistoricalFIFO) List() []interface{} {
	f.lock.RLock()
	defer f.lock.RUnlock()

	// TODO(jdef): slightly overallocates b/c of deleted items
	list := make([]interface{}, 0, len(f.queue))

	for _, entry := range f.items {
		if entry.Event == DELETE_EVENT || entry.Event == POP_EVENT {
			continue
		}
		list = append(list, entry.Value.Copy())
	}
	return list
}

// ContainedIDs returns a util.StringSet containing all IDs of the stored items.
// This is a snapshot of a moment in time, and one should keep in mind that
// other go routines can add or remove items after you call this.
func (c *HistoricalFIFO) ContainedIDs() util.StringSet {
	c.lock.RLock()
	defer c.lock.RUnlock()
	set := util.StringSet{}
	for id, entry := range c.items {
		if entry.Event == DELETE_EVENT || entry.Event == POP_EVENT {
			continue
		}
		set.Insert(id)
	}
	return set
}

// Get returns the requested item, or sets exists=false.
func (f *HistoricalFIFO) Get(id string) (interface{}, bool) {
	f.lock.RLock()
	defer f.lock.RUnlock()
	entry, exists := f.items[id]
	if exists && !(entry.Event == DELETE_EVENT || entry.Event == POP_EVENT) {
		return entry.Value.Copy(), true
	}
	return nil, false
}

// Pop waits until an item is ready and returns it. If multiple items are
// ready, they are returned in the order in which they were added/updated.
// The item is removed from the queue (and the store) before it is returned,
// so if you don't succesfully process it, you need to add it back with Add().
func (f *HistoricalFIFO) Pop() interface{} {
	popEvent := (*Entry)(nil)
	defer func() {
		f.carrier(popEvent)
	}()

	f.lock.Lock()
	defer f.lock.Unlock()
	for {
		for len(f.queue) == 0 {
			f.cond.Wait()
		}
		id := f.queue[0]
		f.queue = f.queue[1:]
		entry, ok := f.items[id]
		if !ok || entry.Event == DELETE_EVENT || entry.Event == POP_EVENT {
			// Item may have been deleted subsequently.
			continue
		}
		value := entry.Value
		popEvent = &Entry{Value: value, Event: POP_EVENT}
		f.items[id] = popEvent
		return value.Copy()
	}
}

// Replace will delete the contents of 'f', using instead the given map.
// 'f' takes ownersip of the map, you should not reference the map again
// after calling this function. f's queue is reset, too; upon return, it
// will contain the items in the map, in no particular order.
func (f *HistoricalFIFO) Replace(idToObj map[string]interface{}) {
	notifications := make([]*Entry, 0, len(idToObj))
	defer func() {
		for _, e := range notifications {
			f.carrier(e)
		}
	}()

	f.lock.Lock()
	defer f.lock.Unlock()

	f.queue = f.queue[:0]
	for id, v := range f.items {
		if _, exists := idToObj[id]; !exists && v.Event != DELETE_EVENT {
			// a non-deleted entry in the items list that doesn't show up in the
			// new list: mark it as deleted
			e := &Entry{Value: v.Value, Event: DELETE_EVENT}
			f.items[id] = e
			notifications = append(notifications, e)
		}
	}
	for id, v := range idToObj {
		obj := checkType(v)
		f.queue = append(f.queue, id)
		n := f.merge(id, obj)
		notifications = append(notifications, n...)
	}
	if len(f.queue) > 0 {
		f.cond.Broadcast()
	}
}

// expects that caller has already locked around state
func (f *HistoricalFIFO) merge(id string, obj UniqueCopyable) (notifications []*Entry) {
	entry, exists := f.items[id]
	if !exists {
		e := &Entry{Value: obj.Copy().(UniqueCopyable), Event: ADD_EVENT}
		f.items[id] = e
		notifications = append(notifications, e)
	} else {
		if entry.Event != DELETE_EVENT && entry.Value.GetUID() != obj.GetUID() {
			// hidden DELETE!
			// (1) append a DELETE
			// (2) append an ADD
			// .. and notify listeners in that order
			e1 := &Entry{Value: entry.Value, Event: DELETE_EVENT}
			e2 := &Entry{Value: obj.Copy().(UniqueCopyable), Event: ADD_EVENT}
			f.items[id] = e2
			notifications = append(notifications, e1, e2)
		} else if !reflect.DeepEqual(obj, entry.Value) {
			//TODO(jdef): it would be nice if we could rely on resource versions
			//instead of doing a DeepEqual. Maybe someday we'll be able to.
			e := &Entry{Value: obj.Copy().(UniqueCopyable), Event: UPDATE_EVENT}
			f.items[id] = e
			notifications = append(notifications, e)
		}
	}
	return
}

// NewFIFO returns a Store which can be used to queue up items to
// process. If a non-nil Mux is provided, then modifications to the
// the FIFO are delivered on a channel specific to this fifo.
func NewFIFO(mux *config.Mux) *HistoricalFIFO {
	carrier := dead
	if mux != nil {
		//TODO(jdef): append a UUID to "fifo" here?
		ch := mux.Channel("fifo")
		carrier = func(msg *Entry) {
			if msg != nil {
				ch <- msg.Copy()
			}
		}
	}
	f := &HistoricalFIFO{
		items:   map[string]*Entry{},
		queue:   []string{},
		carrier: carrier,
	}
	f.cond.L = &f.lock
	return f
}
