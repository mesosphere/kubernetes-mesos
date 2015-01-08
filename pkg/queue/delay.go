package queue

import (
	"container/heap"
	"sync"
	"time"
)

type Delayed interface {
	// return the remaining delay; a non-positive value indicates no delay
	GetDelay() time.Duration
}

type BreakChan <-chan struct{}

// an optional interface to be implemented by Delayed objects; returning a nil
// channel from Breaker() results in waiting the full delay duration
type Breakout interface {
	// return a channel that signals early departure from a blocking delay
	Breaker() BreakChan
}

type qitem struct {
	value    interface{}
	priority time.Time
	index    int
}

// A priorityQueue implements heap.Interface and holds qitems.
type priorityQueue []*qitem

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	return pq[i].priority.Before(pq[j].priority)
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
	lock  sync.Mutex
	cond  sync.Cond
}

func NewDelayQueue() *DelayQueue {
	q := &DelayQueue{}
	q.cond.L = &q.lock
	return q
}

func (q *DelayQueue) Add(d Delayed) {
	deadline := time.Now().Add(d.GetDelay())

	q.lock.Lock()
	defer q.lock.Unlock()
	heap.Push(&q.queue, &qitem{
		value:    d,
		priority: deadline,
	})
	q.cond.Broadcast()
}

func (q *DelayQueue) next() *qitem {
	q.lock.Lock()
	defer q.lock.Unlock()
	for q.queue.Len() == 0 {
		q.cond.Wait()
	}
	x := heap.Pop(&q.queue)
	item := x.(*qitem)
	return item
}

type empty struct{}

// wait for the delay of the next item in the queue to expire, blocking if
// there are no items in the queue. does not guarantee first-come-first-serve
// ordering with respect to clients.
func (q *DelayQueue) Pop() Delayed {
	var ch chan empty
	for {
		item := q.next()

		var breaker BreakChan
		if breakout, ok := item.value.(Breakout); ok {
			breaker = breakout.Breaker()
		}

		x := item.value.(Delayed)
		waitingPeriod := item.priority.Sub(time.Now())
		if waitingPeriod >= 0 {
			// listen for calls to Add() while we're waiting for the deadline
			if ch == nil {
				ch = make(chan empty, 1)
			}
			go func() {
				q.lock.Lock()
				defer q.lock.Unlock()
				q.cond.Wait()
				ch <- empty{}
			}()
			select {
			case <-time.After(waitingPeriod):
				return x
			case <-ch:
				// we may no longer have the earliest deadline, re-try
				q.Add(x)
				continue
			case <-breaker:
				return x
			}
		}
		return x
	}
}
