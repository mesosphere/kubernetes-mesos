# Overview

The intent of the dockerbuilder is to leverage Docker as cleanroom build environment for this project.
Once built the Docker image contains all the libraries, headers, and tooling for compiling the binaries required to run the kubernetes-mesos framework.

## Building the Docker image

The `build` file in this directory will generate a docker image capable of building kubernetes-mesos binaries.

    $ . build

## Building the project

Once the Docker image is generated it can be used to build binaries from the current master branch.
The intent is to build the binaries in the running Docker container and then copy them back to the host when complete.
This example copies the resulting binaries into the host-mounted volume `/tmp/target`.

**SELinux:** On systems with SELinux enabled you may have to add a label to your host-mounted volume directory in order for the Docker container to write the binaries there:

    $ chcon -Rt svirt_sandbox_file_t /tmp/target

To build and copy the binaries:

    $ docker run --rm -v /tmp/target:/target k8s-mesos-builder
    ...
    git clone https://${GOPKG}.git .
    Cloning into '.'...
    test "x${GIT_BRANCH}" = "x" || git checkout "${GIT_BRANCH}"
    make "${MAKE_ARGS[@]}"
    godep restore
    go install github.com/GoogleCloudPlatform/kubernetes/cmd/proxy
    go install github.com/GoogleCloudPlatform/kubernetes/cmd/controller-manager
    env  go install ${WITH_RACE:+-race} \
              github.com/mesosphere/kubernetes-mesos/kubernetes-mesos \
              github.com/mesosphere/kubernetes-mesos/kubernetes-executor
    mkdir -p /target
    (pkg="/pkg"; pkg="${pkg%%:*}"; for x in proxy controller-manager kubernetes-mesos kubernetes-executor; do \
             /bin/cp -vpf -t /target "${pkg}"/bin/$x; done)
    '/pkg/bin/proxy' -> '/target/proxy'
    '/pkg/bin/controller-manager' -> '/target/controller-manager'
    '/pkg/bin/kubernetes-mesos' -> '/target/kubernetes-mesos'
    '/pkg/bin/kubernetes-executor' -> '/target/kubernetes-executor'

Alternatively, it can be used to generate binaries from a branch:

    $ docker run --rm -v /tmp/target:/target -e GIT_BRANCH=default_port k8s-mesos-builder

Want a quick-and-dirty development environment to start hacking?

    $ docker run -ti -v /tmp/target:/target k8s-mesos-builder bash
    root@5883c3a460a6$ make bootstrap all

Need to build the project, but from a forked git repo?

    $ docker run --rm -v /tmp/target:/target -e GIT_REPO=https://github.com/whoami/kubernetes-mesos k8s-mesos-builder

To hack in your currently checked out repo mount the root of the github repo to `/snapshot`:

    $ docker run -ti -v /tmp/target:/target -v /home/jdef/kubernetes-mesos:/snapshot k8s-mesos-builder bash

## Profiling

Profiling in the cloud with Kubernetes-Mesos is easy!
First, ssh into your Mesos cluster and generate a set of project binaries with profiling enabled (the `TAGS` variable is important here):

    $ docker run --rm -ti -e GIT_BRANCH=offer_storage -e TAGS=profile \
        -v $(pwd)/bin:/target jdef/kubernetes-mesos:dockerbuild

Next, [start the framework](https://github.com/mesosphere/kubernetes-mesos/#start-the-framework) and schedule some pods.
Once the framework and executors are up and running you can start capturing heaps:

    $ ts=$(date +'%Y%m%d%H%M%S')
    $ curl http://${servicehost}:9000/debug/pprof/heap >framework.heap.$ts
    $ for slave in 240 242 243; do
          curl http://10.132.189.${slave}:10250/debug/pprof/heap >${slave}.heap.$ts;
      done

Once you have a few heaps, you can generate reports.
Additional packages may be required to support the reporting format you desire.

    $ apt-get install ghostscript graphviz
    $ go tool pprof --base=./framework.heap.20141117175634 --inuse_objects --pdf \
        ./bin/kubernetes-executor ./framework.heap.20141120162503 >framework-20141120a.pdf

For more details regarding profiling read the [pprof](http://golang.org/pkg/net/http/pprof/) package documentation.
