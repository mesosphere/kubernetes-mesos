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

api_server() {
  test -n "$KUBERNETES_MASTER" && {
    echo $KUBERNETES_MASTER
    return
  }
  local apiserver_addr=${KUBE_APISERVER_ADDR:-leader.mesos}
  local apiserver_port=${KUBE_APISERVER_PORT:-8888}
  echo http://${apiserver_addr}:${apiserver_port}
}

service_dir=${SERVICE_DIR:-$MESOS_SANDBOX/services}
mesos_master=$(leading_master) || die
etcd_server_list=$(etcd_servers) || die
apiserver=$(api_server) || die
logv=${GLOG_v:-0}

#
# create services directories and scripts
#
mkdir -p ${service_dir}/apiserver
mkdir -p ${service_dir}/controller-manager
mkdir -p ${service_dir}/scheduler

cat <<EOF >${service_dir}/apiserver/run
#!/bin/sh
exec /km apiserver \\
      --address=$HOST \\
      --port=$PORT_8888 \\
      --mesos_master=${mesos_master} \\
      --etcd_servers=${etcd_server_list} \\
      --portal_net=${PORTAL_NET:-10.10.10.0/24} \\
      --cloud_provider=mesos \\
      --v=$logv >$MESOS_SANDBOX/apiserver.log 2>&1
EOF

cat <<EOF >${service_dir}/controller-manager/run
#!/bin/sh
exec /km controller-manager \\
      --address=$HOST \\
      --port=$PORT_10252 \\
      --mesos_master=${mesos_master} \\
      --master=${apiserver} \\
      --v=$logv >$MESOS_SANDBOX/controller-manager.log 2>&1
EOF

cat <<EOF >${service_dir}/scheduler/run
#!/bin/sh
exec /km scheduler \\
      --address=$HOST \\
      --port=$PORT_10251 \\
      --mesos_master=${mesos_master} \\
      --api_servers=${apiserver} \\
      --etcd_servers=${etcd_server_list} \\
      --mesos_user=${MESOS_USER:-root} \\
      --v=$logv >$MESOS_SANDBOX/scheduler.log 2>&1
EOF

chmod +x ${service_dir}/apiserver/run
chmod +x ${service_dir}/controller-manager/run
chmod +x ${service_dir}/scheduler/run
exec /usr/bin/s6-svscan -t${S6_RESCAN:-5000000} ${service_dir}
