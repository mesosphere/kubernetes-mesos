FROM golang:1.4.2-cross
MAINTAINER Mesosphere <support@mesosphere.io>

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y \
        wget \
        curl \
        g++ \
        make \
        mercurial \
        git \
        rsync \
        patch \
        && \
    apt-get clean

RUN go get github.com/tools/godep

COPY ./docker/bin/* /kubernetes-mesos/bin/
COPY ./docker/build/bin/* /kubernetes-mesos/bin/
ENV PATH /kubernetes-mesos/bin:$PATH

# required to build k8s-mesos components during k8s builds
ENV KUBERNETES_CONTRIB mesos
ENV TARGET /target

VOLUME /target
CMD [ ]

# optional: if $SNAP is host-volume-mounted into the container at run-time then
# use that as the project source code repo instead of github
ENV SNAP /snapshot
ENTRYPOINT [ "km-build" ]
