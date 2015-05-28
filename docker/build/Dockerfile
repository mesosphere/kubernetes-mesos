FROM golang:1.4.2-wheezy
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

COPY ./docker/bin/* /kubernetes-mesos/bin/
COPY ./docker/build/bin/* /kubernetes-mesos/bin/
ENV PATH /kubernetes-mesos/bin:$PATH

RUN go get github.com/tools/godep

VOLUME /target
CMD [ ]

# optional: if $SNAP is host-volume-mounted into the container at run-time then
# use that as the project source code repo instead of github
ENV SNAP /snapshot
ENTRYPOINT [ "km-build" ]
