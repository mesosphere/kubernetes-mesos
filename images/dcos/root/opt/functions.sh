#!/bin/sh

test -n "$sandbox" || die failed to identify mesos sandbox

lookup_ip() {
  local leader=$(nslookup "$1" | grep -v 127.0.0.1 | sed -e '/^Server:/,/^Address .*$/{ d }' -e '/^$/d'|grep -e '^Address '|cut -f3 -d' '|head -1)
  test -n "$leader" || die Failed to identify $1
  echo $leader
}

leading_master_ip() {
  local leader=$(nslookup leader.mesos | sed -e '/^Server:/,/^Address .*$/{ d }' -e '/^$/d'|grep -e '^Address '|cut -f3 -d' ')
  test -n "$leader" || die Failed to identify mesos master, missing K8SM_MESOS_MASTER variable and cannot find leader.mesos
  echo "leader.mesos"
}

# NOTE: this is really intended only for the apiserver since it runs in a
# docker CT and has r/w access to the root file system, unlike the executor
# proxy services.
prepare_var_run() {
  local hostpath=/var/run/kubernetes
  local run=${sandbox}/run

  test -L $hostpath && rm -f $hostpath
  test -d $hostpath && rm -rf $hostpath  # should not happen, but...
  test -f $hostpath && rm -f $hostpath   # should not happen, but...
  mkdir -p $(dirname $hostpath) && mkdir -p $run && ln -s $run $hostpath && chown nobody:nobody $run
}

log_args() {
  local log_history=${1:-${LOG_HISTORY:-10}}
  local log_size=${2:-${LOG_SIZE:-10000000}}
  echo "-p -b n${log_history} s${log_size}"
}

prepare_service_script() {
  local svcdir=$1
  local name=$2
  local script=$3
  local log_history=${4:-}
  local log_size=${5:-}

  mkdir -p ${svcdir}/${name} || die Failed to create service directory at ${svcdir}/${name}
  cat >${svcdir}/${name}/${script}
  chmod +x ${svcdir}/${name}/${script}

  test "$script" = "run" || return 0

  # "run" scripts are special, they get everything below..
  s6-mkfifodir -f ${svcdir}/${name}/event

  # only set up logging for run service scripts
  mkdir -p ${log_dir}/${name}
  mkdir -p ${svcdir}/${name}/log
  cat <<EOF >${svcdir}/${name}/log/run
#!/bin/sh
exec s6-log $(log_args ${log_history} ${log_size}) ${log_dir}/${name}
EOF
  chmod +x ${svcdir}/${name}/log/run

  local loglink=log/${name}/current
  ln -f -s $loglink ${sandbox}/${name}.log
}

prepare_monitor_script() {
  local mondir=$1
  local svcdir=$2
  local name=$3
  local log_history=${4:-}
  local log_size=${5:-}

  mkdir -p ${svcdir}/${name} || die Failed to create service directory at ${svcdir}/${name}

  prepare_service_script ${mondir} ${name}-monitor run ${log_history} ${log_size} <<EOF
#!/bin/sh
exec 2>&1
exec \\
  loopwhilex \\
    foreground s6-ftrig-listen1 ${svcdir}/${name}/event u \\
      s6-notifywhenup echo '' \\
    echo ${name} service started
EOF
}

prepare_service() {
  local mond="$1"
  local svcd="$2"
  local name="$3"
  local respawnSec="$4"

  echo "* prepare service ${name}"
  cat | prepare_service_script ${svcd} ${name} run
  prepare_monitor_script ${mond} ${svcd} ${name} 5 1000000
  prepare_service_script ${svcd} ${name} finish <<EOF
#!/bin/sh
exec 2>&1
test "\$1" != "256" &&
    printf "I%s %7d respawn.xx:0] sleeping ${respawnSec}s before respawning ${name}\\n" \\
      "\$(date '+%m%d %H:%M:%S.999999')" "\${2}" &&
    sleep ${respawnSec}
exit 0
EOF
}

# usage: $0 {watched-service} {version-check-url} {version-check-token} {dependent-services}
prepare_service_depends() {
  local watchedService=$1
  local watcher=${watchedService}-depends
  local versionCheckUrl=$2
  local versionCheckToken=$3
  shift
  shift
  shift

  # down by default because we need to catch the signal when it's up and running
  touch ${service_dir}/$watchedService/down
  # we only want to start these services after we know that the watched-service is up and running
  for i in $*; do
    touch ${service_dir}/$i/down
  done

  # 1. startup waited-on service, waiting for an U signal
  # 2. upon receving the signal, start up dependent services
  prepare_service_script ${service_dir} ${watcher} run 2 1000000 <<EOF
#!/bin/sh
exec 2>&1
set -vx

rm -f down

echo \$(date -Iseconds) sending start signal to ${watchedService}
s6-svc -u ${service_dir}/${watchedService}

version_check() {
  wget -q -O - $versionCheckUrl | grep -i -e "$versionCheckToken" >/dev/null && sleep 5 || exit 2
  echo
}

# HACK(jdef): no super-reliable way to tell if waited-on service will stay up for long, so
# check that 3 seqential version checks pass and if so assume the world is good
version_check; version_check; version_check

echo \$(date -Iseconds) starting $* services...
for i in $*; do
  s6-svc -u ${service_dir}/\$i
done
touch down
EOF

  prepare_service_script ${service_dir} ${watcher} finish <<EOF
#!/bin/sh
exec 2>&1
echo \$(date -Iseconds) ${watcher}-finish \$*
test -f down && exec s6-svc -d \$(pwd) || exec sleep 4
EOF
}

# Builds the APISERVER_RUNTIME_CONFIG var from other feature enable options
# original source: github.com/kubernetes/kubernetes/cluster/gce/util.sh
build_runtime_config() {
  local ENABLE_EXPERIMENTAL_API=$(echo ${ENABLE_DEPLOYMENTS:-}${ENABLE_DAEMONSETS:-} | grep -q "true")
  if [[ -n "${ENABLE_EXPERIMENTAL_API:-}" ]]; then
      if [[ -z "${APISERVER_RUNTIME_CONFIG:-}" ]]; then
          APISERVER_RUNTIME_CONFIG="extensions/v1beta1=true"
      else
          # TODO: add checking if APISERVER_RUNTIME_CONFIG contains "extensions/v1beta1=false" and appending "extensions/v1beta1=true" if not.
          if echo "${APISERVER_RUNTIME_CONFIG}" | grep -q -v "extensions/v1beta1=true"; then
              die "Experimental API should be turned on, but is not turned on in APISERVER_RUNTIME_CONFIG!"
          fi
      fi
  fi
  if [[ "${ENABLE_DEPLOYMENTS:-}" == "true" ]]; then
      if [[ -z "${APISERVER_RUNTIME_CONFIG:-}" ]]; then
          APISERVER_RUNTIME_CONFIG="extensions/v1beta1/deployments=true"
      else
          APISERVER_RUNTIME_CONFIG="${APISERVER_RUNTIME_CONFIG},extensions/v1beta1/deployments=true"
      fi
  fi
  if [[ "${ENABLE_DAEMONSETS:-}" == "true" ]]; then
      if [[ -z "${APISERVER_RUNTIME_CONFIG:-}" ]]; then
          APISERVER_RUNTIME_CONFIG="extensions/v1beta1/daemonsets=true"
      else
          APISERVER_RUNTIME_CONFIG="${APISERVER_RUNTIME_CONFIG},extensions/v1beta1/daemonsets=true"
      fi
  fi
}


log_dir=${LOG_DIR:-$sandbox/log}
monitor_dir=${MONITOR_DIR:-$sandbox/monitor.d}
service_dir=${SERVICE_DIR:-$sandbox/service.d}
logv=${GLOG_v:-0}
