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
	mesosMaster         = flag.String("mesos_master", "localhost:5050", "Location of leading Mesos master. Default localhost:5050.")
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
	// TODO(jdef): validate that name actually points to a slave that we know
	if iplist, err := net.LookupIP(name); err != nil {
		log.Warningf("Failed to resolve IP from host name '%v': %v", name, err)
		return nil, err
	} else {
		ipaddr := iplist[0]
		log.V(2).Infof("Resolved host '%v' to '%v'", name, ipaddr)
		return ipaddr, nil
	}
}

// List lists instances that match 'filter' which is a regular expression
// which must match the entire instance name (fqdn).
func (c *MesosCloud) List(filter string) (slaves []string, err error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slaves, err = c.client.EnlistedSlaves(ctx)
	if err == nil {
		if len(slaves) == 0 {
			log.V(2).Info("no enlisted slaves found, are any running?")
		} else {
			log.V(2).Infof("slaves=%v", slaves)
		}
	} else {
		log.Warning(err)
	}
	return
}

// GetNodeResources gets the resources for a particular node
func (c *MesosCloud) GetNodeResources(name string) (*api.NodeResources, error) {
	return nil, nil
}
