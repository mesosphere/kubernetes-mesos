package uuid

import (
	"os/exec"
	"errors"
)

func Gen() (string, error) {
	out, err := exec.Command("uuidgen").Output()
	if err != nil {
		return "", err
	}

	// Make sure we have enough output to strip newline.
	if len(out) <= 0 {
		return "", errors.New("Received empty string from uuidgen")
	}

	// Strip trailing newline.
	return string(out[:len(out) - 1]), nil
}
