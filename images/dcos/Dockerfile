FROM alpine:3.2
MAINTAINER Mesosphere <support@mesosphere.com>

RUN apk --update add nginx curl ca-certificates && \
    curl -Ls https://circle-artifacts.com/gh/andyshinn/alpine-pkg-glibc/6/artifacts/0/home/ubuntu/alpine-pkg-glibc/packages/x86_64/glibc-2.21-r2.apk > /tmp/glibc-2.21-r2.apk && \
    apk add --allow-untrusted /tmp/glibc-2.21-r2.apk && \
    rm -rf /tmp/glibc-2.21-r2.apk /var/cache/apk/*

ENV S6_VERSION v1.13.0.0
RUN curl -L "https://github.com/just-containers/s6-overlay/releases/download/$S6_VERSION/s6-overlay-amd64.tar.gz" | tar xzvf - -C /

# fix nginx tmp directory permissions to be writable by nginx user
RUN mkdir -p /var/lib/nginx/tmp && chown nginx /var/lib/nginx /var/lib/nginx/tmp && chmod 0700 /var/lib/nginx /var/lib/nginx/tmp

# fix /var/run/kubernetes permissions such that the apisever can create
# self-signed certificates
RUN mkdir -p /var/run/kubernetes && chown nobody /var/run/kubernetes && chmod 0700 /var/run/kubernetes

ADD root /

ADD _build/etcd                /opt/
ADD _build/etcdctl             /opt/
ADD _build/skydns-rc.yaml.in   /opt/
ADD _build/skydns-svc.yaml.in  /opt/
ADD _build/kube-ui-rc.yaml     /opt/
ADD _build/kube-ui-svc.yaml    /opt/
ADD _build/.version            /opt/
ADD _build/linux/km            /opt/
ADD _build/linux/kubectl       /opt/
ADD _build/linux/kubectl.bz2   /opt/static/linux/
ADD _build/darwin/kubectl.bz2  /opt/static/darwin/
ADD _build/windows/kubectl.exe.bz2 /opt/static/windows/
ADD _build/kubectl-meta.json   /opt/static/
ADD bootstrap.sh               /opt/

CMD ["/opt/bootstrap.sh"]
