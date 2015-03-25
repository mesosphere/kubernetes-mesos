package proc

import (
	"errors"
)

var (
	errActionNotAllowed  = errors.New("action is not permitted to run")
	errProcessTerminated = errors.New("cannot execute action because process has terminated")
)

func NewActionNotAllowedError() error {
	return errActionNotAllowed
}

func IsActionNotAllowedError(err error) bool {
	return err == errActionNotAllowed
}

func IsProcessTerminatedError(err error) bool {
	return err == errProcessTerminated
}
