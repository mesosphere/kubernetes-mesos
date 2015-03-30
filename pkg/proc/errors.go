package proc

import (
	"errors"
)

var (
	errProcessTerminated     = errors.New("cannot execute action because process has terminated")
	errIllegalState          = errors.New("illegal state, cannot execute action")
	errActionScheduleTimeout = errors.New("timed out attempting to schedule action for deferred execution")
)

func IsProcessTerminated(err error) bool {
	return err == errProcessTerminated
}

func IsIllegalState(err error) bool {
	return err == errIllegalState
}

func IsActionScheduleTimeout(err error) bool {
	return err == errActionScheduleTimeout
}
