FROM debian:wheezy
MAINTAINER James DeFelice <james.defelice@gmail.com>

ENV DEBIAN_FRONTEND noninteractive

RUN apt-get -y update && \
    apt-get -y install make curl mercurial git && \
    apt-get -y clean

RUN cd /usr/local && curl -L https://storage.googleapis.com/golang/go1.4.2.linux-amd64.tar.gz | tar xvz
ENV PATH /usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

ENV GOPATH /opt
RUN mkdir -pv /opt && cd /opt && go get github.com/tools/godep && ln -sv /opt/bin/godep /usr/local/bin/godep

ENV GOPATH /pkg
VOLUME /target
CMD [ ]

# optional: if $SNAP is host-volume-mounted into the container at run-time then
# use that as the project source code repo instead of github
ENV SNAP /snapshot
ADD run.sh /
ENTRYPOINT [ "/run.sh" ]
