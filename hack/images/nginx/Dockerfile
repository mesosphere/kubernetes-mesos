FROM ubuntu:14.04
MAINTAINER James DeFelice <james@mesosphere.io>

# generate static build of nginx using modified instructions from:
# https://gist.github.com/rjeczalik/7057434

# I hate running apt-get update but if I don't then the packages below
# don't install properly.
RUN apt-get update

RUN DEBIAN_FRONTEND=noninteractive apt-get -q -y install \
  make wget libxslt1-dev libxml2-dev zlib1g-dev libpcre3-dev libbz2-dev libssl-dev

WORKDIR /build
RUN wget -O - http://nginx.org/download/nginx-1.8.0.tar.gz | tar zxf -

WORKDIR nginx-1.8.0
RUN wget -O - http://www.openssl.org/source/openssl-1.0.1o.tar.gz | tar zxf -

RUN ./configure --prefix=/opt/nginx --with-cc-opt="-static -static-libgcc" \
  --with-ld-opt="-static" --with-cpu-opt=generic --with-pcre \
  --with-ipv6 --with-poll_module --with-select_module \
  --with-rtsig_module --with-select_module --with-poll_module \
  --with-http_ssl_module --with-http_spdy_module --with-http_realip_module \
  --with-http_gunzip_module \
  --with-http_gzip_static_module --with-http_auth_request_module \
  --with-http_random_index_module --with-http_secure_link_module \
  --with-http_degradation_module --with-openssl=./openssl-1.0.1o && make -j1

VOLUME /target
CMD cp -vpr /build/nginx-1.8.0/objs/nginx* /target/
