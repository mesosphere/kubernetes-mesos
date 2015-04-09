package proc

import (
	"errors"
)

var (
	errProcessTerminated = errors.New("cannot execute action because process has terminated")
	errIllegalState      = errors.New("illegal state, cannot execute action")
)

func IsProcessTerminated(err error) bool {
	return err == errProcessTerminated
}

func IsIllegalState(err error) bool {
	return err == errIllegalState
}
