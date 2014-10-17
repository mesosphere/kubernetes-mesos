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

Assuming your mesos cluster is started, and the master is running on `${servicehost}:5050`, then:

```shell
$ ./bin/kubernetes-mesos \
  -address=${servicehost} \
  -machines=${servicehost} \
  -mesos_master=${servicehost}:5050 \
  -etcd_servers=http://${servicehost}:4001 \
  -executor_path=$(pwd)/bin/kubernetes-executor \
  -proxy_path=$(pwd)/bin/proxy
```

To enable replication control, start a controller instance:
```shell
$ ./bin/controller-manager -master=${servicehost}:8080
```

###Launch a Pod

Assuming your framework is running on `${servicehost}:8080`, then:

```shell
$ curl -L http://${servicehost}:8080/api/v1beta1/pods -XPOST -d @examples/pod-nginx.json
```

After the pod get launched, you can check it's status via `curl` or your web browser:
```shell
$ curl -L http://${servicehost}:8080/api/v1beta1/pods
```

```json
{
    "kind": "PodList",
    "creationTimestamp": null,
    "apiVersion": "v1beta1",
    "items": [
        {
            "id": "nginx-id-01",
            "creationTimestamp": "2014-10-17T02:46:00Z",
            "labels": {
                "name": "foo"
            },
            "desiredState": {
                "manifest": {
                    "version": "v1beta1",
                    "id": "nginx-id-01",
                    "uuid": "ba48b2b2-55a7-11e4-8ec3-08002766f5aa",
                    "volumes": null,
                    "containers": [
                        {
                            "name": "nginx-01",
                            "image": "dockerfile/nginx",
                            "ports": [
                                {
                                    "hostPort": 31000,
                                    "containerPort": 80,
                                    "protocol": "TCP"
                                }
                            ],
                            "livenessProbe": {
                                "type": "http",
                                "httpGet": {
                                    "path": "/index.html",
                                    "port": "8081"
                                },
                                "initialDelaySeconds": 30
                            }
                        }
                    ],
                    "restartPolicy": {
                        "always": {}
                    }
                },
                "status": "Running"
            },
            "currentState": {
                "manifest": {
                    "version": "v1beta1",
                    "id": "nginx-id-01",
                    "uuid": "ba48b2b2-55a7-11e4-8ec3-08002766f5aa",
                    "volumes": null,
                    "containers": [
                        {
                            "name": "nginx-01",
                            "image": "dockerfile/nginx",
                            "ports": [
                                {
                                    "hostPort": 31000,
                                    "containerPort": 80,
                                    "protocol": "TCP"
                                }
                            ],
                            "livenessProbe": {
                                "type": "http",
                                "httpGet": {
                                    "path": "/index.html",
                                    "port": "8081"
                                },
                                "initialDelaySeconds": 30
                            }
                        }
                    ],
                    "restartPolicy": {
                        "always": {}
                    }
                },
                "status": "Waiting",
                "info": {
                    "net": {
                        "Id": "6c4dd34a8d213bc276103985baee053e49cec5236706b7d56acf24441e0f975d",
                        "Created": "2014-10-17T02:46:01.923547392Z",
                        "Path": "/pause",
                        "Config": {
                            "Hostname": "nginx-id-01",
                            "ExposedPorts": {
                                "80/tcp": {}
                            },
                            "Env": [
                                "HOME=/",
                                "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
                            ],
                            "Image": "kubernetes/pause:latest",
                            "Entrypoint": [
                                "/pause"
                            ]
                        },
                        "State": {
                            "Running": true,
                            "Pid": 13594,
                            "StartedAt": "2014-10-17T02:46:02.005492006Z",
                            "FinishedAt": "0001-01-01T00:00:00Z"
                        },
                        "Image": "6c4579af347b649857e915521132f15a06186d73faa62145e3eeeb6be0e97c27",
                        "NetworkSettings": {
                            "IPAddress": "172.17.0.13",
                            "IPPrefixLen": 16,
                            "Gateway": "172.17.42.1",
                            "Bridge": "docker0",
                            "Ports": {
                                "80/tcp": [
                                    {
                                        "HostIP": "0.0.0.0",
                                        "HostPort": "31000"
                                    }
                                ]
                            }
                        },
                        "ResolvConfPath": "/etc/resolv.conf",
                        "HostnamePath": "/var/lib/docker/containers/6c4dd34a8d213bc276103985baee053e49cec5236706b7d56acf24441e0f975d/hostname",
                        "HostsPath": "/var/lib/docker/containers/6c4dd34a8d213bc276103985baee053e49cec5236706b7d56acf24441e0f975d/hosts",
                        "Name": "/k8s--net.fa4b7d08--nginx_-_id_-_01.etcd--ba48b2b2_-_55a7_-_11e4_-_8ec3_-_08002766f5aa--9acb0442",
                        "Driver": "aufs",
                        "HostConfig": {
                            "PortBindings": {
                                "80/tcp": [
                                    {
                                        "HostIP": "0.0.0.0",
                                        "HostPort": "31000"
                                    }
                                ]
                            },
                            "RestartPolicy": {}
                        }
                    }
                }
            }
        }
    ]
}
```

Or, you can run `docker ps -a` to verify that the example container is running:

```shell
CONTAINER ID        IMAGE                     COMMAND                CREATED             STATUS              PORTS                   NAMES
02d2d7c91d4f        dockerfile/nginx:latest   nginx                  24 seconds ago      Up 24 seconds                               k8s--nginx_-_01.249c7d76--nginx_-_id_-_01.etcd--ba48b2b2_-_55a7_-_11e4_-_8ec3_-_08002766f5aa--4d088f48
6c4dd34a8d21        kubernetes/pause:latest   /pause                 5 minutes ago       Up 5 minutes        0.0.0.0:31000->80/tcp   k8s--net.fa4b7d08--nginx_-_id_-_01.etcd--ba48b2b2_-_55a7_-_11e4_-_8ec3_-_08002766f5aa--9acb0442
```

### Test

Run test suite with:

```shell
$ go test github.com/mesosphere/kubernetes-mesos/kubernetes-mesos -v
```
