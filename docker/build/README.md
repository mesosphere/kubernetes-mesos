# Overview

The intent of kubernetes-mesos-build is to leverage Docker as cleanroom build environment for this project.
Once built the Docker image contains all the libraries, headers, and tooling for compiling the binaries required to run the kubernetes-mesos framework.

## Building the Docker image

The `build.sh` file in this directory will generate a docker image capable of building kubernetes-mesos binaries.

    $ ./build.sh

## Building the project

Once the Docker image is generated it can be used to build binaries from the current master branch.
The intent is to build the binaries in the running Docker container and then copy them back to the host when complete.
This example copies the resulting binaries into the host-mounted volume `/tmp/target`.

**SELinux:** On systems with SELinux enabled you may have to add a label to your host-mounted volume directory in order for the Docker container to write the binaries there:

    $ chcon -Rt svirt_sandbox_file_t /tmp/target

To build and copy the binaries:

    $ docker run --rm -v /tmp/target:/target mesosphere/kubernetes-mesos-build
    ...
    git clone https://${GOPKG}.git .
    Cloning into '.'...
    + test x = x
    
    "${CMD[@]}"
    + k8sm::build
    + make
    hack/build-go.sh
    +++ [0617 14:03:01] Building go targets for linux/amd64:
        ...
        contrib/mesos/cmd/k8sm-scheduler
        contrib/mesos/cmd/k8sm-executor
        contrib/mesos/cmd/k8sm-controller-manager
        contrib/mesos/cmd/km
        ...
    +++ [0617 14:04:10] Placing binaries
    + cp -vpr _output/local/go/bin/e2e.test _output/local/go/bin/genbashcomp _output/local/go/bin/genconversion _output/local/go/bin/gendeepcopy _output/local/go/bin/gendocs _output/local/go/bin/genman _output/local/go/bin/hyperkube _output/local/go/bin/integration _output/local/go/bin/k8sm-controller-manager _output/local/go/bin/k8sm-executor _output/local/go/bin/k8sm-redirfd _output/local/go/bin/k8sm-scheduler _output/local/go/bin/km _output/local/go/bin/kube-apiserver _output/local/go/bin/kube-controller-manager _output/local/go/bin/kube-proxy _output/local/go/bin/kube-scheduler _output/local/go/bin/kubectl _output/local/go/bin/kubelet _output/local/go/bin/kubernetes _output/local/go/bin/web-server /target
    ...
    '_output/local/go/bin/k8sm-controller-manager' -> '/target/k8sm-controller-manager'
    '_output/local/go/bin/k8sm-executor' -> '/target/k8sm-executor'
    '_output/local/go/bin/km' -> '/target/km'
    '_output/local/go/bin/k8sm-scheduler' -> '/target/k8sm-scheduler'
    ...

Alternatively, it can be used to generate binaries from a branch:

    $ docker run --rm -v /tmp/target:/target -e GIT_BRANCH=default_port mesosphere/kubernetes-mesos-build

Want a quick-and-dirty development environment to start hacking?

    $ docker run -ti -v /tmp/target:/target mesosphere/kubernetes-mesos-build bash
    root@5883c3a460a6$ make bootstrap all

Need to build the project, but from a forked git repo?

    $ docker run --rm -v /tmp/target:/target -e GIT_REPO=https://github.com/whoami/kubernetes-mesos \
        mesosphere/kubernetes-mesos-build

To hack in your currently checked out repo mount the root of the github repo to `/snapshot`:

    $ docker run -ti -v /tmp/target:/target -v /home/jdef/kubernetes-mesos:/snapshot \
        mesosphere/kubernetes-mesos-build bash

## Profiling

Profiling in the cloud with Kubernetes-Mesos is easy!
First, ssh into your Mesos cluster and generate a set of project binaries:

    $ docker run --rm -ti -e GIT_BRANCH=offer_storage \
        -v $(pwd)/bin:/target mesosphere/kubernetes-mesos-build

Next, [start the framework](https://github.com/mesosphere/kubernetes-mesos/#start-the-framework) with the `--profiling` flag and schedule some pods.
Once the framework and executors are up and running you can start capturing heaps:

    $ ts=$(date +'%Y%m%d%H%M%S')
    $ curl http://${servicehost}:10251/debug/pprof/heap >framework.$ts.heap
    $ for slave in 240 242 243; do
          curl http://10.132.189.${slave}:10250/debug/pprof/heap >${slave}.$ts.heap;
      done

Once you have a few heaps, you can generate reports.
Additional packages may be required to support the reporting format you desire.

    $ apt-get install ghostscript graphviz
    $ go tool pprof --base=./framework.20141117175634.heap --inuse_objects --pdf \
        ./bin/k8sm-scheduler ./framework.20141120162503.heap >framework-20141120a.pdf

For more details regarding profiling read the [pprof](http://golang.org/pkg/net/http/pprof/) package documentation.
