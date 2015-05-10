## Overview

This package contains scripts to build the DCOS Kubernetes docker container image.

## DNS Support

Kubernetes supports an add-on called kube-dns.
kube-dns is intended to resolve Kubernetes service names, such as `frontend.default.kubernetes.local`, that map to internal Kubernetes service portal IP addresses.
These service portal IP addresses are managed by the Kubernetes cluster and traffic is routed to them via special iptables DNAT rules that are maintained by the k8s proxy.
Service portal IP addresses are not intended to be referenced by anything other than other k8s pods/services (read: no other mesos client or task should reference them).

Deploying the kube-dns addon is optional (defaults to off) and it can be enabled by setting the 'enable-dns' package option (see the walkthrough example below).
There is no funny interaction between kube-dns and mesos-dns;
mesos-dns servers may be used as fallbacks for name resolution since their IP addresses are present in the slave's `/etc/resolv.conf`.

## Usage
```
## build and push the docker image
:; make push TARGET=$HOME/bin/km
```
## Files
* Makefile - coordinate the build process: build s6, bundle it with km into a Docker and push the repo
* Dockerfile - recipe for building the docker
* bootstrap.sh - default target of Dockerfile, launches the framework: apiserver, scheduler, controller
* executor.sh - wrapper script that runs the executor on a slave host
* leapsecs.dat - requirement for s6 to work properly
* webhook.sh - helper that can communicate with simple HTTP webhook endpoints

## TODO
- [x] process supervision for the executor is broken, dandling procs end up as children of slave
- [x] makes a big assumption about the location of etcd, need a better story here
  - support multiple deployment scenarios for etcd
    - case 1: etcd cluster is deployed elsewhere and k8sm will tap into it
    - case 2: etcd-server is deployed in the k8sm container, dies with the k8sm framework
      - this can leave lingering exec's mapped to a framework that is not recognized if k8sm is redeployed
      - [x] etcd needs to start before everything else otherwise bad things happen (apiserver!)
- [x] s6 and k8s require files to exist in certain places on the host. this is terrible practice
  - mentioned this article to elizabeth, she has similar concerns with the HDFS framework
    - http://www.ibm.com/developerworks/library/l-mount-namespaces/
- [x] additional ports (e.g. 6443/apiserver) should be configurable
- [ ] support secure (TLS) apiserver

## Walkthrough w/ the guestbook examples

Some prerequisites:

- set up a DCOS cluster
- install the `kubernetes` app using the DCOS CLI tooling
  - `dcos package install kubernetes`
  - alternatively, if you want to use the kube-dns add-on:
    - `echo '{ "kubernetes": { "enable-dns": "1" } }' >/tmp/options`
    - `dcos package install --options=/tmp/options kubernetes`

- build yourself a kubectl, either using the Kubernetes or Kubernetes-Mesos build process

```shell
$ export KUBERNETES_MASTER=http://{the-dcos-host-your-kubernetes-master-is-running-on}:25502
$ bin/kubectl create -f ../../../examples/guestbook/redis-master.json 
pods/redis-master-2
$ bin/kubectl create -f ../../../examples/guestbook/redis-master-service.json 
services/redismaster
$ bin/kubectl create -f ../../../examples/guestbook/redis-slave-controller.json 
replicationControllers/redis-slave-controller
$ bin/kubectl create -f ../../../examples/guestbook/redis-slave-service.json 
services/redisslave
$ bin/kubectl create -f ../../../examples/guestbook/frontend-controller.json 
replicationControllers/frontend-controller
$ bin/kubectl create -f ../../../examples/guestbook/frontend-service.json 
services/frontend

$ bin/kubectl get pods
POD                        IP          CONTAINER(S)  IMAGE(S)        HOST                             LABELS         STATUS   CREATED
frontend-controller-jf2zt  172.17.0.8  php-redis     jdef/php-redis  ec2-52-24-192-202.../10.0.0.221  name=frontend  Running  51s
frontend-controller-kypr9  172.17.0.6  php-redis     jdef/php-redis  ec2-52-24-218-42.../10.0.0.223   name=frontend  Running  51s
...

$ bin/kubectl exec -p frontend-controller-jf2zt -c php-redis -ti curl http://frontend
<html ng-app="redis">
  <head>
    <title>Guestbook</title>
...
```
