package profile

import "net/http"
import "net/http/pprof"

func InstallHandler(m *http.ServeMux) {
	// register similar endpoints as net/http/pprof.init() does
	m.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	m.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	m.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
}
