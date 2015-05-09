# Running Kubernetes-Mesos In Docker

There are several ways to play with kubernetes-mesos in docker. Here are a couple that might be most useful:

1. [Complete](#complete)
  - Run components and dependencies all in the same container
  - All containers mirroring localhost ports (docker host mode)
2. [Composed](#composed)
  - Run components and dependencies each in different containers
  - Each container gets its own IP
  - Requires an ambassador to proxy ports (docker doesn't allow circular hostname linking)
  - Emulates a more complex system, useful for testing networking

While it is technically possible to run docker within a docker container, doing so requires running the mesos (slave) in priveledged mode.
Instead, it's easier to mount the docker socket (`-v "/var/run/docker.sock:/var/run/docker.sock"`) and give mesos access to the docker it's running in.

In addition to running in docker, it's also possible to [run docker locally](#local).

<a name="complete"/>
## K8SM Complete

The "complete" Dockerfile includes everything needed to run a development instance of kubernetes-mesos, including etcd and mesos.

### Daemon mode

```
docker run -d --name kubernetes-mesos -p 8888:8888 -p 5050:5050 -p 4001:4001 -v "/var/run/docker.sock:/var/run/docker.sock" mesosphere/kubernetes-mesos-complete
```

To see the logs:

```
docker logs kubernetes-mesos
```

To attach in interactive mode to a container already running in daemon mode:

```
docker exec -it kubernetes-mesos /bin/bash
```

### Interactive mode

```
docker run -it --name kubernetes-mesos -p 8888:8888 -p 5050:5050 -p 4001:4001 -v "/var/run/docker.sock:/var/run/docker.sock" --entrypoint=/bin/bash mesosphere/kubernetes-mesos-complete
```

Note: Interactive mode launches bash instead of the start script.

### Stopping

```
docker kill kubernetes-mesos
```

### Building

```
./docker/complete/build.sh
```


<a name="composed"/>
## K8SM Composed

This method requires [Docker Compose](https://docs.docker.com/compose/) to be installed, which can be done via apt-get, homebrew, or [manually](https://docs.docker.com/compose/install/).

The provided docker-compose.yml contains a self-contained configuration for running kubernetes-mesos, including its dependencies (etcd & mesos).
It will launch 5 docker containers linked together with hostnames and port forwarding.

```
# from inside the docker dir
docker-compose up
```

This will tail all the logs for each of the containers into STDOUT. `ctrl-c` to exit.

### Stopping

```
docker-compose rm
```

### Building

The docker-compose.yml file references multiple different docker images.

The one that contains just kubernetes-mesos (not mesos or etcd) can be built with the following:

```
./docker/km/build.sh
```


<a name="local"/>
## K8SM Local

Since kubernetes-mesos requires etcd, mesos, and docker, these can all be started at once or individually.

To start etcd, mesos, and kubernetes-mesos to run locally use the following command:

```
$ ./docker/bin/km-complete
```

If you already have etcd and mesos running locally use the following to start just the three kubernetes-mesos components:

```
$ ./docker/bin/km-local
```

Notes:
- The mesos slave (which runs the kubernetes-mesos executor) needs docker to be installed (or configured via tcp). By default it talks to it using the socket `/var/run/docker.sock`.
