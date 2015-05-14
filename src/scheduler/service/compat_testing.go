// +build unit_test

package service

import (
	"os"
	"syscall"
)

func makeFailoverSigChan() <-chan os.Signal {
	return nil
}

func makeDisownedProcAttr() *syscall.SysProcAttr {
	return nil
}
