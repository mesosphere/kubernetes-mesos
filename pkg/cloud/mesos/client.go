package mesos

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	log "github.com/golang/glog"
	"golang.org/x/net/context"
)

type mesosClient struct {
	mesosMaster string
	client      *http.Client
	tr          *http.Transport
}

//TODO(jdef) we probably want additional configuration passed in here to
//account for things like HTTP timeout, SSL configuration, etc.
func newMesosClient() *mesosClient {
	tr := &http.Transport{}
	return &mesosClient{
		mesosMaster: MasterURI(),
		client:      &http.Client{Transport: tr},
		tr:          tr,
	}
}

// return a list of slaves running a k8sm kubelet/executor
func (c *mesosClient) EnlistedSlaves(ctx context.Context) ([]string, error) {
	//TODO(jdef) probably should not assume that mesosMaster is a host:port
	uri := fmt.Sprintf("http://%s/state.json", c.mesosMaster)
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}
	hosts := []string{}
	err = c.httpDo(ctx, req, func(res *http.Response, err error) error {
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("HTTP request failed with code %d: %v", res.StatusCode, res.Status)
		}
		//TODO(jdef): remove debug logging once this is working
		blob, err1 := ioutil.ReadAll(res.Body)
		if err1 != nil {
			return err1
		}
		log.V(2).Infof("Got mesos state, content length %v", len(blob))
		type State struct {
			Slaves []*struct {
				Id       string `json:"id"`       // ex: 20150106-162714-3815890698-5050-2453-S2
				Pid      string `json:"pid"`      // ex: slave(1)@10.22.211.18:5051
				Hostname string `json:"hostname"` // ex: 10.22.211.18
			} `json:"slaves"`
		}
		state := &State{}
		err = json.Unmarshal(blob, state)
		if err != nil {
			return err
		}
		//TODO(jdef): filter slaves according to those running the executor/kubelet
		for _, slave := range state.Slaves {
			if slave.Hostname != "" {
				hosts = append(hosts, slave.Hostname)
			}
		}
		return nil
	})
	return hosts, err
}

type responseHandler func(*http.Response, error) error

// hacked from https://blog.golang.org/context
func (c *mesosClient) httpDo(ctx context.Context, req *http.Request, f responseHandler) error {
	// Run the HTTP request in a goroutine and pass the response to f.
	ch := make(chan error, 1)
	go func() { ch <- f(c.client.Do(req)) }()
	select {
	case <-ctx.Done():
		c.tr.CancelRequest(req)
		<-ch // Wait for f to return.
		return ctx.Err()
	case err := <-ch:
		return err
	}
}
