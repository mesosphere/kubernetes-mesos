package queue

import (
	"container/heap"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

type Delayed interface {
	// return the remaining delay; a non-positive value indicates no delay
	GetDelay() time.Duration
}

type Deadlined interface {
	// when ok, returns the time when this object should be activated/executed/evaluated
	Deadline() (deadline time.Time, ok bool)
}

// No objects are ever expected to be sent over this channel. References to BreakChan
// instances may be nil (always blocking). Signalling over this channel is performed by
// closing the channel. As such there can only ever be a single signal sent over the
// lifetime of the channel.
type BreakChan <-chan struct{}

// an optional interface to be implemented by Delayed objects; returning a nil
// channel from Breaker() results in waiting the full delay duration
type Breakout interface {
	// return a channel that signals early departure from a blocking delay
	Breaker() BreakChan
}

type Priority struct {
	ts     time.Time // timestamp
	notify BreakChan // notification channel
}

func (p Priority) Equal(other Priority) bool {
	return p.ts.Equal(other.ts) && p.notify == other.notify
}

type qitem struct {
	value    interface{}
	priority Priority
	index    int
	readd    func(item *qitem) // re-add the value of the item to the queue
}

// A priorityQueue implements heap.Interface and holds qitems.
type priorityQueue []*qitem

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	return pq[i].priority.ts.Before(pq[j].priority.ts)
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*qitem)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// concurrency-safe, deadline-oriented queue that returns items after their
// delay period has expired.
type DelayQueue struct {
	queue priorityQueue
	lock  sync.RWMutex
	cond  sync.Cond
}

func NewDelayQueue() *DelayQueue {
	q := &DelayQueue{}
	q.cond.L = &q.lock
	return q
}

func (q *DelayQueue) Add(d Delayed) {
	deadline := extractFromDelayed(d)

	q.lock.Lock()
	defer q.lock.Unlock()
	heap.Push(&q.queue, &qitem{
		value:    d,
		priority: deadline,
		readd: func(qp *qitem) {
			q.Add(qp.value.(Delayed))
		},
	})
	q.cond.Broadcast()
}

// If there's a deadline reported by d.Deadline() then `d` is added to the
// queue and this func returns true.
func (q *DelayQueue) Offer(d Deadlined) bool {
	deadline, ok := extractFromDeadlined(d)
	if ok {
		q.lock.Lock()
		defer q.lock.Unlock()
		heap.Push(&q.queue, &qitem{
			value:    d,
			priority: deadline,
			readd: func(qp *qitem) {
				q.Offer(qp.value.(Deadlined))
			},
		})
		q.cond.Broadcast()
	}
	return ok
}

// wait for the delay of the next item in the queue to expire, blocking if
// there are no items in the queue. does not guarantee first-come-first-serve
// ordering with respect to clients.
func (q *DelayQueue) Pop() interface{} {
	// doesn't implement cancellation, will always return a non-nil value
	return q.pop(func() *qitem {
		q.lock.Lock()
		defer q.lock.Unlock()
		for q.queue.Len() == 0 {
			q.cond.Wait()
		}
		x := heap.Pop(&q.queue)
		item := x.(*qitem)
		return item
	}, nil)
}

// returns a non-nil value from the queue, or else nil if/when cancelled; if cancel
// is nil then cancellation is disabled and this func must return a non-nil value.
func (q *DelayQueue) pop(next func() *qitem, cancel <-chan struct{}) interface{} {
	var ch chan struct{}
	for {
		item := next()
		if item == nil {
			// cancelled
			return nil
		}
		x := item.value
		waitingPeriod := item.priority.ts.Sub(time.Now())
		if waitingPeriod >= 0 {
			// listen for calls to Add() while we're waiting for the deadline
			if ch == nil {
				ch = make(chan struct{}, 1)
			}
			go func() {
				q.lock.Lock()
				defer q.lock.Unlock()
				q.cond.Wait()
				ch <- struct{}{}
			}()
			select {
			case <-cancel:
				item.readd(item)
				return nil
			case <-ch:
				// we may no longer have the earliest deadline, re-try
				item.readd(item)
				continue
			case <-time.After(waitingPeriod):
				// noop
			case <-item.priority.notify:
				// noop
			}
		}
		return x
	}
}

// Decide whether a pre-existing deadline for an item in a delay-queue should be
// updated if an attempt is made to offer/add a new deadline for said item. Whether
// the deadline changes or not has zero impact on the data blob associated with the
// entry in the queue.
type DeadlinePolicy int

const (
	PreferLatest DeadlinePolicy = iota
	PreferEarliest
)

// Decide whether a pre-existing data blob in a delay-queue should be replaced if an
// an attempt is made to add/offer a new data blob in its place. Whether the data is
// replaced has no bearing on the deadline (priority) of the item in the queue.
type ReplacementPolicy int

const (
	KeepExisting ReplacementPolicy = iota
	ReplaceExisting
)

// FIFO receives adds and updates from a Reflector, and puts them in a queue for
// FIFO order processing. If multiple adds/updates of a single item happen while
// an item is in the queue before it has been processed, it will only be
// processed once, and when it is processed, the most recent version will be
// processed. This can't be done with a channel.
type DelayFIFO struct {
	*DelayQueue
	// We depend on the property that items in the set are in the queue and vice versa.
	items          map[string]*qitem
	deadlinePolicy DeadlinePolicy
}

type UniqueDelayed interface {
	UniqueID
	Delayed
}

type UniqueDeadlined interface {
	UniqueID
	Deadlined
}

// Add inserts an item, and puts it in the queue. The item is only enqueued
// if it doesn't already exist in the set.
func (q *DelayFIFO) Add(d UniqueDelayed, rp ReplacementPolicy) {
	deadline := extractFromDelayed(d)
	id := d.GetUID()

	q.lock.Lock()
	defer q.lock.Unlock()
	if item, exists := q.items[id]; !exists {
		item = &qitem{
			value:    d,
			priority: deadline,
			readd: func(qp *qitem) {
				q.Add(qp.value.(UniqueDelayed), KeepExisting)
			},
		}
		heap.Push(&q.queue, item)
		q.items[id] = item
	} else {
		// this is an update of an existing item
		item.value = rp.replacementValue(item.value, d)
		item.priority = q.deadlinePolicy.nextDeadline(item.priority, deadline)
		heap.Fix(&q.queue, item.index)
	}
	q.cond.Broadcast()
}

func (q *DelayFIFO) Offer(d UniqueDeadlined, rp ReplacementPolicy) bool {
	deadline, ok := extractFromDeadlined(d)
	id := d.GetUID()
	if ok {
		q.lock.Lock()
		defer q.lock.Unlock()
		if item, exists := q.items[id]; !exists {
			item = &qitem{
				value:    d,
				priority: deadline,
				readd: func(qp *qitem) {
					q.Offer(qp.value.(UniqueDeadlined), KeepExisting)
				},
			}
			heap.Push(&q.queue, item)
			q.items[id] = item
		} else {
			// this is an update of an existing item
			item.value = rp.replacementValue(item.value, d)
			item.priority = q.deadlinePolicy.nextDeadline(item.priority, deadline)
			heap.Fix(&q.queue, item.index)
		}
		q.cond.Broadcast()
	}
	return ok
}

func extractFromDelayed(d Delayed) Priority {
	deadline := time.Now().Add(d.GetDelay())
	breaker := BreakChan(nil)
	if breakout, good := d.(Breakout); good {
		breaker = breakout.Breaker()
	}
	return Priority{
		ts:     deadline,
		notify: breaker,
	}
}

func extractFromDeadlined(d Deadlined) (Priority, bool) {
	if ts, ok := d.Deadline(); ok {
		breaker := BreakChan(nil)
		if breakout, good := d.(Breakout); good {
			breaker = breakout.Breaker()
		}
		return Priority{
			ts:     ts,
			notify: breaker,
		}, true
	}
	return Priority{}, false
}

func (rp ReplacementPolicy) replacementValue(original, replacement interface{}) (result interface{}) {
	switch rp {
	case KeepExisting:
		result = original
	case ReplaceExisting:
		fallthrough
	default:
		result = replacement
	}
	return
}

func (dp DeadlinePolicy) nextDeadline(a, b Priority) (result Priority) {
	switch dp {
	case PreferEarliest:
		if a.ts.Before(b.ts) {
			result = a
		} else {
			result = b
		}
	case PreferLatest:
		fallthrough
	default:
		if a.ts.After(b.ts) {
			result = a
		} else {
			result = b
		}
	}
	return
}

// Delete removes an item. It doesn't add it to the queue, because
// this implementation assumes the consumer only cares about the objects,
// not their priority order.
func (f *DelayFIFO) Delete(id string) {
	f.lock.Lock()
	defer f.lock.Unlock()
	delete(f.items, id)
}

// List returns a list of all the items.
func (f *DelayFIFO) List() []UniqueID {
	f.lock.RLock()
	defer f.lock.RUnlock()
	list := make([]UniqueID, 0, len(f.items))
	for _, item := range f.items {
		list = append(list, item.value.(UniqueDelayed))
	}
	return list
}

// ContainedIDs returns a util.StringSet containing all IDs of the stored items.
// This is a snapshot of a moment in time, and one should keep in mind that
// other go routines can add or remove items after you call this.
func (c *DelayFIFO) ContainedIDs() util.StringSet {
	c.lock.RLock()
	defer c.lock.RUnlock()
	set := util.StringSet{}
	for id := range c.items {
		set.Insert(id)
	}
	return set
}

// Get returns the requested item, or sets exists=false.
func (f *DelayFIFO) Get(id string) (UniqueID, bool) {
	f.lock.RLock()
	defer f.lock.RUnlock()
	if item, exists := f.items[id]; exists {
		return item.value.(UniqueID), true
	}
	return nil, false
}

// Variant of DelayQueue.Pop() for UniqueDelayed items
func (q *DelayFIFO) Await(timeout time.Duration) UniqueID {
	cancel := make(chan struct{})
	ch := make(chan interface{}, 1)
	go func() { ch <- q.pop(cancel) }()
	var x interface{}
	select {
	case <-time.After(timeout):
		close(cancel)
		x = <-ch
	case x = <-ch:
		// noop
	}
	if x != nil {
		return x.(UniqueID)
	}
	return nil
}

// Variant of DelayQueue.Pop() for UniqueDelayed items
func (q *DelayFIFO) Pop() UniqueID {
	return q.pop(nil).(UniqueID)
}

// variant of DelayQueue.Pop that implements optional cancellation
func (q *DelayFIFO) pop(cancel chan struct{}) interface{} {
	next := func() *qitem {
		q.lock.Lock()
		defer q.lock.Unlock()
		for {
			for q.queue.Len() == 0 {
				signal := make(chan struct{})
				go func() {
					q.cond.Wait()
					close(signal)
				}()
				select {
				case <-cancel:
					// we may not have the lock yet, so
					// broadcast to abort Wait, then
					// return after lock re-acquisition
					q.cond.Broadcast()
					<-signal
					return nil
				case <-signal:
					// we have the lock, re-check
					// the queue for data...
				}
			}
			x := heap.Pop(&q.queue)
			item := x.(*qitem)
			unique := item.value.(UniqueID)
			uid := unique.GetUID()
			if _, ok := q.items[uid]; !ok {
				// item was deleted, keep looking
				continue
			}
			delete(q.items, uid)
			return item
		}
	}
	return q.DelayQueue.pop(next, cancel)
}

func NewDelayFIFO() *DelayFIFO {
	f := &DelayFIFO{
		DelayQueue: NewDelayQueue(),
		items:      map[string]*qitem{},
	}
	return f
}
