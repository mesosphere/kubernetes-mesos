package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
)

// dir represents a directory and implements the flag.Value interface with
// fitting parsing and validation logic.
type dir struct{ http.Dir }

func (d dir) String() string { return string(d.Dir) }

func (d *dir) Set(val string) error {
	if files, err := filepath.Glob(filepath.Dir(val)); err != nil {
		return fmt.Errorf("directory is invalid: %s", err)
	} else if len(files) == 0 {
		return fmt.Errorf("directory %q doesn't exist", val)
	}
	d.Dir = http.Dir(val)
	return nil
}

// address represents a "host/IP:port" pair and implements the flag.Value
// interface with fitting parsing and validation logic.
type address string

func (a address) String() string { return string(a) }

func (a *address) Set(val string) error {
	if _, port, err := net.SplitHostPort(val); err != nil {
		return fmt.Errorf("address is invalid: %s", err)
	} else if port == "" {
		return fmt.Errorf("address %q doesn't contain a port", val)
	}
	*a = address(val)
	return nil
}

// proxyURL implements the flag.Value interface for a url.URL with fitting parsing
// and validation logic.
type proxyURL struct{ *url.URL }

func (u proxyURL) String() string {
	if u.URL == nil {
		return ""
	}
	return u.URL.String()
}

func (u *proxyURL) Set(val string) (err error) {
	if u.URL, err = url.Parse(val); err != nil {
		return fmt.Errorf("url is invalid: %s", err)
	} else if got := u.URL.Scheme; got != "http" && got != "https" {
		return fmt.Errorf("url scheme must be http(s). got %q", got)
	} else if u.URL.Host == "" {
		return fmt.Errorf("url host is empty")
	}
	return nil
}
