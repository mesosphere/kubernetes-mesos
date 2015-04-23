#!/bin/sh
die() {
  test ${#} -eq 0 || echo "$@" >&2
  exit 1
}

leading_master_ip() {
  test -n "$K8SM_MESOS_MASTER" && {
    echo $K8SM_MESOS_MASTER
    return
  }
  local leader=$(nslookup leader.mesos | sed -e '/^Server:/,/^Address .*$/{ d }' -e '/^$/d'|grep -e '^Address '|cut -f3 -d' ')
  test -n "$leader" || die Failed to identify mesos master, missing K8SM_MESOS_MASTER variable and cannot find leader.mesos
  echo leader.mesos
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

  s6-mkfifodir -f ${svcdir}/${name}/event

  # only set up logging for run service scripts
  mkdir -p $log_dir/$name
  mkdir -p ${svcdir}/${name}/log
  cat <<EOF >${svcdir}/${name}/log/run
#!/usr/bin/execlineb
s6-log ${log_args} $log_dir/$name
EOF
  chmod +x ${svcdir}/${name}/log/run

  local loglink=log/$name/current
  ln -sv $loglink ${MESOS_SANDBOX}/${name}.log

  echo prepared script $script for service $name in dir $svcdir
}

prepare_monitor_script() {
  local mondir=$1
  local svcdir=$2
  local name=$3

  prepare_service_script ${mondir} ${name}-monitor run <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
foreground {
  s6-notifywhenup s6-ftrig-listen1 ${svcdir}/${name}/event u
    echo waiting for ${name} service startup
}
foreground {
  echo ${name} service started
}
loopwhilex
  foreground {
    s6-ftrig-listen1 ${svcdir}/${name}/event u
      echo waiting for ${name} service restart
  }
  echo ${name} service restarted
EOF
}

prepare_service() {
  local mond="$1"
  local svcd="$2"
  local name="$3"
  local respawnto="$4"

  cat | prepare_service_script ${svcd} ${name} run
  prepare_monitor_script ${mond} ${svcd} ${name}
  prepare_service_script ${svcd} ${name} finish <<EOF
#!/usr/bin/execlineb -S2
foreground {
  if { test "\${1}" != "256" }
    foreground {
      backtick -n ts { date "+%m%d %H:%M:%S.999999" }
      import ts printf "I%s %7d respawn.xx:0] sleeping ${respawnto}s before respawning ${name}\\n" "\${ts}" "\${2}"
    } sleep ${respawnto}
}
exit 0
EOF
}

log_dir=${LOG_DIR:-$MESOS_SANDBOX/log}
service_dir=${SERVICE_DIR:-$MESOS_SANDBOX/service.d}
monitor_dir=${MONITOR_DIR:-$MESOS_SANDBOX/monitor.d}

mesos_leader=$(leading_master_ip) || die
mesos_master=${mesos_leader}:5050

# assume that the leading mesos master is always running a marathon
# service proxy, perhaps using haproxy.
service_proxy=${SERVICE_PROXY:-${mesos_leader}}

# would be nice if this was auto-discoverable. if this value changes
# between launches of the framework, there can be dangling executors,
# so it is important that this point to some frontend load balancer
# of some sort, addressed by a fixed domain name or else a static IP.
etcd_server_list=${ETCD_SERVER_LIST:-http://${service_proxy}:4001}

# would be nice if this was auto-discoverable. if this value changes
# between launches of the framework, there can be dangling executors,
# so it is important that this point to some frontend load balancer
# of some sort, addressed by a fixed domain name or else a static IP.
api_server=${KUBERNETES_MASTER:-http://${service_proxy}:8888}

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
#!/usr/bin/execlineb
  define hostpath /var/run/kubernetes
  foreground { if { test -L ${hostpath} } rm -f ${hostpath} } exit 0
EOF

#
# apiserver, uses frontend service proxy to connect with etcd
#
prepare_service ${monitor_dir} ${service_dir} apiserver ${APISERVER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
$apply_uids
/km apiserver
  --address=$HOST
  --port=$PORT_8888
  --mesos_master=${mesos_master}
  --etcd_servers=${etcd_server_list}
  --portal_net=${PORTAL_NET:-10.10.10.0/24}
  --cloud_provider=mesos
  --v=${APISERVER_GLOG_v:-${logv}}
EOF

#
# controller-manager, doesn't need to use frontend proxy to access
# apiserver like the scheduler, it can access it directly here.
#
prepare_service ${monitor_dir} ${service_dir} controller-manager ${CONTROLLER_MANAGER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
$apply_uids
/km controller-manager
  --address=$HOST
  --port=$PORT_10252
  --mesos_master=${mesos_master}
  --master=http://$HOST:$PORT_8888
  --v=${CONTROLLER_MANAGER_GLOG_v:-${logv}}
EOF

#
# scheduler, uses frontend service proxy to access apiserver and
# etcd. it spawns executors configured with the same address for
# --api_servers and if the IPs change (because this container changes
# hosts) then the executors become zombies.
#
prepare_service ${monitor_dir} ${service_dir} scheduler ${SCHEDULER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
$apply_uids
/km scheduler
  --address=$HOST
  --port=$PORT_10251
  --mesos_master=${mesos_master}
  --api_servers=${api_server}
  --etcd_servers=${etcd_server_list}
  --mesos_user=${K8SM_MESOS_USER:-root}
  --v=${SCHEDULER_GLOG_v:-${logv}}
EOF

#--- service monitor
#
# (0) subscribe to monitor "up" events
# (1) fork service monitors
# (2) after all monitors have reported "up" once,
# (3) spawn the service tree
#
cd ${MESOS_SANDBOX}

cat <<EOF >monitor.sh
#!/usr/bin/execlineb
foreground {
  s6-ftrig-listen -a {
    ${monitor_dir}/apiserver-monitor/event U
  } /usr/bin/s6-svscan -t${S6_RESCAN:-30000} ${monitor_dir}
}
/usr/bin/s6-svscan -t${S6_RESCAN:-30000} ${service_dir}
EOF

chmod +x monitor.sh
exec ./monitor.sh
