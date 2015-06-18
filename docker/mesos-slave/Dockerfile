FROM mesosphere/mesos-slave:0.22.0-1.0.ubuntu1404
MAINTAINER Mesosphere <support@mesosphere.io>

RUN apt-get update -qq && \
    apt-get install -qqy \
        apt-transport-https \
        ca-certificates \
        curl \
        lxc \
        iptables \
        && \
    apt-get clean

COPY ./resolveip /usr/local/bin/resolveip
RUN chmod +x /usr/local/bin/resolveip

ENTRYPOINT ["mesos-slave"]
