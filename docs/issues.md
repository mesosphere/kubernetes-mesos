## Known Issues

### Ports

Mesos typically defines `ports` resources for each slave and these ports are consumed by tasks, as their launched, that require one or more host ports.
Kubernetes pod container specifications identify two types of ports, container ports and host ports: 
container ports are allocated from the network namespace of the pod, which is independent from that of the host, whereas;
host ports are allocated from the network namespace of the host.
The k8sm scheduler recognizes the declared host ports of each container in a pod/task and for each port, attempts to allocate that port from mesos resource offers.

If slaves are configured to offer `ports` resources between, for example, 31000 and 32000 then the host ports declared in the pod container specification must fall within that range.
Ports declared outside that range will never match resource offers received by the k8sm scheduler, and so pod specifications that declare such ports will never be executed as tasks on the cluster.

As in Kubernetes proper, a missing pod container host port specification or a host port set to zero will skip allocation of a host port.

### Service Endpoints

At the time of this writing both Kubernetes and Mesos are using IPv4 addressing, albeit under different assumptions.
Mesos clusters configured with Docker typically use default Docker networking, which is host-private.
Kubernetes clusters assume a custom Docker networking configuration that assigns a cluster-routable IPv4 address to each pod.
Kubernetes service endpoints terminate, by default, at a backing pod's IPv4 address using the container-port selected for in the service specification (PodIP:ContainerPort).
This is problematic when default Docker networking has been configured, such as in the case of typical Mesos clusters.

The k8sm project has implemented a work-around: service endpoints are terminated at HostIP:HostPort, where the HostIP resolves to the IP address of the Mesos slave and the HostPort resolves to the host port declared in the pod container port specification.
Thus, this enfores a **new requirement** for Kubernetes services running on the k8sm framework:

* Pod containers that wish to expose a Port to a service must declare a host port in their specification.

The ugly impact of this is that users are required to manually curate the service port space in order to avoid port resource starvation.
To disable the work-around and revert to vanilla Kubernetes service endpoint termination:

* execute the k8sm controller-manager with `-host_port_endpoints=false`
