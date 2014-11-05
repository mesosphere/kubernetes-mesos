FROM debian:wheezy
MAINTAINER James DeFelice <james.defelice@gmail.com>

ENV DEBIAN_FRONTEND noninteractive
ENV DEB_VERSION_MESOS 0.20.1-1.0.debian75

RUN apt-key adv --keyserver keyserver.ubuntu.com --recv E56151BF && \
    echo "deb http://repos.mesosphere.io/debian wheezy main" | \
          tee /etc/apt/sources.list.d/mesosphere.list && \
    apt-get -y update 

RUN apt-get -y install mesos=$DEB_VERSION_MESOS

ENTRYPOINT bash -il

## RUN echo "deb-src http://http.debian.net/debian wheezy main" >>/etc/apt/sources.list && \
##     echo "deb-src http://http.debian.net/debian wheezy-updates main" >>/etc/apt/sources.list && \
##     echo "deb-src http://security.debian.org wheezy/updates main" >>/etc/apt/sources.list && \
##     apt-get update

## RUN apt-get -y install devscripts build-essential && apt-get -y build-dep golang-go
## ADD go1.3-install-deb.sh /
## RUN apt-get -y install curl mercurial git && chmod +x /go1.3-install-deb.sh && /go1.3-install-deb.sh

RUN apt-get -y install g++ make curl mercurial git
RUN curl -L https://protobuf.googlecode.com/files/protobuf-2.5.0.tar.gz | tar xz && cd protobuf-2.5.0/ && ./configure --prefix=/usr && make && make install

RUN cd /usr/local && curl -L https://storage.googleapis.com/golang/go1.3.3.linux-amd64.tar.gz | tar xvz

ENV PATH /usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV GOPATH /opt
RUN mkdir -pv /opt && cd /opt && go get github.com/tools/godep && ln -sv /opt/bin/godep /usr/bin/godep

ENV GOPATH /pkg
ENTRYPOINT mkdir -pv /pkg/src/github.com/mesosphere/kubernetes-mesos && \
    git clone https://github.com/mesosphere/kubernetes-mesos.git /pkg/src/github.com/mesosphere/kubernetes-mesos && \
    cd /pkg/src/github.com/mesosphere/kubernetes-mesos && make bootstrap install
