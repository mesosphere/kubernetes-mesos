kubernetes-mesos
================

When [Google Kubernetes][2] meets [Apache Mesos][3]


[![GoDoc] (https://godoc.org/github.com/mesosphere/kubernetes-mesos?status.png)](https://godoc.org/github.com/mesosphere/kubernetes-mesos)

Kubernetes and Mesos are a match made in heaven.
Kubernetes enables the Pod, an abstraction that represents a group of co-located containers, along with Labels for service discovery, load-balancing, and replication control.
Mesos provides the fine-grained resource allocations for pods across nodes in a cluster, and facilitates resource sharing among Kubernetes and other frameworks running on the same cluster.

This is still very much a work-in-progress, but stay tuned for updates as we continue development.
If you have ideas or patches, feel free to contribute!

Refer to the [documentation pages][12] for a review of core concepts, system architecture, and [known issues][13].

### Tutorial

Mesosphere maintains a tutorial for running [Kubernetes-Mesos on GCE][10], which is periodically updated to coincide with releases of this project.
Please use a release from the [0.3.x series][11] for the best experience with the tutorial.

### Binaries

For a binary-only install of the Kubernetes-Mesos framework you can use the Docker-based builder.
Assuming you are running the latest official binary release of Mesos, the following commands will build and copy the resulting binaries to `./bin`:
```shell
# chcon needed for systems protected by SELinux
$ mkdir bin && chcon -Rt svirt_sandbox_file_t bin
$ docker run --rm -v $(pwd)/bin:/target mesosphere/kubernetes-mesos:build
```

### Build

To build from source follow the instructions below.

**NOTE:** Building Kubernetes for Mesos requires Go 1.3+, [godep][5], and Mesos 0.19+.
Building the project is greatly simplified by using godep.

* To install Mesos, see [mesosphere.io/downloads][4]
* To install godep, see [github.com/tools/godep][5]
* See the [development][1] page for sample environment setup steps.

Kubernetes-Mesos is built using a Makefile to greatly simplify the process of patching the vanilla Kubernetes code.
At this time it is **highly recommended** to use the Makefile instead of building components manually using `go build`.
```shell
$ cd $GOPATH # If you don't have one, create directory and set GOPATH accordingly.

$ mkdir -p src/github.com/mesosphere/kubernetes-mesos
$ git clone https://github.com/mesosphere/kubernetes-mesos.git src/github.com/mesosphere/kubernetes-mesos
$ cd src/github.com/mesosphere/kubernetes-mesos
$ make bootstrap                   # downloads dependencies using godep
$ make                             # compile the binaries
$ make test                        # execute unit tests
$ make install DESTDIR=$(pwd)/bin  # install to ./bin
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

Ensure that your Mesos cluster is started.
If you're running a standalone Mesos master on `${servicehost}` then set:
```shell
$ export mesos_master=${servicehost}:5050
```

Or if you have multiple Mesos masters registered with a Zookeeper cluster then set:
```shell
$ export mesos_master=zk://${zkserver1}:2181,${zkserver2}:2181,${zkserver3}:2181/mesos
```

Assuming that the mesos-master and etcd are running on `${servicehost}`, then:

```shell
$ ./bin/kube-apiserver \
  -address=${servicehost} \
  -mesos_master=${mesos_master} \
  -etcd_servers=http://${servicehost}:4001 \
  -portal_net=10.10.10.0/24 \
  -port=8888 \
  -cloud_provider=mesos \
  -health_check_minions=false

$ ./bin/k8sm-controller-manager \
  -master=$servicehost:8888 \
  -mesos_master=${mesos_master}

$ ./bin/k8sm-scheduler \
  -address=${servicehost} \
  -mesos_master=${mesos_master} \
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

Assuming your framework is running on `${KUBERNETES_MASTER}` and that you have multiple Mesos slaves in your cluster, then:

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
[10]: https://mesosphere.com/docs/tutorials/kubernetes-mesos-gcp/
[11]: https://github.com/mesosphere/kubernetes-mesos/releases
[12]: docs/README.md
[13]: docs/issues.md
