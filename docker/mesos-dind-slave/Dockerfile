FROM mesosphere/mesos-slave:0.22.0-1.0.ubuntu1404
MAINTAINER Mesosphere <support@mesosphere.io>

RUN apt-get update -qq && \
    apt-get install -qqy \
        apt-transport-https \
        ca-certificates \
        curl \
        lxc \
        iptables \
        ipcalc \
        && \
    apt-get clean

# Install Docker from Docker Inc. repositories.
RUN curl -sSL https://get.docker.com/ubuntu/ | sh

# Install the magic wrapper.
ADD ./bin/wrapdocker /usr/local/bin/wrapdocker
RUN chmod +x /usr/local/bin/wrapdocker

# Define additional metadata for our image.
VOLUME /var/lib/docker
ENTRYPOINT ["wrapdocker"]
