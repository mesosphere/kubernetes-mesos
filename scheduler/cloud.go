package scheduler

import "net"
import log "github.com/golang/glog"
import cloud "github.com/GoogleCloudPlatform/kubernetes/pkg/cloudprovider"

// implementation of cloud.Interface
func (k *KubernetesScheduler) Instances() (cloud.Instances, bool) {
	return k, true
}

// implementation of cloud.Interface
func (k *KubernetesScheduler) TCPLoadBalancer() (cloud.TCPLoadBalancer, bool) {
	return nil, false
}

// implementation of cloud.Interface
func (k *KubernetesScheduler) Zones() (cloud.Zones, bool) {
	return nil, false
}

// implementation of cloud.Instances
func (k *KubernetesScheduler) IPAddress(name string) (net.IP, error) {
	if iplist, err := net.LookupIP(name); err != nil {
		log.Warningf("Failed to resolve IP from host name '%v': %v", name, err)
		return nil, err
	} else {
		ipaddr := iplist[0]
		log.V(2).Infof("returning IP to '%v'", ipaddr)
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
