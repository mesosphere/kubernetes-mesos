// +build darwin dragonfly freebsd linux netbsd openbsd

package service

import (
	"os"
	"os/signal"
	"syscall"
)

func makeFailoverSigChan() <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	return ch
}

func makeDisownedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true, // disown the spawned scheduler
	}
}
