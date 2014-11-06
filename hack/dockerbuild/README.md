# Overview

The intent of the dockerbuilder is to leverage Docker as cleanroom build environment for this project.
Once built the Docker image contains all the libraries, headers, and tooling for compiling the binaries required to run the kubernetes-mesos framework.

## Building the Docker image

The `build` file in this directory will generate a docker image capable of building kubernetes-mesos binaries.
```shell
$ . build
```

## Building the project

Once the Docker image is generated it can be used to build binaries from the current master branch.
The intent is to build the binaries in the running Docker container and then copy them back to the host when complete.
This example copies the resulting binaries into the host-mounted volume `/tmp/target`.

**SELinux:** On systems with SELinux enabled you may have to add a label to your host-mounted volume directory in order for the Docker container to write the binaries there:
```shell
$ chcon -Rt svirt_sandbox_file_t /tmp/target
```

To build and copy the binaries:
```shell
$ docker run -rm -v /tmp/target:/target k8s-mesos-builder
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
```

Alternatively, it can be used to generate binaries from a branch:
```shell
$ docker run -rm -v /tmp/target:/target -e GIT_BRANCH=default_port k8s-mesos-builder
```
