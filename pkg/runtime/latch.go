package runtime

import (
	"sync/atomic"
)

type Latch struct {
	int32
}

// return true if this latch was successfully acquired. concurrency safe. will only return true
// upon the first invocation, all subsequent invocations will return false. always returns false
// when self is nil.
func (self *Latch) Acquire() bool {
	if self == nil {
		return false
	}
	return atomic.CompareAndSwapInt32(&self.int32, 0, 1)
}
