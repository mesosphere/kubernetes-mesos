package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime"
)

const description = `
  This program starts an HTTP(S) server on a given -addr. Requests to /assets
  are served files present in the specified -assets directory. All other
  requests are proxied through to the given -backend.
`

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	var (
		addr    = address(":8080")
		assets  = dir{"."}
		backend proxyURL
	)

	fs := flag.NewFlagSet("revproxy", flag.ContinueOnError)
	fs.Var(&assets, "assets", "Directory of assets to be served")
	fs.Var(&addr, "addr", "Address to listen on")
	fs.Var(&backend, "backend", "Backend URL to proxy non-asset requests to")

	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "\nusage: revproxy [flags]\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\ndescription:%s\n", description)
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}

	if backend.URL == nil {
		fmt.Fprintln(os.Stderr, `invalid value "" for flag -backend`)
		fs.Usage()
		os.Exit(1)
	}

	middleware := []decorator{logging(os.Stdout)}
	for route, handler := range map[string]http.Handler{
		"/assets/": methods("GET")(http.StripPrefix("/assets/", http.FileServer(assets.Dir))),
		"/":        httputil.NewSingleHostReverseProxy(backend.URL),
	} {
		http.Handle(route, decorate(handler, middleware...))
	}

	fmt.Fprintf(os.Stderr, "Listening on %q and serving %q in /assets/ and proxying / to %q\n", addr, assets, backend)
	if err := http.ListenAndServe(string(addr), nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
