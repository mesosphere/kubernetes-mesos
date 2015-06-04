FROM golang:1.4.2
MAINTAINER Mesosphere <support@mesosphere.io>

# docker.io is suppossed to be in backports, but it's not there yet.
# https://github.com/docker/docker/issues/13253
# http://docs.docker.com/installation/debian/#debian-jessie-80-64-bit
#RUN echo "deb http://httpredir.debian.org/debian jessie-backports main" >> /etc/apt/sources.list
#RUN echo "deb http://http.debian.net/debian jessie-backports main" >> /etc/apt/sources.list

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
        jq \
        && \
    apt-get clean

RUN curl -sSL https://get.docker.com/ | sh

RUN go get github.com/tools/godep

RUN mkdir -p /go/src/github.com/GoogleCloudPlatform/kubernetes
WORKDIR /go/src/github.com/GoogleCloudPlatform/kubernetes

ENTRYPOINT [ "bash" ]
