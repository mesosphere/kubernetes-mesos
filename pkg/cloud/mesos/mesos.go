package mesos

import (
	"errors"
	"fmt"
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
)

func init() {
	cloudprovider.RegisterCloudProvider(
		PluginName,
		func(configReader io.Reader) (cloudprovider.Interface, error) {
			return newMesosCloud(configReader)
		})
}

type MesosCloud struct {
	client *mesosClient
}

func MasterURI() string {
	return getConfig().MesosMaster
}

func newMesosCloud(configReader io.Reader) (*MesosCloud, error) {
	err := readConfig(configReader)
	if err != nil {
		return nil, err
	}

	log.V(1).Infof("new mesos cloud, master='%v'", getConfig().MesosMaster)
	if d, err := detector.New(getConfig().MesosMaster); err != nil {
		log.V(1).Infof("failed to create master detector: %v", err)
		return nil, err
	} else if cl, err := newMesosClient(d,
		getConfig().MesosHttpClientTimeout.Duration,
		getConfig().StateCacheTTL.Duration); err != nil {
		log.V(1).Infof("failed to create mesos cloud client: %v", err)
		return nil, err
	} else {
		return &MesosCloud{client: cl}, nil
	}
}

// Mesos natively provides minimal cloud-type resources. More robust cloud
// support requires a combination of Mesos and cloud-specific knowledge.
func (c *MesosCloud) Instances() (cloudprovider.Instances, bool) {
	return c, true
}

// Mesos does not provide any type of native load balancing by default,
// so this implementation always returns (nil, false).
func (c *MesosCloud) TCPLoadBalancer() (cloudprovider.TCPLoadBalancer, bool) {
	return nil, false
}

// Mesos does not provide any type of native region or zone awareness,
// so this implementation always returns (nil, false).
func (c *MesosCloud) Zones() (cloudprovider.Zones, bool) {
	return nil, false
}

// Mesos does not provide support for multiple clusters.
func (c *MesosCloud) Clusters() (cloudprovider.Clusters, bool) {
	return c, true
}

// List lists the names of the available clusters.
func (c *MesosCloud) ListClusters() ([]string, error) {
	// Always returns a single cluster (this one!)
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	name, err := c.client.clusterName(ctx)
	return []string{name}, err
}

// Master gets back the address (either DNS name or IP address) of the master node for the cluster.
func (c *MesosCloud) Master(clusterName string) (string, error) {
	clusters, err := c.ListClusters()
	if err != nil {
		return "", err
	}
	for _, name := range clusters {
		if name == clusterName {
			if c.client.master == "" {
				return "", errors.New("The currently leading master is unknown.")
			}

			host, _, err := net.SplitHostPort(c.client.master)
			if err != nil {
				return "", err
			}

			return host, nil
		}
	}
	return "", errors.New(fmt.Sprintf("The supplied cluster '%v' does not exist", clusterName))
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
