FROM busybox:ubuntu-14.04
MAINTAINER James DeFelice <james.defelice@gmail.com>

ADD ./etcd /etcd
ADD ./etcdctl /etcdctl
ADD bootstrap.sh /bootstrap.sh
ADD ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
CMD source /bootstrap.sh
