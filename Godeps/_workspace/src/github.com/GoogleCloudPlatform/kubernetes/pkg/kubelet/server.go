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

package kubelet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/httplog"
	"github.com/golang/glog"
	"github.com/google/cadvisor/info"
	"gopkg.in/v1/yaml"
)

// Server is a http.Handler which exposes kubelet functionality over HTTP.
type Server struct {
	host    HostInterface
	updates chan<- interface{}
	handler http.Handler
}

func ListenAndServeKubeletServer(host HostInterface, updates chan<- interface{}, delegate http.Handler, address string, port uint) {
	glog.Infof("Starting to listen on %s:%d", address, port)
	handler := Server{
		host:    host,
		updates: updates,
		handler: delegate,
	}
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
	GetContainerInfo(podFullName, containerName string, req *info.ContainerInfoRequest) (*info.ContainerInfo, error)
	GetRootInfo(req *info.ContainerInfoRequest) (*info.ContainerInfo, error)
	GetMachineInfo() (*info.MachineInfo, error)
	GetPodInfo(name string) (api.PodInfo, error)
	ServeLogs(w http.ResponseWriter, req *http.Request)
}

func (s *Server) error(w http.ResponseWriter, err error) {
	http.Error(w, fmt.Sprintf("Internal Error: %v", err), http.StatusInternalServerError)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer httplog.MakeLogged(req, &w).StacktraceWhen(
		httplog.StatusIsNot(
			http.StatusOK,
			http.StatusNotFound,
		),
	).Log()

	u, err := url.ParseRequestURI(req.RequestURI)
	if err != nil {
		s.error(w, err)
		return
	}
	// TODO: use an http.ServeMux instead of a switch.
	switch {
	case u.Path == "/container" || u.Path == "/containers":
		defer req.Body.Close()
		data, err := ioutil.ReadAll(req.Body)
		if err != nil {
			s.error(w, err)
			return
		}
		if u.Path == "/container" {
			// This is to provide backward compatibility. It only supports a single manifest
			var pod Pod
			err = yaml.Unmarshal(data, &pod.Manifest)
			if err != nil {
				s.error(w, err)
				return
			}
			//TODO: sha1 of manifest?
			pod.Name = "1"
			s.updates <- PodUpdate{[]Pod{pod}, SET}
		} else if u.Path == "/containers" {
			var manifests []api.ContainerManifest
			err = yaml.Unmarshal(data, &manifests)
			if err != nil {
				s.error(w, err)
				return
			}
			pods := make([]Pod, len(manifests))
			for i := range manifests {
				pods[i].Name = fmt.Sprintf("%d", i+1)
				pods[i].Manifest = manifests[i]
			}
			s.updates <- PodUpdate{pods, SET}
		}
	case u.Path == "/podInfo":
		podID := u.Query().Get("podID")
		if len(podID) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			http.Error(w, "Missing 'podID=' query entry.", http.StatusBadRequest)
			return
		}
		// TODO: backwards compatibility with existing API, needs API change
		podFullName := GetPodFullName(&Pod{Name: podID, Namespace: "etcd"})
		info, err := s.host.GetPodInfo(podFullName)
		if err == ErrNoContainersInPod {
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
	case strings.HasPrefix(u.Path, "/stats"):
		s.serveStats(w, req)
	case strings.HasPrefix(u.Path, "/spec"):
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
	case strings.HasPrefix(u.Path, "/logs/"):
		s.host.ServeLogs(w, req)
	default:
		if s.handler != nil {
			s.handler.ServeHTTP(w, req)
		}
	}
}

func (s *Server) serveStats(w http.ResponseWriter, req *http.Request) {
	// /stats/<podfullname>/<containerName>
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
		stats, err = s.host.GetContainerInfo(components[1], components[2], &query)
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
