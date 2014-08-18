kubernetes-mesos
================

When [Google Kubernetes](https://github.com/GoogleCloudPlatform/kubernetes) meets [Apache Mesos](http://mesos.apache.org/)


[![GoDoc] (https://godoc.org/github.com/mesosphere/kubernetes-mesos?status.png)](https://godoc.org/github.com/mesosphere/kubernetes-mesos)

Kubernetes and Mesos are a match made in heaven. Kubernetes enables the Pod (group of co-located containers) abstraction, along with Pod labels for service discovery, load-balancing, and replication control. Mesos provides the fine-grained resource allocations for pods across nodes in a cluster, and can make Kubernetes play nicely with other frameworks running on the same cluster resources. With the Kubernetes framework for Mesos, the framework scheduler will register Kubernetes with Mesos, and then Mesos will begin offering Kubernetes sets of available resources from the cluster nodes (slaves/minions). The framework scheduler will match Mesos' resource offers to Kubernetes pods to run, and then send a launchTasks message to the Mesos master, which will forward the request onto the appropriate slaves. The slave will then fetch the kubelet/executor and start running it. The kubelet will pull down the pod, start running it, and send a TASK_RUNNING update back to the Kubernetes framework scheduler. Once the pods are running on the slaves/minions, they can make use of Kubernetes' pod labels to register themselves in etcd for service discovery, load-balancing, and replication control. 

### Roadmap
This is still very much a work-in-progress, but stay tuned for updates as we continue development. If you have ideas or patches, feel free to contribute!

- [x] Launching pods (on local machine)
  1. Implement Kube-scheduler API
  1. Pick a Pod (FCFS), match it to an offer.
  1. Launch it!
  1. Kubelet as Executor+Containerizer
- [x] Pod Labels: for Service Discovery + Load Balancing
- [x] Running multi-node on GCE
- [ ] Replication Control
- [ ] Use resource shapes to schedule pods
- [ ] Even smarter (Marathon-like) scheduling

### Build

**NOTE** Kubernetes for Mesos requires Go 1.2+ and protobuf 2.5.0.

```shell
$ sudo aptitude install golang libprotobuf-dev mercurial

$ cd $GOPATH # If you don't have one, create directory and set GOPATH accordingly.

$ go get github.com/mesos/mesos-go/mesos
$ export GOPATH=$GOPATH:$GOPATH/src/github.com/GoogleCloudPlatform/kubernetes/third_party
$ go get github.com/mesosphere/kubernetes-mesos/kubernetes-mesos # If version.go fails to build, rerun after:
$ ./src/github.com/GoogleCloudPlatform/kubernetes/hack/version-gen.sh
$ go get github.com/mesosphere/kubernetes-mesos/kubernetes-mesos
$ go get github.com/mesosphere/kubernetes-mesos/kubernetes-executor
$ go install github.com/GoogleCloudPlatform/kubernetes/cmd/proxy
```

### Start the framework

Assuming your mesos cluster is started, and the master is running on `127.0.1.1:5050`, then:

```shell
$ ./bin/kubernetes-mesos \
  -machines=$(hostname) \
  -mesos_master=127.0.1.1:5050 \
  -executor_path=$(pwd)/bin/kubernetes-executor \
  -proxy_path=$(pwd)/bin/proxy
```

###Launch a Pod

Assuming your framework is running on `localhost:8080`, then:

```shell
$ curl -L http://localhost:8080/api/v1beta1/pods -XPOST -d @examples/pod.json
```

After the pod get launched, you can check it's status via `curl` or your web browser:
```shell
$ curl -L http://localhost:8080/api/v1beta1/pods
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
