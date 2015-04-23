FROM busybox:ubuntu-14.04
MAINTAINER James DeFelice <james.defelice@gmail.com>

RUN echo 'nobody:x:99:99:Nobody:/:/sbin/false' >>/etc/passwd
RUN echo 'nobody::99:' >>/etc/group

ADD usr /usr/
ADD km /km
ADD leapsecs.dat /etc/

CMD [ ]
ENTRYPOINT [ "/bootstrap.sh" ]

ADD bootstrap.sh /bootstrap.sh
