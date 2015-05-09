FROM mesosphere/mesos:0.22.0-1.0.ubuntu1404
MAINTAINER Mesosphere <support@mesosphere.io>

RUN locale-gen en_US.UTF-8
RUN dpkg-reconfigure locales
ENV LANG en_US.UTF-8
ENV LC_ALL en_US.UTF-8

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y \
        wget \
        curl \
        && \
    apt-get clean

RUN mkdir /kubernetes-mesos
WORKDIR /kubernetes-mesos

COPY ./bin /kubernetes-mesos/bin
COPY ./docker/bin/* /kubernetes-mesos/bin/
ENV PATH /kubernetes-mesos/bin:$PATH

ADD ./docker/mesos-cloud.conf /opt/

RUN install-etcd.sh && which install-etcd.sh | xargs rm

EXPOSE 8888 5050 4001
ENTRYPOINT ["km-complete"]