#!/bin/sh
die() {
  test ${#} -eq 0 || echo "$@" >&2
  exit 1
}

leading_master() {
  test -n "$K8SM_MESOS_MASTER" && {
    echo $K8SM_MESOS_MASTER
    return
  }
  local leader=$(nslookup leader.mesos | sed -e '/^Server:/,/^Address .*$/{ d }' -e '/^$/d'|grep -e '^Address '|cut -f3 -d' ')
  test -n "$leader" || die Failed to identify mesos master, missing K8SM_MESOS_MASTER variable and cannot find leader.mesos
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

prepare_var_run() {
  local hostpath=/var/run/kubernetes
  local run=${MESOS_SANDBOX}/run

  test -L $hostpath && rm -f $hostpath
  test -d $hostpath && rm -rf $hostpath  # should not happen, but...
  test -f $hostpath && rm -f $hostpath   # should not happen, but...
  mkdir -p $(dirname $hostpath) && mkdir -p $run && ln -s $run $hostpath && chown nobody:nobody $run
}

prepare_service_script() {
  local svcdir=$1
  local name=$2
  local script=$3
  mkdir -p ${svcdir}/${name} || die Failed to create service directory at $svcdir/$name
  cat >${svcdir}/${name}/${script}
  chmod +x ${svcdir}/${name}/${script}

  test "$script" == "run" || return 0

  # only set up logging for run service scripts
  mkdir -p $log_dir/$name
  mkdir -p ${svcdir}/${name}/log
  cat <<EOF >${svcdir}/${name}/log/run
#!/bin/sh
exec s6-log ${log_args} $log_dir/$name
EOF
  chmod +x ${svcdir}/${name}/log/run
}

log_dir=${LOG_DIR:-$MESOS_SANDBOX/log}
service_dir=${SERVICE_DIR:-$MESOS_SANDBOX/services}
mesos_master=$(leading_master) || die
etcd_server_list=$(etcd_servers) || die
logv=${GLOG_v:-0}
log_history=${LOG_HISTORY:-10}
log_size=${LOG_SIZE:-2000000}

log_args="-p -b n${log_history} s${log_size}"

# run service procs as "nobody"
apply_uids="s6-applyuidgid -u 99 -g 99"

#
# create services directories and scripts
#
mkdir -p ${log_dir}
prepare_var_run || die Failed to initialize apiserver run directory

prepare_service_script ${service_dir} .s6-svscan finish <<EOF
#!/bin/sh
  local hostpath=/var/run/kubernetes
  test -L $hostpath && rm -f $hostpath
EOF

prepare_service_script ${service_dir} apiserver run <<EOF
#!/bin/sh
exec $apply_uids /km apiserver \\
  --address=$HOST \\
  --port=$PORT_8888 \\
  --mesos_master=${mesos_master} \\
  --etcd_servers=${etcd_server_list} \\
  --portal_net=${PORTAL_NET:-10.10.10.0/24} \\
  --cloud_provider=mesos \\
  --v=${APISERVER_GLOG_v:-${logv}} \\
  2>&1
EOF

prepare_service_script ${service_dir} controller-manager run <<EOF
#!/bin/sh
exec $apply_uids /km controller-manager \\
  --address=$HOST \\
  --port=$PORT_10252 \\
  --mesos_master=${mesos_master} \\
  --master=http://$HOST:$PORT_8888 \\
  --v=${CONTROLLER_MANAGER_GLOG_v:-${logv}} \\
  2>&1
EOF

prepare_service_script ${service_dir} scheduler run <<EOF
#!/bin/sh
exec $apply_uids /km scheduler \\
  --address=$HOST \\
  --port=$PORT_10251 \\
  --mesos_master=${mesos_master} \\
  --api_servers=http://$HOST:$PORT_8888 \\
  --etcd_servers=${etcd_server_list} \\
  --mesos_user=${K8SM_MESOS_USER:-root} \\
  --v=${SCHEDULER_GLOG_v:-${logv}} \\
  2>&1
EOF

exec /usr/bin/s6-svscan -t${S6_RESCAN:-500000} ${service_dir}
