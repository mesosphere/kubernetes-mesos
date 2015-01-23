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

:; make dockerbuild bake

:; sudo docker run -d --net=host coreos/etcd go-wrapper run \
   -advertise-client-urls=http://${servicehost}:4001 \
   -listen-client-urls=http://${servicehost}:4001 \
   -initial-advertise-peer-urls=http://${servicehost}:7001 \
   -listen-peer-urls=http://${servicehost}:7001

:; ./hack/kube up

:; sudo docker ps

## ... and then you can start spinning up the guestbook
:; ./hack/kube config -c examples/guestbook/redis-master.json create pods
:; ./hack/kube config -c examples/guestbook/redis-master-service.json create services
:; ./hack/kube list pods
:; ./hack/kube list services

```

[1]: https://google.mesosphere.com/
