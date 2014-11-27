FROM jdef/kubernetes-mesos:build-base
MAINTAINER James DeFelice <james.defelice@gmail.com>

ENV DEB_VERSION_MESOS 0.20.1-1.0.debian75

RUN apt-key adv --keyserver keyserver.ubuntu.com --recv E56151BF && \
    echo "deb http://repos.mesosphere.io/debian wheezy main" | \
          tee /etc/apt/sources.list.d/mesosphere.list && \
    apt-get -y update && \
    apt-get -y install mesos=$DEB_VERSION_MESOS
