package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// A decorator decorates an http.Handler with a layer of behaviour
type decorator func(http.Handler) http.Handler

// decorate decorates an http.Handler with all the given decorators, in order.
func decorate(h http.Handler, ds ...decorator) http.Handler {
	decorated := h
	for _, decorate := range ds {
		decorated = decorate(decorated)
	}
	return decorated
}

// logging returns a decorator which wraps an http.Handler with structured
// logging.
func logging(w io.Writer) decorator {
	logger := json.NewEncoder(w)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := capture{w, 200}
			began := time.Now().UTC()
			next.ServeHTTP(&rw, r)
			_ = logger.Encode(map[string]interface{}{
				"time":           began,
				"latency_ns":     time.Since(began),
				"method":         r.Method,
				"code":           rw.code,
				"url":            r.URL.String(),
				"host":           r.Host,
				"remote-addr":    r.RemoteAddr,
				"proto":          r.Proto,
				"content-length": r.ContentLength,
				"headers":        r.Header,
			})
		})
	}
}

// methods returns a decorator which wraps an http.Handler with request method
// verification, responding with MethodNotAllowed if failed.
func methods(ms ...string) decorator {
	const code = http.StatusMethodNotAllowed
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, method := range ms {
				if strings.ToUpper(r.Method) == strings.ToUpper(method) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, http.StatusText(code), code)
		})
	}
}

// capture records the response code returned through the embedded
// ResponseWriter from the WriteHeader call.
type capture struct {
	http.ResponseWriter
	code int
}

// WriteHeader captures the returned code and delegates
func (w *capture) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}
