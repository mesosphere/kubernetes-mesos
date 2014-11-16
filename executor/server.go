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

package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/healthz"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/httplog"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/dockertools"
	"github.com/golang/glog"
	"github.com/google/cadvisor/info"
)

// Server is a http.Handler which exposes kubelet functionality over HTTP.
type Server struct {
	host      HostInterface
	updates   chan<- interface{}
	mux       *http.ServeMux
	namespace string
}

// ListenAndServeKubeletServer initializes a server to respond to HTTP network requests on the Kubelet.
func ListenAndServeKubeletServer(host HostInterface, updates chan<- interface{}, address string, port uint, namespace string) {
	glog.Infof("Starting to listen on %s:%d", address, port)
	handler := NewServer(host, updates, namespace)
	s := &http.Server{
		Addr:           net.JoinHostPort(address, strconv.FormatUint(uint64(port), 10)),
		Handler:        &handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	s.ListenAndServe()
}

// HostInterface contains all the kubelet methods required by the server.
// For testablitiy.
type HostInterface interface {
	GetContainerInfo(podFullName, uuid, containerName string, req *info.ContainerInfoRequest) (*info.ContainerInfo, error)
	GetRootInfo(req *info.ContainerInfoRequest) (*info.ContainerInfo, error)
	GetMachineInfo() (*info.MachineInfo, error)
	GetPodInfo(name, uuid string) (api.PodInfo, error)
	ServeLogs(w http.ResponseWriter, req *http.Request)
}

// NewServer initializes and configures a kubelet.Server object to handle HTTP requests.
func NewServer(host HostInterface, updates chan<- interface{}, ns string) Server {
	server := Server{
		host:      host,
		updates:   updates,
		mux:       http.NewServeMux(),
		namespace: ns,
	}
	server.InstallDefaultHandlers()
	return server
}

// InstallDefaultHandlers registers the set of supported HTTP request patterns with the mux.
func (s *Server) InstallDefaultHandlers() {
	healthz.InstallHandler(s.mux)
	s.mux.HandleFunc("/podInfo", s.handlePodInfo)
	s.mux.HandleFunc("/stats/", s.handleStats)
	s.mux.HandleFunc("/logs/", s.handleLogs)
	s.mux.HandleFunc("/spec/", s.handleSpec)
}

// error serializes an error object into an HTTP response.
func (s *Server) error(w http.ResponseWriter, err error) {
	http.Error(w, fmt.Sprintf("Internal Error: %v", err), http.StatusInternalServerError)
}

// handlePodInfo handles podInfo requests against the Kubelet.
func (s *Server) handlePodInfo(w http.ResponseWriter, req *http.Request) {
	req.Close = true
	u, err := url.ParseRequestURI(req.RequestURI)
	if err != nil {
		s.error(w, err)
		return
	}
	podID := u.Query().Get("podID")
	podUUID := u.Query().Get("UUID")
	if len(podID) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		http.Error(w, "Missing 'podID=' query entry.", http.StatusBadRequest)
		return
	}
	// TODO: backwards compatibility with existing API, needs API change
	podFullName := kubelet.GetPodFullName(&kubelet.Pod{Name: podID, Namespace: s.namespace})
	info, err := s.host.GetPodInfo(podFullName, podUUID)
	if err == dockertools.ErrNoContainersInPod {
		http.Error(w, "Pod does not exist", http.StatusNotFound)
		return
	}
	if err != nil {
		s.error(w, err)
		return
	}
	data, err := json.Marshal(info)
	if err != nil {
		s.error(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Header().Add("Content-type", "application/json")
	w.Write(data)
}

// handleStats handles stats requests against the Kubelet.
func (s *Server) handleStats(w http.ResponseWriter, req *http.Request) {
	s.serveStats(w, req)
}

// handleLogs handles logs requests against the Kubelet.
func (s *Server) handleLogs(w http.ResponseWriter, req *http.Request) {
	s.host.ServeLogs(w, req)
}

// handleSpec handles spec requests against the Kubelet.
func (s *Server) handleSpec(w http.ResponseWriter, req *http.Request) {
	info, err := s.host.GetMachineInfo()
	if err != nil {
		s.error(w, err)
		return
	}
	data, err := json.Marshal(info)
	if err != nil {
		s.error(w, err)
		return
	}
	w.Header().Add("Content-type", "application/json")
	w.Write(data)

}

// ServeHTTP responds to HTTP requests on the Kubelet.
func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer httplog.NewLogged(req, &w).StacktraceWhen(
		httplog.StatusIsNot(
			http.StatusOK,
			http.StatusNotFound,
		),
	).Log()
	s.mux.ServeHTTP(w, req)
}

// serveStats implements stats logic.
func (s *Server) serveStats(w http.ResponseWriter, req *http.Request) {
	// /stats/<podfullname>/<containerName> or /stats/<podfullname>/<uuid>/<containerName>
	components := strings.Split(strings.TrimPrefix(path.Clean(req.URL.Path), "/"), "/")
	var stats *info.ContainerInfo
	var err error
	var query info.ContainerInfoRequest
	err = json.NewDecoder(req.Body).Decode(&query)
	if err != nil && err != io.EOF {
		s.error(w, err)
		return
	}
	switch len(components) {
	case 1:
		// Machine stats
		stats, err = s.host.GetRootInfo(&query)
	case 2:
		// pod stats
		// TODO(monnand) Implement this
		errors.New("pod level status currently unimplemented")
	case 3:
		// Backward compatibility without uuid information
		stats, err = s.host.GetContainerInfo(components[1], "", components[2], &query)
	case 4:
		stats, err = s.host.GetContainerInfo(components[1], components[2], components[2], &query)
	default:
		http.Error(w, "unknown resource.", http.StatusNotFound)
		return
	}
	if err != nil {
		s.error(w, err)
		return
	}
	if stats == nil {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "{}")
		return
	}
	data, err := json.Marshal(stats)
	if err != nil {
		s.error(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Header().Add("Content-type", "application/json")
	w.Write(data)
	return
}
