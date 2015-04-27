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
  local respawnSec="$4"

  cat | prepare_service_script ${svcd} ${name} run
  prepare_monitor_script ${mond} ${svcd} ${name}
  prepare_service_script ${svcd} ${name} finish <<EOF
#!/usr/bin/execlineb -S2
foreground {
  if { test "\${1}" != "256" }
    foreground {
      backtick -n ts { date "+%m%d %H:%M:%S.999999" }
      import ts printf "I%s %7d respawn.xx:0] sleeping ${respawnSec}s before respawning ${name}\\n" "\${ts}" "\${2}"
    } sleep ${respawnSec}
}
exit 0
EOF
}

log_dir=${LOG_DIR:-$MESOS_SANDBOX/log}
monitor_dir=${MONITOR_DIR:-$MESOS_SANDBOX/monitor.d}
service_dir=${SERVICE_DIR:-$MESOS_SANDBOX/service.d}

log_history=${LOG_HISTORY:-10}
log_size=${LOG_SIZE:-2000000}
log_args="-p -b n${log_history} s${log_size}"
logv=${GLOG_v:-0}
