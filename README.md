kubernetes-mesos
================

When [Google Kubernetes][2] meets [Apache Mesos][3]


[![GoDoc] (https://godoc.org/github.com/mesosphere/kubernetes-mesos?status.png)](https://godoc.org/github.com/mesosphere/kubernetes-mesos)
[![Build Status](https://travis-ci.org/mesosphere/kubernetes-mesos.svg)](https://travis-ci.org/mesosphere/kubernetes-mesos)
[![Coverage Status](https://coveralls.io/repos/mesosphere/kubernetes-mesos/badge.svg)](https://coveralls.io/r/mesosphere/kubernetes-mesos)

Kubernetes and Mesos are a match made in heaven.
Kubernetes enables the Pod, an abstraction that represents a group of co-located containers, along with Labels for service discovery, load-balancing, and replication control.
Mesos provides the fine-grained resource allocations for pods across nodes in a cluster, and facilitates resource sharing among Kubernetes and other frameworks running on the same cluster.

This is still very much a work-in-progress, but stay tuned for updates as we continue development.
If you have ideas or patches, feel free to contribute!

Refer to the [documentation pages][12] for a review of core concepts, system architecture, and [known issues][13].

This project is currently based on the [Kubernetes v0.14 release][14] and has customized the release distribution:
components have been added, removed, and/or changed.

### Tutorial

Mesosphere maintains a tutorial for running [Kubernetes-Mesos on GCE][10], which is periodically updated to coincide with releases of this project.

### Binaries

For a binary-only install of the Kubernetes-Mesos framework you can use the Docker-based builder.
The following commands will build and copy the resulting binaries to `./bin`:
```shell
# chcon needed for systems protected by SELinux
$ mkdir bin && chcon -Rt svirt_sandbox_file_t bin
$ docker run --rm -v $(pwd)/bin:/target mesosphere/kubernetes-mesos:build
```

### Build

To build from source follow the instructions below.
Before building anything please review all of the instructions, including any environment setup steps that may be required prior to the actual build.

**NOTE:** Building Kubernetes for Mesos requires Go 1.4+.
* See the [development][1] page for sample environment setup steps.

Kubernetes-Mesos is built using a Makefile to automate the process of patching the vanilla Kubernetes code.
At this time it is **highly recommended** to use the Makefile instead of building components manually using `go build`.
```shell
$ git clone https://github.com/mesosphere/kubernetes-mesos.git k8sm
$ cd k8sm
$ make                             # compile the binaries
$ make test                        # execute unit tests
$ make install DESTDIR=$(pwd)/bin  # install to ./bin
```

### Start the framework

**NETWORKING:** Kubernetes v0.5 introduced "Services v2" which follows an IP-per-Service model.
A consequence of this is that you must provide the Kubernetes-Mesos framework with a [CIDR][8] subnet that will be used for the allocation of IP addresses for Kubernetes services: the `-portal_net` parameter.
Please keep this in mind when reviewing (and attempting) the example below - the CIDR subnet may need to be adjusted for your network.
See the Kubernetes [release notes][9] for additional details regarding the new services model.

The examples that follow assume that you have a running Mesos cluster.
If needed you may download and install Mesos binaries through your OS package manager, or else download them directly from [Mesosphere][4] and install them manually.
Once your Mesos cluster is up and running you're ready to fire up kubernetes-mesos.

To keep things simple the following guide also assumes that you intend to run the mesos-master, etcd, and the kubernetes-mesos framework processes on the same host, exposed on an IP address referred to hereafter as `${servicehost}`.
Alternatively, the instructions also support a multi-mesos-master cluster running with Zookeeper.

```shell
$ export servicehost=...  # IP address of the framework host, perhaps $(hostname -i)
```

#### etcd

If you are not running in a production setting then a single etcd instance will suffice.
To run etcd, see [github.com/coreos/etcd][6], or run it via docker:

```shell
$ sudo docker run -d --hostname $(hostname -f) -p 4001:4001 -p 7001:7001 --name=etcd coreos/etcd
```

**NOTE:** A troublesome etcd bug, [discovered][15] circa Kubernetes v0.14.2, has been fixed in etcd v2.0.9.
If you are running an older version of etcd you may want to consider an upgrade.

#### kubernetes-mesos

Ensure that your Mesos cluster is started.

Create a configuration file for the Mesos cloud provider:

```shell
$ edit ./mesos-cloud.conf
```

The file content should have the following format:

```ini
[mesos-cloud]
	mesos-master        = host:port
	http-client-timeout = 5s
	state-cache-ttl     = 20s
```

**Mesos Cloud Provider Configuration Options:**

- `mesos-master`: Location of the Mesos master.  This value can take the
  form `host:port` or a canonical Zookeeper URL
(`zk://host1:port1,host2:port2/your/znode/path`)
- `http-client-timeout`: Time to wait for a response from the Mesos
  master when querying the state.
- `state-cache-ttl`: Maximum age of the cached Mesos state, after which
  new values will be fetched.

Fire up the kubernetes-mesos framework components (yes, these are **all** required for a working framework):

```shell
$ ./bin/km apiserver \
  --address=${servicehost} \
  --etcd_servers=http://${servicehost}:4001 \
  --portal_net=10.10.10.0/24 \
  --port=8888 \
  --cloud_provider=mesos \
  --cloud_config=./mesos-cloud.conf \
  --v=2 >apiserver.log 2>&1 &

$ ./bin/km controller-manager \
  --master=$servicehost:8888 \
  --cloud_config=./mesos-cloud.conf \
  --v=2 >controller.log 2>&1 &

$ ./bin/km scheduler \
  --mesos_master=${mesos_masster} \
  --address=${servicehost} \
  --etcd_servers=http://${servicehost}:4001 \
  --mesos_user=root \
  --api_servers=$servicehost:8888 \
  --v=2 >scheduler.log 2>&1 &
```

For simpler execution of `kubectl`:
```shell
$ export KUBERNETES_MASTER=http://${servicehost}:8888
```

You can increase the verbosity of the logging for the API server, scheduler, and/or the controller-manager by including, for example, `--v=2`.
This can be very helpful while debugging.

###Launch a Pod

Assuming your framework is running on `${KUBERNETES_MASTER}`, then:

```shell
$ bin/kubectl create -f examples/pod-nginx.json
# -- or --
$ curl -L ${KUBERNETES_MASTER}/api/v1beta1/pods -XPOST -d @examples/pod-nginx.json
```

After the pod get launched, you can check it's status via `kubectl`, `curl` or your web browser:
```shell
$ bin/kubectl get pods
POD          IP           CONTAINER(S)  IMAGE(S)          HOST                       LABELS    STATUS
nginx-id-01  172.17.6.20  nginx-01      dockerfile/nginx  10.22.211.18/10.22.211.18  name=foo  Running

# -- or --
$ curl -L ${KUBERNETES_MASTER}/api/v1beta1/pods
```
```json
{
  "kind": "PodList",
  "creationTimestamp": null,
  "selfLink": "/api/v1beta1/pods?namespace=",
  "resourceVersion": 2800,
  "apiVersion": "v1beta1",
  "items": [
    {
      "id": "nginx-id-01",
      "uid": "2cf6694e-bd16-11e4-90fa-42010adf71e3",
      "creationTimestamp": "2015-02-25T17:46:06Z",
      "selfLink": "/api/v1beta1/pods/nginx-id-01?namespace=default",
      "resourceVersion": 2751,
      "namespace": "default",
      "labels": {
        "name": "foo"
      },
      "desiredState": {
        "manifest": {
          "version": "v1beta2",
          "id": "",
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
              "resources": {},
              "livenessProbe": {
                "httpGet": {
                  "path": "/",
                  "port": "80"
                },
                "initialDelaySeconds": 30,
                "timeoutSeconds": 1
              },
              "terminationMessagePath": "/dev/termination-log",
              "imagePullPolicy": "PullIfNotPresent",
              "capabilities": {}
            }
          ],
          "restartPolicy": {
            "always": {}
          },
          "dnsPolicy": "ClusterFirst"
        }
      },
      "currentState": {
        "manifest": {
          "version": "",
          "id": "",
          "volumes": null,
          "containers": null,
          "restartPolicy": {}
        },
        "status": "Running",
        "Condition": [
          {
            "kind": "Ready",
            "status": "Full"
          }
        ],
        "host": "10.22.211.18",
        "hostIP": "10.22.211.18",
        "podIP": "172.17.6.20",
        "info": {
          "POD": {
            "state": {
              "running": {
                "startedAt": "2015-02-25T17:46:07Z"
              }
            },
            "ready": false,
            "restartCount": 0,
            "podIP": "172.17.6.20",
            "image": "kubernetes/pause:latest",
            "imageID": "docker://6c4579af347b649857e915521132f15a06186d73faa62145e3eeeb6be0e97c27",
            "containerID": "docker://811760e5070fd3c8e8014aff2a169831adc0c602833b540f689d76be6fadabf1"
          },
          "nginx-01": {
            "state": {
              "running": {
                "startedAt": "2015-02-25T17:46:08Z"
              }
            },
            "ready": true,
            "restartCount": 0,
            "image": "dockerfile/nginx",
            "imageID": "docker://0180c66bffa9450b438970b9350295c1b5d4345a8b4573abe8e2b46b075e63f8",
            "containerID": "docker://371ae01aa63b01875410f5bcca42ccdcc03872d64c351fbdcadcce2cba4a578f"
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
CONTAINER ID  IMAGE                     COMMAND   CREATED  STATUS  PORTS                  NAMES
371ae01aa63b  dockerfile/nginx:latest   "nginx"   11m ago  Up 11m                         k8s_nginx-01.aa49fb33_nginx-id-01.default.mesos_2cf6694e-bd16-11e4-90fa-42010adf71e3_c4ef9708
811760e5070f  kubernetes/pause:go       "/pause"  11m ago  Up 11m  0.0.0.0:31000->80/tcp  k8s_POD.5c99ea13_nginx-id-01.default.mesos_2cf6694e-bd16-11e4-90fa-42010adf71e3_3d99b13e
```

###Launch a Replication Controller

Assuming your framework is running on `${KUBERNETES_MASTER}` and that you have multiple Mesos slaves in your cluster, then:

```shell
$ bin/kubectl create -f examples/controller-nginx.json
# -- or --
$ curl -L ${KUBERNETES_MASTER}/api/v1beta1/replicationControllers -XPOST -d@examples/controller-nginx.json
```

After the pod get launched, you can check it's status via `kubectl`, `curl` or your web browser:
```shell
$ bin/kubectl get replicationControllers
CONTROLLER          CONTAINER(S)        IMAGE(S)            SELECTOR            REPLICAS
nginxcontroller     nginx               dockerfile/nginx    name=nginx          2

# -- or --
$ curl -L ${KUBERNETES_MASTER}/api/v1beta1/replicationControllers
```
```json
{
  "kind": "ReplicationControllerList",
  "creationTimestamp": null,
  "selfLink": "/api/v1beta1/replicationControllers?namespace=",
  "resourceVersion": 3087,
  "apiVersion": "v1beta1",
  "items": [
    {
      "id": "nginxcontroller",
      "uid": "5f091198-bd18-11e4-90fa-42010adf71e3",
      "creationTimestamp": "2015-02-25T18:01:49Z",
      "selfLink": "/api/v1beta1/replicationControllers/nginxcontroller?namespace=default",
      "resourceVersion": 3041,
      "namespace": "default",
      "desiredState": {
        "replicas": 2,
        "replicaSelector": {
          "name": "nginx"
        },
        "podTemplate": {
          "desiredState": {
            "manifest": {
              "version": "v1beta2",
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
                  ],
                  "resources": {},
                  "terminationMessagePath": "/dev/termination-log",
                  "imagePullPolicy": "PullIfNotPresent",
                  "capabilities": {}
                }
              ],
              "restartPolicy": {
                "always": {}
              },
              "dnsPolicy": "ClusterFirst"
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
$ make test.v
```

#### Test Coverage

To generate an HTML test coverage report:

```shell
$ go get golang.org/x/tools/cmd/cover
$ make test.cover
$ go tool cover -html=all.coverage.out -o coverage-report.html
```

To view the report, open the file `coverage-report.html` in a web browser.

[1]: DEVELOP.md
[2]: https://github.com/GoogleCloudPlatform/kubernetes
[3]: http://mesos.apache.org/
[4]: http://mesosphere.io/downloads
[5]: https://github.com/tools/godep
[6]: https://github.com/coreos/etcd/releases/
[7]: DEVELOP.md#prerequisites
[8]: http://en.wikipedia.org/wiki/Classless_Inter-Domain_Routing
[9]: https://github.com/GoogleCloudPlatform/kubernetes/releases/tag/v0.5
[10]: https://github.com/GoogleCloudPlatform/kubernetes/blob/master/docs/getting-started-guides/mesos.md
[11]: https://github.com/mesosphere/kubernetes-mesos/releases
[12]: docs/README.md
[13]: docs/issues.md
[14]: https://github.com/GoogleCloudPlatform/kubernetes/releases/tag/v0.14.0
[15]: https://github.com/GoogleCloudPlatform/kubernetes/pull/6544
