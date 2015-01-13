kubernetes-mesos
================

When [Google Kubernetes][2] meets [Apache Mesos][3]


[![GoDoc] (https://godoc.org/github.com/mesosphere/kubernetes-mesos?status.png)](https://godoc.org/github.com/mesosphere/kubernetes-mesos)

Kubernetes and Mesos are a match made in heaven.
Kubernetes enables the Pod (group of co-located containers) abstraction, along with Pod labels for service discovery, load-balancing, and replication control.
Mesos provides the fine-grained resource allocations for pods across nodes in a cluster, and can make Kubernetes play nicely with other frameworks running on the same cluster resources.

Within the Kubernetes framework for Mesos the framework scheduler first registers with Mesos and begins accepting API requests.
At some point Mesos offers the scheduler sets of available resources from the cluster nodes (slaves/minions).
Once the scheduler is able to match a Mesos resource offer to an unassigned Kubernetes pod it binds the pod to the slave, first sending a launchTasks message to the Mesos master, and then updating the pod's state (desired host).
Mesos marks the resources as claimed and then forwards the request onto the appropriate slave.
The slave fetches the kubelet/executor and starts running it, spawning the pod's containers and communicating the pod/task status back to Mesos.

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

For a binary-only install of the Kubernetes-Mesos framework you can use the Docker-based builder:
```shell
# chcon needed for systems protected by SELinux
$ mkdir bin && chcon -Rt svirt_sandbox_file_t bin   
$ docker run -rm -v $(pwd)/bin:/target jdef/kubernetes-mesos:build-latest
```

Instructions to build and install from source are as follows:

**NOTE:** Building Kubernetes for Mesos requires Go 1.2+, protobuf 2.5.0, and Mesos 0.19+.
Building the project is greatly simplified by using godep.

* To install Mesos, see [mesosphere.io/downloads][4]
* To install godep, see [github.com/tools/godep][5]
* See the [development][1] page for sample environment setup steps.

Once the [prerequisites][7] have been installed you can build the project:
```shell
$ cd $GOPATH # If you don't have one, create directory and set GOPATH accordingly.

$ mkdir -p src/github.com/mesosphere/kubernetes-mesos
$ git clone https://github.com/mesosphere/kubernetes-mesos.git src/github.com/mesosphere/kubernetes-mesos
$ cd src/github.com/mesosphere/kubernetes-mesos && godep restore
$ go install github.com/GoogleCloudPlatform/kubernetes/cmd/{kube-proxy,kube-apiserver,kubecfg,kubectl}
$ go install github.com/mesosphere/kubernetes-mesos/cmd/k8sm-{scheduler,executor,controller-manager}
```

### Start the framework

**NETWORKING:** Kubernetes v0.5 introduced "Services v2" which follows an IP-per-Service model.
A consequence of this is that you must provide the Kubernetes-Mesos framework with a [CIDR][8] subnet that will be used for the allocation of IP addresses for Kubernetes services: the `-portal_net` parameter.
Please keep this in mind when reviewing (and attempting) the example below - the CIDR subnet may need to be adjusted for your network.
See the Kubernetes [release notes][9] for additional details regarding the new services model.

The examples that follow assume that you are running the mesos-master, etcd, and the kubernetes-mesos framework on the same host, exposed on an IP address referred to hereafter as `${servicehost}`.
If you are not running in a production setting then a single etcd instance will suffice.
To run etcd, see [github.com/coreos/etcd][6], or run it via docker:

```shell
$ export servicehost=...  # IP address of the framework host

$ sudo docker run -d --net=host coreos/etcd go-wrapper run \
   -advertise-client-urls=http://${servicehost}:4001 \
   -listen-client-urls=http://${servicehost}:4001 \
   -initial-advertise-peer-urls=http://${servicehost}:7001 \
   -listen-peer-urls=http://${servicehost}:7001
```

Assuming your mesos cluster is started, and that the mesos-master and etcd are running on `${servicehost}`, then:

```shell
$ ./bin/kube-apiserver \
  -address=${servicehost} \
  -mesos_master=${servicehost}:5050 \
  -etcd_servers=http://${servicehost}:4001 \
  -portal_net=10.10.10.0/24 \
  -port=8888 \
  -cloud_provider=mesos \
  -health_check_minions=false

$ ./bin/k8sm-scheduler \
  -address=${servicehost} \
  -mesos_master=${servicehost}:5050 \
  -etcd_servers=http://${servicehost}:4001 \
  -executor_path=$(pwd)/bin/k8sm-executor \
  -proxy_path=$(pwd)/bin/kube-proxy \
  -mesos_user=root \
  -api_servers=$servicehost:8888
```

For simpler execution of `kubecfg`:
```shell
$ export KUBERNETES_MASTER=http://${servicehost}:8888
```

To enable replication control, start a kubernetes replication controller instance:
```shell
$ ./bin/k8sm-controller-manager \
  -master=$servicehost:8888 \
  -mesos_master=$servicehost:5050
```

You can increase logging for both the framework and the controller by including, for example, `-v=2`.
This can be very helpful while debugging.

###Launch a Pod

Assuming your framework is running on `${KUBERNETES_MASTER}`, then:

```shell
$ bin/kubecfg -c examples/pod-nginx.json create pods
# -- or --
$ curl -L ${KUBERNETES_MASTER}/api/v1beta1/pods -XPOST -d @examples/pod-nginx.json
```

After the pod get launched, you can check it's status via `kubecfg`, `curl` or your web browser:
```shell
$ bin/kubecfg list pods
ID                  Image(s)            Host                            Labels                 Status
----------          ----------          ----------                      ----------             ----------
nginx-id-01         dockerfile/nginx    10.132.189.242/10.132.189.242   name=foo               Running

# -- or --
$ curl -L ${KUBERNETES_MASTER}/api/v1beta1/pods
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

Or, you can run `docker ps` on the appropriate Mesos slave to verify that the example container is running:

```shell
$ docker ps
CONTAINER ID        IMAGE                     COMMAND             CREATED             STATUS              PORTS                   NAMES
ca87f2981e6d        dockerfile/nginx:latest   "nginx"             30 seconds ago      Up 30 seconds                               k8s--nginx_-_01.e7078cc4--nginx_-_id_-_01.mesos--ab87fcc4_-_668e_-_11e4_-_bd1f_-_04012f416701--83e4f98d
78e9f83ed4a9        kubernetes/pause:go       "/pause"            5 minutes ago       Up 5 minutes        0.0.0.0:31000->80/tcp   k8s--net.fa4b7d08--nginx_-_id_-_01.mesos--ab87fcc4_-_668e_-_11e4_-_bd1f_-_04012f416701--aa209b8e
```

###Launch a Replication Controller

Assuming your framework is running on `${KUBERNETES_MASTER}` and that you have multiple mesos slaves in your cluster, then:

```shell
$ bin/kubecfg -c examples/controller-nginx.json create replicationControllers
# -- or --
$ curl -L ${KUBERNETES_MASTER}/api/v1beta1/replicationControllers -XPOST -d@examples/controller-nginx.json
```

After the pod get launched, you can check it's status via `kubecfg`, `curl` or your web browser:
```shell
$ bin/kubecfg list replicationControllers
ID                  Image(s)            Selector            Replicas
----------          ----------          ----------          ----------
nginxController     dockerfile/nginx    name=nginx          2

# -- or --
$ curl -L ${KUBERNETES_MASTER}/api/v1beta1/replicationControllers
```
```json
{
    "kind": "ReplicationControllerList",
    "creationTimestamp": null,
    "resourceVersion": 7,
    "apiVersion": "v1beta1",
    "items": [
        {
            "id": "nginxController",
            "creationTimestamp": "2014-10-17T11:10:29Z",
            "resourceVersion": 3,
            "desiredState": {
                "replicas": 2,
                "replicaSelector": {
                    "name": "nginx"
                },
                "podTemplate": {
                    "desiredState": {
                        "manifest": {
                            "version": "v1beta1",
                            "id": "",
                            "volumes": null,
                            "containers": [
                                {
                                    "name": "nginx",
                                    "image": "dockerfile/nginx",
                                    "ports": [
                                        {
                                            "hostPort": 31001,
                                            "containerPort": 80,
                                            "protocol": "TCP"
                                        }
                                    ]
                                }
                            ],
                            "restartPolicy": {
                                "always": {}
                            }
                        }
                    },
                    "labels": {
                        "name": "nginx"
                    }
                }
            },
            "currentState": {
                "replicas": 2,
                "podTemplate": {
                    "desiredState": {
                        "manifest": {
                            "version": "",
                            "id": "",
                            "volumes": null,
                            "containers": null,
                            "restartPolicy": {}
                        }
                    }
                }
            },
            "labels": {
                "name": "nginx"
            }
        }
    ]
}
```

### Test

Run test suite with:

```shell
$ go test github.com/mesosphere/kubernetes-mesos/kubernetes-mesos -v
```

[1]: DEVELOP.md
[2]: https://github.com/GoogleCloudPlatform/kubernetes
[3]: http://mesos.apache.org/
[4]: http://mesosphere.io/downloads
[5]: https://github.com/tools/godep
[6]: https://github.com/coreos/etcd/releases/
[7]: DEVELOP.md#prerequisites
[8]: http://en.wikipedia.org/wiki/Classless_Inter-Domain_Routing
[9]: https://github.com/GoogleCloudPlatform/kubernetes/releases/tag/v0.5
