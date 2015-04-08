package mesos

import (
	"errors"
	"flag"
	"io"
	"net"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"
	log "github.com/golang/glog"
	"github.com/mesos/mesos-go/detector"
	"golang.org/x/net/context"
)

var (
	PluginName = "mesos"

	noHostNameSpecified = errors.New("No hostname specified")

	//TODO(jdef) this should handle mesos upid's (perhaps once we move to pure bindings)
	mesosMaster = flag.String("mesos_master", "localhost:5050", "Location of leading Mesos master. Default localhost:5050.")
)

func init() {
	cloudprovider.RegisterCloudProvider(
		PluginName,
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
	log.V(1).Infof("new mesos cloud, master='%v'", *mesosMaster)
	if d, err := detector.New(*mesosMaster); err != nil {
		log.V(1).Infof("failed to create master detector: %v", err)
		return nil, err
	} else if cl, err := newMesosClient(d); err != nil {
		log.V(1).Infof("failed to mesos cloud client: %v", err)
		return nil, err
	} else {
		return &MesosCloud{client: cl}, nil
	}
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
func (c *MesosCloud) ipAddress(name string) (net.IP, error) {
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

// ExternalID returns the cloud provider ID of the specified instance.
func (c *MesosCloud) ExternalID(instance string) (string, error) {
	ip, err := c.ipAddress(instance)
	if err != nil {
		return "", err
	}
	return ip.String(), nil
}

// List lists instances that match 'filter' which is a regular expression
// which must match the entire instance name (fqdn).
func (c *MesosCloud) List(filter string) ([]string, error) {
	//TODO(jdef) use a timeout here? 15s?
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	nodes, err := c.client.listSlaves(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		log.V(2).Info("no slaves found, are any running?")
		return nil, nil
	}
	addr := []string{}
	for _, node := range nodes {
		addr = append(addr, node.hostname)
	}
	return addr, err
}

// GetNodeResources gets the resources for a particular node
func (c *MesosCloud) GetNodeResources(name string) (*api.NodeResources, error) {
	//TODO(jdef) use a timeout here? 15s?
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	nodes, err := c.client.listSlaves(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		log.V(2).Info("no slaves found, are any running?")
	} else {
		for _, node := range nodes {
			if name == node.hostname {
				return node.resources, nil
			}
		}
	}
	log.Warningf("failed to locate node spec for %q", name)
	return nil, nil
}

// NodeAddresses returns the addresses of the specified instance.
func (c *MesosCloud) NodeAddresses(name string) ([]api.NodeAddress, error) {
	ip, err := c.ipAddress(name)
	if err != nil {
		return nil, err
	}
	return []api.NodeAddress{{Type: api.NodeLegacyHostIP, Address: ip.String()}}, nil
}
