package scheduler

import "errors"
import "net"
import log "github.com/golang/glog"
import cloud "github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"

var (
	noHostNameSpecified = errors.New("No hostname specified")
)

// implementation of cloud.Interface; Mesos natively provides minimal cloud-type resources.
// More robust cloud support requires a combination of Mesos and cloud-specific knowledge,
// which will likely never be present in this vanilla implementation.
func (k *KubernetesScheduler) Instances() (cloud.Instances, bool) {
	return k, true
}

// implementation of cloud.Interface; Mesos does not provide any type of native load
// balancing by default, so this implementation always returns (nil,false).
func (k *KubernetesScheduler) TCPLoadBalancer() (cloud.TCPLoadBalancer, bool) {
	return nil, false
}

// implementation of cloud.Interface; Mesos does not provide any type of native region
// or zone awareness, so this implementation always returns (nil,false).
func (k *KubernetesScheduler) Zones() (cloud.Zones, bool) {
	return nil, false
}

// implementation of cloud.Instances
func (k *KubernetesScheduler) IPAddress(name string) (net.IP, error) {
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

// implementation of cloud.Instances
func (k *KubernetesScheduler) List(filter string) ([]string, error) {
	k.RLock()
	defer k.RUnlock()

	var slaveHosts []string
	for _, slave := range k.slaves {
		slaveHosts = append(slaveHosts, slave.HostName)
	}
	return slaveHosts, nil
}
