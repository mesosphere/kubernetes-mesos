package mesos

import (
	"errors"
	"flag"
	"io"
	"net"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
	log "github.com/golang/glog"
	"golang.org/x/net/context"
)

var (
	noHostNameSpecified = errors.New("No hostname specified")

	//TODO(jdef) this should handle mesos upid's (perhaps once we move to pure bindings)
	mesosMaster = flag.String("mesos_master", "localhost:5050", "Location of leading Mesos master. Default localhost:5050.")
)

func init() {
	cloudprovider.RegisterCloudProvider(
		"mesos",
		func(conf io.Reader) (cloudprovider.Interface, error) {
			return newMesosCloud()
		})
}

type MesosCloud struct {
	client *mesosClient
}

func MasterURI() string {
	return *mesosMaster
}

func newMesosCloud() (*MesosCloud, error) {
	return &MesosCloud{
		client: newMesosClient(),
	}, nil
}

// Mesos natively provides minimal cloud-type resources. More robust cloud
// support requires a combination of Mesos and cloud-specific knowledge, which
// will likely never be present in this vanilla implementation.
func (c *MesosCloud) Instances() (cloudprovider.Instances, bool) {
	return c, true
}

// Mesos does not provide any type of native load balancing by default,
// so this implementation always returns (nil,false).
func (c *MesosCloud) TCPLoadBalancer() (cloudprovider.TCPLoadBalancer, bool) {
	return nil, false
}

// Mesos does not provide any type of native region or zone awareness,
// so this implementation always returns (nil,false).
func (c *MesosCloud) Zones() (cloudprovider.Zones, bool) {
	return nil, false
}

// Mesos does not provide support for multiple clusters
func (c *MesosCloud) Clusters() (cloudprovider.Clusters, bool) {
	//TODO(jdef): we could probably implement this and always return a
	//single cluster- this one.
	return nil, false
}

// IPAddress returns an IP address of the specified instance.
func (c *MesosCloud) IPAddress(name string) (net.IP, error) {
	if name == "" {
		return nil, noHostNameSpecified
	}
	if iplist, err := net.LookupIP(name); err != nil {
		log.V(2).Infof("failed to resolve IP from host name '%v': %v", name, err)
		return nil, err
	} else {
		ipaddr := iplist[0]
		log.V(2).Infof("resolved host '%v' to '%v'", name, ipaddr)
		return ipaddr, nil
	}
}

// List lists instances that match 'filter' which is a regular expression
// which must match the entire instance name (fqdn).
func (c *MesosCloud) List(filter string) ([]string, error) {
	//TODO(jdef) use a timeout here? 15s?
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	// TODO(jdef) listing all slaves for now, until EnlistedSlaves scales better
	// Because of this, minion health checks should be disabled on the apiserver
	addr, err := c.client.EnumerateSlaves(ctx)
	if err == nil && len(addr) == 0 {
		log.V(2).Info("no slaves found, are any running?")
	}
	return addr, err
}

// GetNodeResources gets the resources for a particular node
func (c *MesosCloud) GetNodeResources(name string) (*api.NodeResources, error) {
	return nil, nil
}
