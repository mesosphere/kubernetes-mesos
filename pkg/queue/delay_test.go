package queue

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	tolerance = 100 * time.Millisecond // go time delays aren't perfect, this is our tolerance for errors WRT expected timeouts
)

func timedPriority(t time.Time) Priority {
	return Priority{ts: t}
}

func TestPQ(t *testing.T) {
	t.Parallel()

	var pq priorityQueue
	if pq.Len() != 0 {
		t.Fatalf("pq should be empty")
	}

	now := timedPriority(time.Now())
	now2 := timedPriority(now.ts.Add(2 * time.Second))
	pq.Push(&qitem{priority: now2})
	if pq.Len() != 1 {
		t.Fatalf("pq.len should be 1")
	}
	x := pq.Pop()
	if x == nil {
		t.Fatalf("x is nil")
	}
	if pq.Len() != 0 {
		t.Fatalf("pq should be empty")
	}
	item := x.(*qitem)
	if !item.priority.Equal(now2) {
		t.Fatalf("item.priority != now2")
	}

	pq.Push(&qitem{priority: now2})
	pq.Push(&qitem{priority: now2})
	pq.Push(&qitem{priority: now2})
	pq.Push(&qitem{priority: now2})
	pq.Push(&qitem{priority: now2})
	pq.Pop()
	pq.Pop()
	pq.Pop()
	pq.Pop()
	pq.Pop()
	if pq.Len() != 0 {
		t.Fatalf("pq should be empty")
	}
	now4 := timedPriority(now.ts.Add(4 * time.Second))
	now6 := timedPriority(now.ts.Add(4 * time.Second))
	pq.Push(&qitem{priority: now2})
	pq.Push(&qitem{priority: now4})
	pq.Push(&qitem{priority: now6})
	pq.Swap(0, 2)
	if !pq[0].priority.Equal(now6) || !pq[2].priority.Equal(now2) {
		t.Fatalf("swap failed")
	}
	if pq.Less(1, 2) {
		t.Fatalf("now4 < now2")
	}
}

func TestPopEmptyPQ(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("Expected panic from popping an empty PQ")
		}
	}()
	var pq priorityQueue
	pq.Pop()
}

type testjob struct {
	d time.Duration
	t time.Time
	deadline *time.Time
}

func (j *testjob) GetDelay() time.Duration {
	return j.d
}

func (td *testjob) Deadline() (deadline time.Time, ok bool) {
	if td.deadline != nil {
		return *td.deadline, true
	} else {
		return time.Now(), false
	}
}

func TestDQ_sanity_check(t *testing.T) {
	t.Parallel()

	dq := NewDelayQueue()
	delay := 2 * time.Second
	dq.Add(&testjob{d: delay})

	before := time.Now()
	x := dq.Pop()

	now := time.Now()
	waitPeriod := now.Sub(before)

	if waitPeriod+tolerance < delay {
		t.Fatalf("delay too short: %v, expected: %v", waitPeriod, delay)
	}
	if x == nil {
		t.Fatalf("x is nil")
	}
	item := x.(*testjob)
	if item.d != delay {
		t.Fatalf("d != delay")
	}
}

func TestDQ_Offer(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	dq := NewDelayQueue()
	delay := time.Second

	added := dq.Offer(&testjob{})
	if added {
		t.Fatalf("offered job without deadline added")
	}

	deadline := time.Now().Add(delay)
	added = dq.Offer(&testjob{deadline: &deadline})
	if !added {
		t.Fatalf("offered job with deadline not added")
	}

	before := time.Now()
	x := dq.Pop()

	now := time.Now()
	waitPeriod := now.Sub(before)

	if waitPeriod+tolerance < delay {
		t.Fatalf("delay too short: %v, expected: %v", waitPeriod, delay)
	}
	assert.NotNil(x)
	assert.Equal(x.(*testjob).deadline, &deadline)
}

func TestDQ_ordered_add_pop(t *testing.T) {
	t.Parallel()

	dq := NewDelayQueue()
	dq.Add(&testjob{d: 2 * time.Second})
	dq.Add(&testjob{d: 1 * time.Second})
	dq.Add(&testjob{d: 3 * time.Second})

	var finished [3]*testjob
	before := time.Now()
	idx := int32(-1)
	ch := make(chan bool, 3)
	for _ = range finished {
		go func() {
			var ok bool
			x := dq.Pop()
			i := atomic.AddInt32(&idx, 1)
			if finished[i], ok = x.(*testjob); !ok {
				t.Fatalf("expected a *testjob, not %v", x)
			}
			finished[i].t = time.Now()
			ch <- true
		}()
	}
	<-ch
	<-ch
	<-ch

	after := time.Now()
	totalDelay := after.Sub(before)
	if totalDelay+tolerance < (3 * time.Second) {
		t.Fatalf("totalDelay < 3s: %v", totalDelay)
	}
	for i, v := range finished {
		if v == nil {
			t.Fatalf("task %d was nil", i)
		}
		expected := time.Duration(i+1) * time.Second
		if v.d != expected {
			t.Fatalf("task %d had delay-priority %v, expected %v", i, v.d, expected)
		}
		actualDelay := v.t.Sub(before)
		if actualDelay+tolerance < v.d {
			t.Fatalf("task %d had actual-delay %v < expected delay %v", i, actualDelay, v.d)
		}
	}
}

func TestDQ_always_pop_earliest_deadline(t *testing.T) {
	t.Parallel()

	// add a testjob with delay of 2s
	// spawn a func f1 that attempts to Pop() and wait for f1 to begin
	// add a testjob with a delay of 1s
	// check that the func f1 actually popped the 1s task (not the 2s task)

	dq := NewDelayQueue()
	dq.Add(&testjob{d: 2 * time.Second})
	ch := make(chan *testjob)
	started := make(chan bool)

	go func() {
		started <- true
		x := dq.Pop()
		job := x.(*testjob)
		job.t = time.Now()
		ch <- job
	}()

	<-started
	time.Sleep(500 * time.Millisecond) // give plently of time for Pop() to enter
	expected := 1 * time.Second
	dq.Add(&testjob{d: expected})
	job := <-ch

	if expected != job.d {
		t.Fatalf("Expected delay-prority of %v got instead got %v", expected, job.d)
	}

	job = dq.Pop().(*testjob)
	expected = 2 * time.Second
	if expected != job.d {
		t.Fatalf("Expected delay-prority of %v got instead got %v", expected, job.d)
	}
}

func TestDQ_always_pop_earliest_deadline_multi(t *testing.T) {
	t.Parallel()

	dq := NewDelayQueue()
	dq.Add(&testjob{d: 2 * time.Second})

	ch := make(chan *testjob)
	multi := 10
	started := make(chan bool, multi)

	go func() {
		started <- true
		for i := 0; i < multi; i++ {
			x := dq.Pop()
			job := x.(*testjob)
			job.t = time.Now()
			ch <- job
		}
	}()

	<-started
	time.Sleep(500 * time.Millisecond) // give plently of time for Pop() to enter
	expected := 1 * time.Second

	for i := 0; i < multi; i++ {
		dq.Add(&testjob{d: expected})
	}
	for i := 0; i < multi; i++ {
		job := <-ch
		if expected != job.d {
			t.Fatalf("Expected delay-prority of %v got instead got %v", expected, job.d)
		}
	}

	job := dq.Pop().(*testjob)
	expected = 2 * time.Second
	if expected != job.d {
		t.Fatalf("Expected delay-prority of %v got instead got %v", expected, job.d)
	}
}

func TestDQ_negative_delay(t *testing.T) {
	t.Parallel()

	dq := NewDelayQueue()
	delay := -2 * time.Second
	dq.Add(&testjob{d: delay})

	before := time.Now()
	x := dq.Pop()

	now := time.Now()
	waitPeriod := now.Sub(before)

	if waitPeriod > tolerance {
		t.Fatalf("delay too long: %v, expected something less than: %v", waitPeriod, tolerance)
	}
	if x == nil {
		t.Fatalf("x is nil")
	}
	item := x.(*testjob)
	if item.d != delay {
		t.Fatalf("d != delay")
	}
}
