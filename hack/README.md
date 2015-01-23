## Hacking on k8sm

This document needs a lot of work.
For now it serves as a very rough guide for bootstrapping a kubernetes-mesos cluster.
It has seen *very* limited testing.
You have been warned.

### Quick and Dirty

The process below should get everything up and running in docker containers.
Recommended to run through this on your Mesos master node (for now).
At the end, the following containers should be running, all sharing the host's network namespace:

* etcd
* kubernetes API server & scheduler
* kubernetes replication controller
* kubernetes service proxy

This worked for me on a GCE cluster, spun up from [Mesosphere's GCE tooling][1].

```shell
:; git clone https://github.com/mesosphere/kubernetes-mesos.git k8sm
:; cd k8sm && git checkout dcos_demo

## assumes that Mesos was installed to /usr/local. If it's somewhere else then
## you can specify, after 'bake', WITH_MESOS_DIR=/your/special/mesos/distro/prefix
:; make dockerbuild bake

:; export servicehost=$(hostname -i|tr ' ' '\n'|head -n1)
:; sudo docker run -d --net=host coreos/etcd go-wrapper run \
   -advertise-client-urls=http://${servicehost}:4001 \
   -listen-client-urls=http://${servicehost}:4001 \
   -initial-advertise-peer-urls=http://${servicehost}:7001 \
   -listen-peer-urls=http://${servicehost}:7001

:; ./hack/kube up

:; sudo docker ps
CONTAINER ID  IMAGE                              COMMAND            CREATED  STATUS  NAMES
fb7741160735  mesosphere/k8sm:latest-proxy       "/bootstrap.sh ku  27m ago  Up 27m  kube-proxy
03c374dbcb83  mesosphere/k8sm:latest-controller  "/bootstrap.sh co  27m ago  Up 27m  k8sm-controller-manager
713ce321b533  mesosphere/k8sm:latest-master      "/bootstrap.sh ku  27m ago  Up 27m  k8sm-scheduler
475024dedd5b  coreos/etcd:latest                 "/etcd go-wrapper  27m ago  Up 27m  happy_goldstine

## ... and then you can start spinning up the guestbook
:; ./hack/kube config -c examples/guestbook/redis-master.json create pods
:; ./hack/kube config -c examples/guestbook/redis-master-service.json create services
:; ./hack/kube config -c examples/guestbook/redis-slave-controller.json create replicationControllers
:; ./hack/kube config -c examples/guestbook/redis-slave-service.json create services
:; ./hack/kube config -c examples/guestbook/frontend-controller.json create replicationControllers
:; cat <<EOS >/tmp/frontend-service
{
  "id": "frontend",
  "kind": "Service",
  "apiVersion": "v1beta1",
  "port": 9998,
  "selector": {
    "name": "frontend"
  },
  "publicIPs": [
    "${servicehost}"
  ]
}
EOS

:; ./hack/kube config -c /tmp/frontend-service create services
:; ./hack/kube config list pods
:; ./hack/kube config list services

```

[1]: https://google.mesosphere.com/
