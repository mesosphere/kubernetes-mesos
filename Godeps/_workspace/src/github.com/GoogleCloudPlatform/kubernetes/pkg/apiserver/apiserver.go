/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apiserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"runtime/debug"
	"strings"
	"time"

	"code.google.com/p/go.net/html"
	"code.google.com/p/go.net/html/atom"
	"code.google.com/p/go.net/websocket"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/httplog"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
	"github.com/golang/glog"
)

// errNotFound is an error which indicates that a specified resource is not found.
type errNotFound string

// Error returns a string representation of the err.
func (err errNotFound) Error() string {
	return string(err)
}

// IsNotFound determines if the err is an error which indicates that a specified resource was not found.
func IsNotFound(err error) bool {
	_, ok := err.(errNotFound)
	return ok
}

// NewNotFoundErr returns a new error which indicates that the resource of the kind and the name was not found.
func NewNotFoundErr(kind, name string) error {
	return errNotFound(fmt.Sprintf("%s %q not found", kind, name))
}

// RESTStorage is a generic interface for RESTful storage services
// Resources whicih are exported to the RESTful API of apiserver need to implement this interface.
type RESTStorage interface {
	// List selects resources in the storage which match to the selector.
	List(labels.Selector) (interface{}, error)

	// Get finds a resource in the storage by id and returns it.
	// Although it can return an arbitrary error value, IsNotFound(err) is true for the returned error value err when the specified resource is not found.
	Get(id string) (interface{}, error)

	// Delete finds a resource in the storage and deletes it.
	// Although it can return an arbitrary error value, IsNotFound(err) is true for the returned error value err when the specified resource is not found.
	Delete(id string) (<-chan interface{}, error)

	Extract(body []byte) (interface{}, error)
	Create(interface{}) (<-chan interface{}, error)
	Update(interface{}) (<-chan interface{}, error)
}

// ResourceWatcher should be implemented by all RESTStorage objects that
// want to offer the ability to watch for changes through the watch api.
type ResourceWatcher interface {
	WatchAll() (watch.Interface, error)
	WatchSingle(id string) (watch.Interface, error)
}

// WorkFunc is used to perform any time consuming work for an api call, after
// the input has been validated. Pass one of these to MakeAsync to create an
// appropriate return value for the Update, Delete, and Create methods.
type WorkFunc func() (result interface{}, err error)

// MakeAsync takes a function and executes it, delivering the result in the way required
// by RESTStorage's Update, Delete, and Create methods.
func MakeAsync(fn WorkFunc) <-chan interface{} {
	channel := make(chan interface{})
	go func() {
		defer util.HandleCrash()
		obj, err := fn()
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case tools.IsEtcdConflict(err):
				status = http.StatusConflict
			}
			channel <- &api.Status{
				Status:  api.StatusFailure,
				Details: err.Error(),
				Code:    status,
			}
		} else {
			channel <- obj
		}
		// 'close' is used to signal that no further values will
		// be written to the channel. Not strictly necessary, but
		// also won't hurt.
		close(channel)
	}()
	return channel
}

// APIServer is an HTTPHandler that delegates to RESTStorage objects.
// It handles URLs of the form:
// ${prefix}/${storage_key}[/${object_name}]
// Where 'prefix' is an arbitrary string, and 'storage_key' points to a RESTStorage object stored in storage.
//
// TODO: consider migrating this to go-restful which is a more full-featured version of the same thing.
type APIServer struct {
	prefix  string
	storage map[string]RESTStorage
	ops     *Operations
	mux     *http.ServeMux
}

// New creates a new APIServer object.
// 'storage' contains a map of handlers.
// 'prefix' is the hosting path prefix.
func New(storage map[string]RESTStorage, prefix string) *APIServer {
	s := &APIServer{
		storage: storage,
		prefix:  strings.TrimRight(prefix, "/"),
		ops:     NewOperations(),
		mux:     http.NewServeMux(),
	}

	s.mux.Handle("/logs/", http.StripPrefix("/logs/", http.FileServer(http.Dir("/var/log/"))))
	s.mux.HandleFunc(s.prefix+"/", s.ServeREST)
	healthz.InstallHandler(s.mux)

	s.mux.HandleFunc("/", s.handleIndex)

	// Handle both operations and operations/* with the same handler
	s.mux.HandleFunc(s.operationPrefix(), s.handleOperationRequest)
	s.mux.HandleFunc(s.operationPrefix()+"/", s.handleOperationRequest)

	s.mux.HandleFunc(s.watchPrefix()+"/", s.handleWatch)

	s.mux.HandleFunc("/proxy/minion/", s.handleMinionReq)

	return s
}

func (s *APIServer) operationPrefix() string {
	return path.Join(s.prefix, "operations")
}

func (s *APIServer) watchPrefix() string {
	return path.Join(s.prefix, "watch")
}

func (server *APIServer) handleIndex(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/" && req.URL.Path != "/index.html" {
		server.notFound(w, req)
		return
	}
	w.WriteHeader(http.StatusOK)
	// TODO: serve this out of a file?
	data := "<html><body>Welcome to Kubernetes</body></html>"
	fmt.Fprint(w, data)
}

func (server *APIServer) handleMinionReq(w http.ResponseWriter, req *http.Request) {
	minionPrefix := "/proxy/minion/"
	if !strings.HasPrefix(req.URL.Path, minionPrefix) {
		server.notFound(w, req)
		return
	}

	path := req.URL.Path[len(minionPrefix):]
	rawQuery := req.URL.RawQuery

	// Expect path as: ${minion}/${query_to_minion}
	// and query_to_minion can be any query that kubelet will accept.
	//
	// For example:
	// To query stats of a minion or a pod or a container,
	// path string can be ${minion}/stats/<podid>/<containerName> or
	// ${minion}/podInfo?podID=<podid>
	//
	// To query logs on a minion, path string can be:
	// ${minion}/logs/
	idx := strings.Index(path, "/")
	minionHost := path[:idx]
	_, port, _ := net.SplitHostPort(minionHost)
	if port == "" {
		// Couldn't retrieve port information
		// TODO: Retrieve port info from a common object
		minionHost += ":10250"
	}
	minionPath := path[idx:]

	minionURL := &url.URL{
		Scheme: "http",
		Host:   minionHost,
	}
	newReq, err := http.NewRequest("GET", minionPath+"?"+rawQuery, nil)
	if err != nil {
		glog.Errorf("Failed to create request: %s", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(minionURL)
	proxy.Transport = &minionTransport{}
	proxy.ServeHTTP(w, newReq)
}

type minionTransport struct{}

func (t *minionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)

	if strings.Contains(resp.Header.Get("Content-Type"), "text/plain") {
		// Do nothing, simply pass through
		return resp, err
	}

	resp, err = t.ProcessResponse(req, resp)
	return resp, err
}

func (t *minionTransport) ProcessResponse(req *http.Request, resp *http.Response) (*http.Response, error) {
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		// copying the response body did not work
		return nil, err
	}

	bodyNode := &html.Node{
		Type:     html.ElementNode,
		Data:     "body",
		DataAtom: atom.Body,
	}
	nodes, err := html.ParseFragment(bytes.NewBuffer(body), bodyNode)
	if err != nil {
		glog.Errorf("Failed to found <body> node: %v", err)
		return resp, err
	}

	// Define the method to traverse the doc tree and update href node to
	// point to correct minion
	var updateHRef func(*html.Node)
	updateHRef = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for i, attr := range n.Attr {
				if attr.Key == "href" {
					Url := &url.URL{
						Path: "/proxy/minion/" + req.URL.Host + req.URL.Path + attr.Val,
					}
					n.Attr[i].Val = Url.String()
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			updateHRef(c)
		}
	}

	newContent := &bytes.Buffer{}
	for _, n := range nodes {
		updateHRef(n)
		err = html.Render(newContent, n)
		if err != nil {
			glog.Errorf("Failed to render: %v", err)
		}
	}

	resp.Body = ioutil.NopCloser(newContent)
	// Update header node with new content-length
	// TODO: Remove any hash/signature headers here?
	resp.Header.Del("Content-Length")
	resp.ContentLength = int64(newContent.Len())

	return resp, err
}

// HTTP Handler interface
func (server *APIServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer func() {
		if x := recover(); x != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "apiserver panic. Look in log for details.")
			glog.Infof("APIServer panic'd on %v %v: %#v\n%s\n", req.Method, req.RequestURI, x, debug.Stack())
		}
	}()
	defer httplog.MakeLogged(req, &w).StacktraceWhen(
		httplog.StatusIsNot(
			http.StatusOK,
			http.StatusAccepted,
			http.StatusConflict,
			http.StatusNotFound,
		),
	).Log()

	// Dispatch via our mux.
	server.mux.ServeHTTP(w, req)
}

// ServeREST handles requests to all our RESTStorage objects.
func (server *APIServer) ServeREST(w http.ResponseWriter, req *http.Request) {
	if !strings.HasPrefix(req.URL.Path, server.prefix) {
		server.notFound(w, req)
		return
	}
	requestParts := strings.Split(req.URL.Path[len(server.prefix):], "/")[1:]
	if len(requestParts) < 1 {
		server.notFound(w, req)
		return
	}
	storage := server.storage[requestParts[0]]
	if storage == nil {
		httplog.LogOf(w).Addf("'%v' has no storage object", requestParts[0])
		server.notFound(w, req)
		return
	}

	server.handleREST(requestParts, req, w, storage)
}

func (server *APIServer) notFound(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "Not Found: %#v", req)
}

func (server *APIServer) write(statusCode int, object interface{}, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	output, err := api.Encode(object)
	if err != nil {
		server.error(err, w)
		return
	}
	w.Write(output)
}

func (server *APIServer) error(err error, w http.ResponseWriter) {
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, "Internal Error: %#v", err)
}

func (server *APIServer) readBody(req *http.Request) ([]byte, error) {
	defer req.Body.Close()
	return ioutil.ReadAll(req.Body)
}

// finishReq finishes up a request, waiting until the operation finishes or, after a timeout, creating an
// Operation to receive the result and returning its ID down the writer.
func (server *APIServer) finishReq(out <-chan interface{}, sync bool, timeout time.Duration, w http.ResponseWriter) {
	op := server.ops.NewOperation(out)
	if sync {
		op.WaitFor(timeout)
	}
	obj, complete := op.StatusOrResult()
	if complete {
		status := http.StatusOK
		switch stat := obj.(type) {
		case api.Status:
			httplog.LogOf(w).Addf("programmer error: use *api.Status as a result, not api.Status.")
			if stat.Code != 0 {
				status = stat.Code
			}
		case *api.Status:
			if stat.Code != 0 {
				status = stat.Code
			}
		}
		server.write(status, obj, w)
	} else {
		server.write(http.StatusAccepted, obj, w)
	}
}

func parseTimeout(str string) time.Duration {
	if str != "" {
		timeout, err := time.ParseDuration(str)
		if err == nil {
			return timeout
		}
		glog.Errorf("Failed to parse: %#v '%s'", err, str)
	}
	return 30 * time.Second
}

// handleREST is the main dispatcher for the server.  It switches on the HTTP method, and then
// on path length, according to the following table:
//   Method     Path          Action
//   GET        /foo          list
//   GET        /foo/bar      get 'bar'
//   POST       /foo          create
//   PUT        /foo/bar      update 'bar'
//   DELETE     /foo/bar      delete 'bar'
// Returns 404 if the method/pattern doesn't match one of these entries
// The server accepts several query parameters:
//    sync=[false|true] Synchronous request (only applies to create, update, delete operations)
//    timeout=<duration> Timeout for synchronous requests, only applies if sync=true
//    labels=<label-selector> Used for filtering list operations
func (server *APIServer) handleREST(parts []string, req *http.Request, w http.ResponseWriter, storage RESTStorage) {
	sync := req.URL.Query().Get("sync") == "true"
	timeout := parseTimeout(req.URL.Query().Get("timeout"))
	switch req.Method {
	case "GET":
		switch len(parts) {
		case 1:
			selector, err := labels.ParseSelector(req.URL.Query().Get("labels"))
			if err != nil {
				server.error(err, w)
				return
			}
			list, err := storage.List(selector)
			if err != nil {
				server.error(err, w)
				return
			}
			server.write(http.StatusOK, list, w)
		case 2:
			item, err := storage.Get(parts[1])
			if IsNotFound(err) {
				server.notFound(w, req)
				return
			}
			if err != nil {
				server.error(err, w)
				return
			}
			server.write(http.StatusOK, item, w)
		default:
			server.notFound(w, req)
		}
	case "POST":
		if len(parts) != 1 {
			server.notFound(w, req)
			return
		}
		body, err := server.readBody(req)
		if err != nil {
			server.error(err, w)
			return
		}
		obj, err := storage.Extract(body)
		if IsNotFound(err) {
			server.notFound(w, req)
			return
		}
		if err != nil {
			server.error(err, w)
			return
		}
		out, err := storage.Create(obj)
		if IsNotFound(err) {
			server.notFound(w, req)
			return
		}
		if err != nil {
			server.error(err, w)
			return
		}
		server.finishReq(out, sync, timeout, w)
	case "DELETE":
		if len(parts) != 2 {
			server.notFound(w, req)
			return
		}
		out, err := storage.Delete(parts[1])
		if IsNotFound(err) {
			server.notFound(w, req)
			return
		}
		if err != nil {
			server.error(err, w)
			return
		}
		server.finishReq(out, sync, timeout, w)
	case "PUT":
		if len(parts) != 2 {
			server.notFound(w, req)
			return
		}
		body, err := server.readBody(req)
		if err != nil {
			server.error(err, w)
			return
		}
		obj, err := storage.Extract(body)
		if IsNotFound(err) {
			server.notFound(w, req)
			return
		}
		if err != nil {
			server.error(err, w)
			return
		}
		out, err := storage.Update(obj)
		if IsNotFound(err) {
			server.notFound(w, req)
			return
		}
		if err != nil {
			server.error(err, w)
			return
		}
		server.finishReq(out, sync, timeout, w)
	default:
		server.notFound(w, req)
	}
}

func (server *APIServer) handleOperationRequest(w http.ResponseWriter, req *http.Request) {
	opPrefix := server.operationPrefix()
	if !strings.HasPrefix(req.URL.Path, opPrefix) {
		server.notFound(w, req)
		return
	}
	trimmed := strings.TrimLeft(req.URL.Path[len(opPrefix):], "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) > 1 {
		server.notFound(w, req)
		return
	}
	if req.Method != "GET" {
		server.notFound(w, req)
		return
	}
	if len(parts) == 0 {
		// List outstanding operations.
		list := server.ops.List()
		server.write(http.StatusOK, list, w)
		return
	}

	op := server.ops.Get(parts[0])
	if op == nil {
		server.notFound(w, req)
		return
	}

	obj, complete := op.StatusOrResult()
	if complete {
		server.write(http.StatusOK, obj, w)
	} else {
		server.write(http.StatusAccepted, obj, w)
	}
}

func (server *APIServer) handleWatch(w http.ResponseWriter, req *http.Request) {
	prefix := server.watchPrefix()
	if !strings.HasPrefix(req.URL.Path, prefix) {
		server.notFound(w, req)
		return
	}
	parts := strings.Split(req.URL.Path[len(prefix):], "/")[1:]
	if req.Method != "GET" || len(parts) < 1 {
		server.notFound(w, req)
	}
	storage := server.storage[parts[0]]
	if storage == nil {
		server.notFound(w, req)
	}
	if watcher, ok := storage.(ResourceWatcher); ok {
		var watching watch.Interface
		var err error
		if id := req.URL.Query().Get("id"); id != "" {
			watching, err = watcher.WatchSingle(id)
		} else {
			watching, err = watcher.WatchAll()
		}
		if err != nil {
			server.error(err, w)
			return
		}

		// TODO: This is one watch per connection. We want to multiplex, so that
		// multiple watches of the same thing don't create two watches downstream.
		watchServer := &WatchServer{watching}
		if req.Header.Get("Connection") == "Upgrade" && req.Header.Get("Upgrade") == "websocket" {
			websocket.Handler(watchServer.HandleWS).ServeHTTP(httplog.Unlogged(w), req)
		} else {
			watchServer.ServeHTTP(w, req)
		}
		return
	}

	server.notFound(w, req)
}

// WatchServer serves a watch.Interface over a websocket or vanilla HTTP.
type WatchServer struct {
	watching watch.Interface
}

// HandleWS implements a websocket handler.
func (w *WatchServer) HandleWS(ws *websocket.Conn) {
	done := make(chan struct{})
	go func() {
		var unused interface{}
		// Expect this to block until the connection is closed. Client should not
		// send anything.
		websocket.JSON.Receive(ws, &unused)
		close(done)
	}()
	for {
		select {
		case <-done:
			w.watching.Stop()
			return
		case event, ok := <-w.watching.ResultChan():
			if !ok {
				// End of results.
				return
			}
			err := websocket.JSON.Send(ws, &api.WatchEvent{
				Type:   event.Type,
				Object: api.APIObject{event.Object},
			})
			if err != nil {
				// Client disconnect.
				w.watching.Stop()
				return
			}
		}
	}
}

// ServeHTTP serves a series of JSON encoded events via straight HTTP with
// Transfer-Encoding: chunked.
func (self *WatchServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	loggedW := httplog.LogOf(w)
	w = httplog.Unlogged(w)

	cn, ok := w.(http.CloseNotifier)
	if !ok {
		loggedW.Addf("unable to get CloseNotifier")
		http.NotFound(loggedW, req)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		loggedW.Addf("unable to get Flusher")
		http.NotFound(loggedW, req)
		return
	}

	loggedW.Header().Set("Transfer-Encoding", "chunked")
	loggedW.WriteHeader(http.StatusOK)
	flusher.Flush()

	encoder := json.NewEncoder(w)
	for {
		select {
		case <-cn.CloseNotify():
			self.watching.Stop()
			return
		case event, ok := <-self.watching.ResultChan():
			if !ok {
				// End of results.
				return
			}
			err := encoder.Encode(&api.WatchEvent{
				Type:   event.Type,
				Object: api.APIObject{event.Object},
			})
			if err != nil {
				// Client disconnect.
				self.watching.Stop()
				return
			}
			flusher.Flush()
		}
	}
}
