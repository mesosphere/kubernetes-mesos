package proc

import (
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
	"time"
)

func TestNewProcImpl(t *testing.T) {
	proc := newProcImpl()
	assert.Equal(t, 1024, cap(proc.backlog))
	assert.Equal(t, 0, cap(proc.exec))
}

func TestBeforeProcBegin(t *testing.T) {
	proc := newProcImpl()
	ch := make(chan struct{})
	go func() {
		proc.exec <- func() {
			close(ch)
		}
	}()
	select {
	case <-time.After(time.Millisecond):
	case <-ch:
		t.Error("Process tasks executed before Begin()")
	}
}

func TestProcBegin(t *testing.T) {
	a := false
	proc := newProcImpl()
	proc.Begin()
	ch := make(chan struct{})

	go func() {
		proc.backlog <- func() {
			defer close(ch)
			a = true
		}
	}()

	select {
	case <-ch:
		assert.Equal(t, true, a)
	case <-time.After(time.Millisecond * 5):
		t.Error("Not processing actions even after Begin called.")
	}
}

func TestProcEnd(t *testing.T) {
	proc := newProcImpl()
	proc.Begin()
	go func() {
		proc.backlog <- func() {
			proc.End()
		}
	}()

	select {
	case <-proc.Done():
		assert.True(t, len(proc.backlog) == 0) // was backlog emptied
	case <-time.After(time.Millisecond * 5):
		t.Errorf("Done is not signaled when proc.End() called.")
	}
}

func TestProcDoNow(t *testing.T) {
	proc := newProcImpl()
	proc.Begin()
	go proc.DoNow(func() {
		proc.End()
	})

	select {
	case <-proc.Done():
	case <-time.After(time.Millisecond * 5):
		t.Error("DoNow() not firing")
	}
}

func TestProcDoLater(t *testing.T) {
	proc := newProcImpl()
	proc.Begin()
	var wg sync.WaitGroup
	// queue up 100 actions
	go func() {
		for i := 0; i < 100; i++ {
			wg.Add(i)
			proc.DoLater(func() {
				wg.Done()
			})
		}
	}()

	wg.Wait()
}

func TestProcDo(t *testing.T) {
	proc := newProcImpl()
	proc.Begin()
	var wg sync.WaitGroup
	go func() {
		for i := 0; i < 100; i++ {
			wg.Add(i)
			proc.Do(func() {
				wg.Done()
			})
		}
	}()

	wg.Wait()
}
