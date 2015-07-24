FROM busybox:ubuntu-14.04
MAINTAINER Mesosphere <support@mesosphere.com>

ADD https://github.com/just-containers/s6-overlay/releases/download/v1.13.0.0/s6-overlay-amd64.tar.gz /tmp/
RUN tar xzf /tmp/s6-overlay-amd64.tar.gz -C / && rm /tmp/s6-overlay-amd64.tar.gz

RUN echo 'nobody:x:99:99:Nobody:/:/sbin/false' >>/etc/passwd
RUN echo 'nobody::99:' >>/etc/group

CMD [ ]
ENTRYPOINT [ "/opt/bootstrap.sh" ]

ADD leapsecs.dat	/etc/
ADD etcd		/opt/
ADD etcdctl		/opt/
ADD km			/opt/
ADD kubectl		/opt/
ADD skydns-rc.yaml.in	/opt/
ADD skydns-svc.yaml.in	/opt/
ADD kube-ui-rc.yaml	/opt/
ADD kube-ui-svc.yaml	/opt/
ADD functions.sh	/opt/
ADD bootstrap.sh	/opt/
ADD .version		/opt/
