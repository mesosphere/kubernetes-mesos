package proc

import (
	"github.com/stretchr/testify/assert"
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
		t.Fatalf("Process tasks executed before Begin()")
	}
}

func TestProcBegin(t *testing.T) {
	a := false
	proc := newProcImpl()
	proc.Begin()
	ch := make(chan struct{})

	go func() {
		proc.backlog <- func() {
			a = true
			close(ch)
		}
	}()

	select {
	case <-ch:
		assert.Equal(t, true, a)
	case <-time.After(time.Millisecond * 5):
		t.Fatal("Not processing actions even after Begin called.")
	}
}
