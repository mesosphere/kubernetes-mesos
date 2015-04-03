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
	ch := make(chan struct{})
	go func() {
		err := proc.DoNow(func() {
			close(ch)
		})
		assert.NoError(t, err)
	}()

	select {
	case <-ch:
		go proc.End()
		select {
		case <-proc.Done():
		case <-time.After(time.Millisecond):
			t.Error("Waited to long for Done, after End() called.")
		}
	case <-time.After(time.Millisecond * 5):
		t.Error("Waited too long for DoNow() to execute Action.")
	}
}

func TestProcDoLater(t *testing.T) {
	proc := newProcImpl()
	proc.Begin()
	var wg sync.WaitGroup
	ch := make(chan struct{}, 1)

	// queue up 100 actions, make sure they all get done.
	go func() {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			err := proc.DoLater(func() {
				wg.Done()
			})
			assert.NoError(t, err)
		}
	}()

	go func() {
		wg.Wait()
		ch <- struct{}{}
	}()

	select {
	case <-ch:
	case <-time.After(time.Millisecond * 5):
		t.Error("Tired of waiting for WG.")
	}
}

func TestProcDo(t *testing.T) {
	proc := newProcImpl()
	proc.Begin()
	var wg sync.WaitGroup
	// queue up 100 actions, make sure they all get done.
	go func() {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			err := proc.Do(func() {
				wg.Done()
			})
			assert.NoError(t, err)
		}
	}()

	ch := make(chan struct{}, 1)
	go func() {
		wg.Wait()
		ch <- struct{}{}
	}()

	select {
	case <-ch:
	case <-time.After(time.Millisecond * 5):
		t.Error("Tired of waiting for WG.")
	}
}

// TODO (vvivien) comeback to this test.
// ProcAdapter does not seem to Begin() to start
func TestProcAdapterDo(t *testing.T) {
	procImpl := newProcImpl()
	proc := DoWith(procImpl, procImpl)
	ch := make(chan struct{}, 1)
	go func() {
		err := proc.Do(func() {
			ch <- struct{}{}
		})
		assert.NoError(t, err)
	}()

	// channel ch not signaled.
	// select {
	// case <-ch:
	// case <-time.After(time.Millisecond * 5):
	// 	t.Error("Tired of waiting for WG.")
	// }
}
