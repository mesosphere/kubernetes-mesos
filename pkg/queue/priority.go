package queue

import (
	"time"
)

type Priority struct {
	ts     time.Time // timestamp
	notify BreakChan // notification channel
}

func (p Priority) Equal(other Priority) bool {
	return p.ts.Equal(other.ts) && p.notify == other.notify
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
