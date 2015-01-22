#!/bin/bash

set -e

function die() {
  echo "$*" 1>&2
  exit 1
}

# Render a list of dependencies for the spec'd list of binaries
# Output is as follows:
#     bin:path-to-real-binary-that-was-exactly-where-you-said
#     lib:path-to-library dependency
#     sym:the-binary-name-you-specified:the-real-path-to-the-binary
function k8sm::libs_for() {
  local prog
  for prog in "$@"; do
    test -f "$prog" -a ! -L "$prog" && {
      echo "bin:$prog"
    } || {
      local x=$(which "$prog") || die "failed to locate $prog"
      x=$(readlink -f "$x") || die "failed to follow symlink $x"
      echo "sym:$prog:$x" # because it's not where we expected it would be
      prog="$x"
    }
    ldd "$prog" |sed -e 's~^[^/]\+\( => \)\?/\([^ ]\+\) .*~/\2~g' -e '/^[^/]/d'|sed -e 's/^/lib:/g'
  done
}

# build a tarball for Docker context
function k8sm::context() {
  local -a args
  local sandbox=/tmp/context-$RANDOM path
  mkdir -pv $sandbox/app/{bin,sbin} >&2 && trap "/bin/rm -rfv $sandbox >&2" EXIT RETURN
  while IFS=: read -r -a line; do
  case ${line[0]} in
  bin)
    local short="${line[1]}" # could be /bin/bash or bash
    if [ "${short##/}" != "${short}" ]; then
      # short starts with / so try to honor the original location
      path=$(dirname "$short")
      test -d "$sandbox/$path" || mkdir -pv "$sandbox/$path" >&2
      /bin/cp -vf "$short" $sandbox/$path >&2
    else
      # assuming no intermediate directories here
      /bin/cp -vf "$short" $sandbox/app/bin >&2
    fi
    ;;
  lib)
    path=$(dirname "${line[1]}")
    test -d "$sandbox/$path" || mkdir -pv "$sandbox/$path" >&2
    /bin/cp -vf "$(readlink -f "${line[1]}")" "$sandbox/$path/$(basename "${line[1]}")" >&2
    ;;
  sym)
    local short="${line[1]}" # could be /bin/bash or bash
    path=$(dirname "${line[2]}")
    test -d "$sandbox/$path" || mkdir -pv "$sandbox/$path" >&2
    /bin/cp -vf "${line[2]}" "$sandbox/$path/" >&2

    if [ "${short##/}" != "${short}" ]; then
      # short starts with / so try to honor the original location
      path=$(dirname "$short")
      test -d "$sandbox/$path" || mkdir -pv "$sandbox/$path" >&2
      ln -sv "${line[2]}" "$sandbox/$short" >&2
    else
      # assuming that you were lazy and specified something like "iptables"
      # and not a relative path like "123/456" that is really a symlink that
      # points to the actual binary
      ln -sv "${line[2]}" "$sandbox/app/sbin/$short" >&2
    fi
    ;;
  *)
    die unexpected type ${line[0]} in context
    ;;
  esac
  done

  ls -laF $sandbox/app/sbin >&2
  find $sandbox >&2

  tar -c -C $sandbox --owner=0 --group=0 . /etc/ld.so.conf /etc/ld.so.conf.d -C $(readlink -f "$(dirname "$0")") bootstrap.sh
}

binary_image=mesosphere/k8sm:${k8sm_version:-latest}

{ k8sm::libs_for "$@" ; ls /lib/*/libnss* /lib/xtables/lib* | sed -e 's/^/lib:/g' ; } \
  | sort | uniq | k8sm::context | sudo docker import - ${binary_image}

#
# make base binary image
#
sudo docker build -t $binary_image - <<EOF
FROM $binary_image
MAINTAINER James DeFelice <james.defelice@gmail.com>
ENV PATH=/app/bin:/app/sbin:/usr/sbin:/usr/bin:/sbin:/bin
ENV LD_DEBUG=files
ENV LD_LIBRARY_PATH=/usr/local/lib
CMD [ ]
WORKDIR /app
ENTRYPOINT [ "/bootstrap.sh" ]
EOF

#
# make the image that runs the kubernetes-mesos master
#
# you can set environment variables before invoking this script
# to customize the generated docker image:
#
#     APISERVER_READONLY_PORT
#     APISERVER_PORT
#     EXECUTOR_ARTIFACTS_PORT
#     SCHEDULER_LIBPROCESS_PORT
#
kubernetes_mesos_image=${binary_image}-master
sudo docker build -t $kubernetes_mesos_image - <<EOF
FROM $binary_image
MAINTAINER James DeFelice <james.defelice@gmail.com>
ENV APISERVER_READONLY_PORT ${APISERVER_READONLY_PORT:-7080}
ENV APISERVER_PORT ${APISERVER_PORT:-8888}
ENV ARTIFACTS_PORT ${EXECUTOR_ARTIFACTS_PORT:-9000}
ENV LIBPROCESS_PORT ${SCHEDULER_LIBPROCESS_PORT:-9876}
EXPOSE \$APISERVER_READONLY_PORT \$APISERVER_PORT \$ARTIFACTS_PORT \$LIBPROCESS_PORT
EOF
