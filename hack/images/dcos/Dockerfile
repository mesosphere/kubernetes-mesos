FROM busybox:ubuntu-14.04
MAINTAINER James DeFelice <james.defelice@gmail.com>

RUN echo 'nobody:x:99:99:Nobody:/:/sbin/false' >>/etc/passwd
RUN echo 'nobody::99:' >>/etc/group

CMD [ ]
ENTRYPOINT [ "/opt/bootstrap.sh" ]

ADD usr			/usr/
ADD leapsecs.dat	/etc/
ADD km			/opt/
ADD functions.sh	/opt/
ADD bootstrap.sh	/opt/
ADD executor.sh		/opt/
