kubernetes-mesos
================

When [Google Kubernetes](https://github.com/GoogleCloudPlatform/kubernetes) meets [Apache Mesos](http://mesos.apache.org/)


[![GoDoc] (https://godoc.org/github.com/mesosphere/kubernetes-mesos?status.png)](https://godoc.org/github.com/mesosphere/kubernetes-mesos)

Kubernetes and Mesos are a match made in heaven. Kubernetes enables the Pod (group of co-located containers) abstraction, along with Pod labels for service discovery, load-balancing, and replication control. Mesos provides the fine-grained resource allocations for pods across nodes in a cluster, and can make Kubernetes play nicely with other frameworks running on the same cluster resources. Within the Kubernetes framework for Mesos, the framework scheduler first registers with Mesos and begins watching etcd's pod registry, and then Mesos offers the scheduler sets of available resources from the cluster nodes (slaves/minions). The scheduler matches Mesos' resource offers to unassigned Kubernetes pods, and then sends a launchTasks message to the Mesos master, which claims the resources and forwards the request onto the appropriate slave. The slave then fetches the kubelet/executor and starts running it. Once the scheduler knows that there are resource claimed for the kubelet to launch its pod, the scheduler writes a Binding to etcd to assign the pod to a specific host. The appropriate kubelet notices the assignment, pulls down the pod, and runs it.

### Roadmap
This is still very much a work-in-progress, but stay tuned for updates as we continue development. If you have ideas or patches, feel free to contribute!

- [x] Launching pods (on local machine)
  1. Implement Kube-scheduler API
  1. Pick a Pod (FCFS), match it to an offer.
  1. Launch it!
  1. Kubelet as Executor+Containerizer
- [x] Pod Labels: for Service Discovery + Load Balancing
- [x] Running multi-node on GCE
- [x] Replication Control
- [ ] Use resource shapes to schedule pods
- [ ] Even smarter (Marathon-like) scheduling

### Build

**NOTE** Kubernetes for Mesos requires Go 1.2+, protobuf 2.5.0, etcd, and Mesos 0.19+. Building the project is grealy simplified by using godep.

To install etcd, see [github.com/coreos/etcd](https://github.com/coreos/etcd/releases/)

To install Mesos, see [mesosphere.io/downloads](http://mesosphere.io/downloads)

To install godep, see [github.com/tools/godep](https://github.com/tools/godep)

```shell
$ sudo aptitude install golang libprotobuf-dev mercurial

$ cd $GOPATH # If you don't have one, create directory and set GOPATH accordingly.

$ mkdir -p src/github.com/mesosphere/kubernetes-mesos
$ git clone git@github.com:mesosphere/kubernetes-mesos.git src/github.com/mesosphere/kubernetes-mesos
$ cd src/github.com/mesosphere/kubernetes-mesos && godep restore
$ go install github.com/GoogleCloudPlatform/kubernetes/cmd/{proxy,controller-manager}
$ go install github.com/mesosphere/kubernetes-mesos/kubernetes-{mesos,executor}
```

### Start the framework

Assuming your mesos cluster is started, and the master is running on `127.0.1.1:5050`, then:

```shell
$ ./bin/kubernetes-mesos \
  -machines=$(hostname) \
  -mesos_master=127.0.1.1:5050 \
  -etcd_servers=http://$(hostname):4001 \
  -executor_path=$(pwd)/bin/kubernetes-executor \
  -proxy_path=$(pwd)/bin/proxy
```

To enable replication control, start a controller instance:
```shell
$ ./bin/controller-manager -master=$(hostname):8080
```

###Launch a Pod

Assuming your framework is running on `$(hostname):8080`, then:

```shell
$ curl -L http://$(hostname):8080/api/v1beta1/pods -XPOST -d @examples/pod.json
```

After the pod get launched, you can check it's status via `curl` or your web browser:
```shell
$ curl -L http://$(hostname):8080/api/v1beta1/pods
```

```json
{
	"kind": "PodList",
	"items": [
		{
			"id": "php",
			"labels": {
				"name": "foo"
			},
			"desiredState": {
				"manifest": {
					"version": "v1beta1",
					"id": "php",
					"volumes": null,
					"containers": [
						{
							"name": "nginx",
							"image": "dockerfile/nginx",
							"ports": [
								{
									"hostPort": 8080,
									"containerPort": 80
								}
							],
							"livenessProbe": {
								"enabled": true,
								"type": "http",
								"httpGet": {
									"path": "/index.html",
									"port": "8080"
								},
								"initialDelaySeconds": 30
							}
						}
					]
				}
			},
			"currentState": {
				"manifest": {
					"version": "",
					"id": "",
					"volumes": null,
					"containers": null
				}
			}
		}
	]
}
```

Or, you can run `docker ps -a` to verify that the example container is running:

```shell
CONTAINER ID        IMAGE                       COMMAND                CREATED             STATUS              PORTS               NAMES
3fba73ff274a        busybox:buildroot-2014.02   sh -c 'rm -f nap &&    57 minutes ago                                              k8s--net--php--9acb0442   
```

### Test

Run test suite with:

```shell
$ go test github.com/mesosphere/kubernetes-mesos/kubernetes-mesos -v
```
