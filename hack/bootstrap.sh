#!/bin/bash
#
# bootstrap for a baked k8sm docker container
#
set -e

test -n "$1" -a "${1##-}" = "$1" && {
  prog="$1"
  shift
} || {
  prog=kubernetes-mesos
}

prog=bin/$prog
test -x "$prog" && {
  declare -a args
  case "$prog" in
  bin/kubernetes-mesos)
    args=(
      -executor_path=${KUBE_EXECUTOR_ARTIFACT:-/app/bin/kubernetes-executor}
      -proxy_path=${KUBE_PROXY_PATH:-/app/bin/kube-proxy}
      -portal_net=${KUBE_PORTAL_NET:-10.10.10.0/24}
      -mesos_user=${FRAMEWORK_MESOS_USER:-root}
      -logtostderr=true
      -port=${APISERVER_PORT:-8888}
      -read_only_port=${APISERVER_READONLY_PORT:-7080}
    )
    test ! -n "${ETCD_SERVER_LIST}" || args+=("-etcd_servers=${ETCD_SERVER_LIST}")
    test ! -n "${MESOS_MASTER}" || args+=("-mesos_master=${MESOS_MASTER}")
    test ! -n "${BIND_ADDRESS}" || args+=("-address=${BIND_ADDRESS}")
    ;;
  bin/controller-manager)
    args=(
      -port=${CONTROLLER_MANAGER_PORT:-10252}
    )
    test ! -n "${KUBERNETES_MASTER}" || args+=("-master=${KUBERNETES_MASTER##*://}")
    ;;
  bin/kube-proxy)
    args=(
      -healthz_port=${KUBE_PROXY_PORT:-10249}
    )
    test ! -n "${BIND_ADDRESS}" || args+=("-bind_address=${BIND_ADDRESS}")
    test ! -n "${ETCD_SERVER_LIST}" || args+=("-etcd_servers=${ETCD_SERVER_LIST}")
    ;;
  esac
  set -v
  exec $prog "${args[@]}" "$@"
} || {
  echo "failed to locate program $prog" >&2
  exit 1
}
