FROM busybox:ubuntu-14.04
MAINTAINER Mesosphere <support@mesosphere.com>

ADD _build/s6-overlay-amd64.tar.gz /

RUN echo 'nobody:x:99:99:Nobody:/:/sbin/false' >>/etc/passwd
RUN echo 'nobody::99:' >>/etc/group

ADD leapsecs.dat              /etc/
ADD _build/etcd               /opt/
ADD _build/etcdctl            /opt/
ADD _build/skydns-rc.yaml.in  /opt/
ADD _build/skydns-svc.yaml.in /opt/
ADD _build/kube-ui-rc.yaml    /opt/
ADD _build/kube-ui-svc.yaml   /opt/
ADD _build/.version           /opt/
ADD _build/km                 /opt/
ADD _build/kubectl            /opt/

ADD functions.sh              /opt/
ADD bootstrap.sh              /opt/

CMD ["/opt/bootstrap.sh"]
