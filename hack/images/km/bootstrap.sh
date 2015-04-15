#!/bin/sh
die() {
  test ${#} -eq 0 || echo "$@" >&2
  exit 1
}

leading_master() {
  test -n "$MESOS_MASTER" && {
    echo $MESOS_MASTER
    return
  }
  local leader=$(nslookup leader.mesos | sed -e '/^Server:/,/^Address .*$/{ d }' -e '/^$/d'|grep -e '^Address '|cut -f3 -d' ')
  test -n "$leader" || die Failed to identify mesos master, missing MESOS_MASTER variable and cannot find leader.mesos
  echo leader.mesos:5050
}

etcd_servers() {
  test -n "$ETCD_SERVER_LIST" && {
    echo $ETCD_SERVER_LIST
    return
  }

  #-- eventually we'll be able to look this up via SRV
  #local srv=_etcd-server._tcp.marathon.mesos
  #nslookup -q=SRV $srv | grep -e "^$srv"| awk '{print $7;}'| \
  #  sort | uniq | sed -e 's/^\(.*\)\.$/\1/g' | xargs echo -n | tr ' ' ',' || \
  #    die Failed to look up etcd services in marathon

  #-- for now, assume etcd is running on the mesos master, or is being proxied from there
  echo http://leader.mesos:4001
}

app="$1"
shift

mesos_master=$(leading_master) || die
apiserver_addr=${KUBE_APISERVER_ADDR:-leader.mesos}
apiserver_port=${KUBE_APISERVER_PORT:-8888}
apiserver=http://${apiserver_addr}:${apiserver_port}

case "$app" in
  apiserver)
    etcd_server_list=$(etcd_servers) || die
    exec /km "$app" \
      --address=$HOST \
      --port=$PORT_8888 \
      --mesos_master=${mesos_master} \
      --etcd_servers=${etcd_server_list} \
      --portal_net=${PORTAL_NET:-10.10.10.0/24} \
      --cloud_provider=mesos \
      "$@"
    ;;
  controller-manager)
    exec /km "$app" \
      --address=$HOST \
      --port=$PORT_10252 \
      --mesos_master=${mesos_master} \
      --master=${apiserver} \
      "$@"
    ;;
  scheduler)
    etcd_server_list=$(etcd_servers) || die
    exec /km "$app" \
      --address=$HOST \
      --port=$PORT_10251 \
      --mesos_master=${mesos_master} \
      --api_servers=${apiserver} \
      --etcd_servers=${etcd_server_list} \
      --mesos_user=${MESOS_USER:-root} \
      "$@"
    ;;
  *)
    die Unrecognized command \"$app\"
    ;;
esac
