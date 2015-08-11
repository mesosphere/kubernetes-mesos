## Overview

This package contains scripts to build the DCOS Kubernetes docker container image.

![Kubernetes-Mesos on DCOS](images/dcos_k8sm.png)

## Usage

```shell
$ make clean
$ make build
$ make push
```

If you source directory is not clean, this will be reflected in the Docker tag with "dirty" and "unclean".

The `make build` will store the docker image name for a following `make push`. Hence, don't worry about passing all variables again if you customize your build as described above. Moreover, `make push` will ask you whether you really want to push the given docker image.

Optionally the version might be specified:

```shell
$ GIT_REF=v1.0.5-v0.6.1 make build
$ GIT_REF=v1.0.5-v0.6.1 make push
```

Moreover, there are the following variables can be customized via the command line:

```make
GIT_URL ?= https://github.com/mesosphere/kubernetes
DOCKER_ORG ?= mesosphere
KUBE_ROOT ?=
SUDO ?=
POSTFIX ?= -alpha
```

If you are on Linux, build the dockerized kubernetes binaries most probably results in file being owned by root. Set `SUDO=sudo` to get them deleted as normal user:

```bash
$ make clean SUDO=sudo
```

With `KUBE_ROOT` it is possible to build from a local kubernetes checkout:

```bash
$ make KUBE_ROOT=~/src/kubernetes
```

## Uploading Github Release Assets

The Makefile can build kubectl tar.gz files for Linux and OSX, and upload those as Github assets to a release:

```bash
make github-release-assets GITHUB_TOKEN=<github-developer-token>
```

## Development and Testing

The Docker container can be run manually outside of a DCOS cluster, e.g.:

```
docker run --rm --name=k8s -e GLOG_v=3 -e HOST=<docker-host-IP> \
  -e K8SM_MESOS_MASTER=<mesos-master-ip>:5050 \
  -e DEFAULT_DNS_NAME=<docker-host-IP> \
  -e MESOS_SANDBOX=/tmp \
  --net=host -it mesosphere/kubernetes:k8s-1.0.pre-k8sm-0.6-dcos-dev
```

## Files
* Makefile - coordinate the build process: build s6, bundle it with km into a Docker and push the repo
* Dockerfile - recipe for building the docker
* bootstrap.sh - default target of Dockerfile, launches the framework: apiserver, scheduler, controller
* leapsecs.dat - requirement for s6 to work properly

## Walkthrough w/ the guestbook examples

Some prerequisites:

- set up a DCOS cluster
- install the `kubernetes` app using the DCOS CLI tooling
  - `dcos package install kubernetes`

- build yourself a kubectl using the Kubernetes build process (`make`)
- wait until kubernetes is up

```shell
$ export KUBERNETES_MASTER=http://{the-dcos-host-your-kubernetes-master-is-running-on}/service/kubernetes/api
$ kubectl config set-cluster dcos --server=$KUBERNETES_MASTER
$ kubectl config set-context dcos
$ kubectl config use-context dcos

$ kubectl get pods --all-namespaces
NAMESPACE     NAME                READY     STATUS    RESTARTS   AGE
kube-system   kube-dns-v8-vruvs   4/4       Running   0          22h
kube-system   kube-ui-v1-1r5km    1/1       Running   0          22h

$ kubectl create -f kubernetes/examples/guestbook/redis-master.json 
pods/redis-master-2
$ kubectl create -f kubernetes/examples/guestbook/redis-master-service.json 
services/redismaster
$ kubectl create -f kubernetes/examples/guestbook/redis-slave-controller.json 
replicationControllers/redis-slave-controller
$ kubectl create -f kubernetes/examples/guestbook/redis-slave-service.json 
services/redisslave
$ kubectl create -f kubernetes/examples/guestbook/frontend-controller.json 
replicationControllers/frontend-controller
$ kubectl create -f kubernetes/examples/guestbook/frontend-service.json 
services/frontend

$ kubectl get pods
POD                        IP          CONTAINER(S)  IMAGE(S)        HOST                             LABELS         STATUS   CREATED
frontend-controller-jf2zt  172.17.0.8  php-redis     jdef/php-redis  ec2-52-24-192-202.../10.0.0.221  name=frontend  Running  51s
frontend-controller-kypr9  172.17.0.6  php-redis     jdef/php-redis  ec2-52-24-218-42.../10.0.0.223   name=frontend  Running  51s
...

$ kubectl exec -p frontend-controller-jf2zt -c php-redis -ti curl http://frontend
<html ng-app="redis">
  <head>
    <title>Guestbook</title>
...
```
