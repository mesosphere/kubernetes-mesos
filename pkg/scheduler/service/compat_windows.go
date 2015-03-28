// +build windows

package service

import (
	"os"
	"syscall"
)

func makeFailoverSigChan() <-chan os.Signal {
	/* TODO(jdef)
		from go's windows compatibility test, it looks like we need to provide a filtered
		signal channel here

	        c := make(chan os.Signal, 10)
	        signal.Notify(c)
	        select {
	        case s := <-c:
	                if s != os.Interrupt {
	                        log.Fatalf("Wrong signal received: got %q, want %q\n", s, os.Interrupt)
	                }
	        case <-time.After(3 * time.Second):
	                log.Fatalf("Timeout waiting for Ctrl+Break\n")
	        }
	*/
	return nil
}

func makeDisownedProcAttr() *syscall.SysProcAttr {
	//TODO(jdef) test this somehow?!?!
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | syscall.CREATE_UNICODE_ENVIRONMENT,
	}
}
