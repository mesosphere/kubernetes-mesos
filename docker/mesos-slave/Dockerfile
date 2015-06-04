FROM mesosphere/mesos-slave:0.22.0-1.0.ubuntu1404
MAINTAINER Mesosphere <support@mesosphere.io>

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y \
        iptables \
        && \
    apt-get clean

ENTRYPOINT [ "mesos-slave" ]