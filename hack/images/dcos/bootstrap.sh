#!/bin/sh

sandbox=${MESOS_SANDBOX:-${MESOS_DIRECTORY}}
test -n "$sandbox" || die failed to identify mesos sandbox. neither MESOS_DIRECTORY or MESOS_SANDBOX was specified

. /opt/functions.sh

mesos_leader=$(leading_master_ip) || die
mesos_master=${mesos_leader}:5050

default_dns_name=${DEFAULT_DNS_NAME:-k8sm.marathon.mesos}

apiserver_host=${APISERVER_HOST:-${default_dns_name}}
apiserver_port=${APISERVER_PORT:-8888}
apiserver_ro_port=${APISERVER_RO_PORT:-8889}
apiserver_ro_host=${APISERVER_RO_HOST:-${default_dns_name}}

scheduler_host=${SCHEDULER_HOST:-${default_dns_name}}
scheduler_port=${SCHEDULER_PORT:-10251}

controller_manager_host=${CONTROLLER_MANAGER_HOST:-${default_dns_name}}
controller_manager_port=${CONTROLLER_MANAGER_PORT:-10252}

# assume that the leading mesos master is always running a marathon
# service proxy, perhaps using haproxy.
service_proxy=${SERVICE_PROXY:-${mesos_leader}}

# would be nice if this was auto-discoverable. if this value changes
# between launches of the framework, there can be dangling executors,
# so it is important that this point to some frontend load balancer
# of some sort, or is otherwise addressed by a fixed domain name or
# else a static IP.
etcd_server_port=${ETCD_SERVER_PORT:-4001}
etcd_server_peer_port=${ETCD_SERVER_PEER_PORT:-4002}
if test -n "$DISABLE_ETCD_SERVER"; then
  etcd_server_list=${ETCD_SERVER_LIST:-http://${service_proxy}:${etcd_server_port}}
else
  etcd_advertise_server_host=${ETCD_ADVERTISE_SERVER_HOST:-${default_dns_name}}
  etcd_server_host=${ETCD_SERVER_HOST:-${HOST}}

  etcd_initial_advertise_peer_urls=${ETCD_INITIAL_ADVERTISE_PEER_URLS:-http://${etcd_advertise_server_host}:${etcd_server_peer_port}}
  etcd_listen_peer_urls=${ETCD_LISTEN_PEER_URLS:-http://${etcd_server_host}:${etcd_server_peer_port}}

  etcd_advertise_client_urls=${ETCD_ADVERTISE_CLIENT_URLS:-http://${etcd_advertise_server_host}:${etcd_server_port}}
  etcd_listen_client_urls=${ETCD_LISTEN_CLIENT_URLS:-http://${etcd_server_host}:${etcd_server_port}}

  etcd_server_name=${ETCD_SERVER_NAME:-k8sm-etcd}
  etcd_server_data=${ETCD_SERVER_DATA:-${sandbox}/etcd-data}
  etcd_server_list=${etcd_listen_client_urls}
fi

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

prepare_etcd_service() {
  prepare_service ${monitor_dir} ${service_dir} etcd-server ${ETCD_SERVER_RESPAWN_DELAY:-1} << EOF
#!/bin/sh
#TODO(jdef) don't run this as root
#TODO(jdef) would be super-cool to have socket-activation here so that clients can connect before etcd is really ready
exec 2>&1
mkdir -p $etcd_server_data
echo starting etcd
PATH=/opt:$PATH
export PATH
exec /opt/etcd \\
  -data-dir $etcd_server_data \\
  -name $etcd_server_name \\
  -initial-cluster ${etcd_server_name}=${etcd_initial_advertise_peer_urls} \\
  -initial-advertise-peer-urls ${etcd_initial_advertise_peer_urls} \\
  -advertise-client-urls ${etcd_advertise_client_urls} \\
  -listen-client-urls ${etcd_listen_client_urls} \\
  -listen-peer-urls ${etcd_listen_peer_urls}
EOF

  # down by default because we need to catch the signal when it's up and running
  touch ${service_dir}/etcd-server/down
  # we only want to start these services after we know that etcd is up and running
  touch ${service_dir}/apiserver/down
  touch ${service_dir}/scheduler/down
  touch ${service_dir}/controller-manager/down

  # 1. startup etcd, waiting for an U signal
  # 2. upon receving the signal, start up apiserver and scheduler
  # 3. block forever (TODO: should just terminate and not restart)
  prepare_service_script ${service_dir} etcd-server-watcher run <<EOF
#!/bin/sh
exec 2>&1
set -vx
echo \$(date -Iseconds) sending start signal to etcd-server
s6-svc -u ${service_dir}/etcd-server

version_check() {
  wget -q -O - ${etcd_server_list}/version >/dev/null 2>&1 && sleep 2 || exit 2
}

# HACK(jdef): no super-reliable way to tell if etcd will stay up for long, so
# check that 5 seqential version checks pass and if so assume the world is good
version_check; version_check; version_check; version_check; version_check

echo \$(date -Iseconds) starting apiserver, scheduler, controller-manager services...
s6-svc -u ${service_dir}/apiserver
s6-svc -u ${service_dir}/scheduler
s6-svc -u ${service_dir}/controller-manager
touch down
EOF

  prepare_service_script ${service_dir} etcd-server-watcher finish <<EOF
#!/bin/sh
exec 2>&1
echo \$(date -Iseconds) etcd-server-watcher-finish \$*
test -f down && exec s6-svc -d \$(pwd) || exec sleep 4
EOF
}

#
# apiserver, uses frontend service proxy to connect with etcd
#
prepare_service ${monitor_dir} ${service_dir} apiserver ${APISERVER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
$apply_uids
/opt/km apiserver
  --address=$HOST
  --port=$apiserver_port
  --read_only_port=$apiserver_ro_port
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
/opt/km controller-manager
  --address=$HOST
  --port=$controller_manager_port
  --mesos_master=${mesos_master}
  --master=http://$HOST:$apiserver_port
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
/opt/km scheduler
  --address=$HOST
  --port=$scheduler_port
  --mesos_master=${mesos_master}
  --api_servers=http://${apiserver_host}:${apiserver_port}
  --etcd_servers=${etcd_server_list}
  --mesos_user=${K8SM_MESOS_USER:-root}
  --v=${SCHEDULER_GLOG_v:-${logv}}
  --km_path=${sandbox}/executor-installer.sh
  --advertised_address=${scheduler_host}:${scheduler_port}
EOF

test -n "$DISABLE_ETCD_SERVER" || prepare_etcd_service

cd ${sandbox}

>executor-extractor echo '#!/bin/bash
echo "Self Extracting Installer"
ARCHIVE=`awk "/^__ARCHIVE_BELOW__/ {print NR + 1; exit 0; }" $0`
tail -n+$ARCHIVE $0 | tar xzv
exec ./opt/executor.sh "$@"
__ARCHIVE_BELOW__'

(cat executor-extractor; tar czf - -C / opt etc/leapsecs.dat usr bin sbin) >executor-installer.sh
chmod +x executor-installer.sh

#--- service monitor
#
# (0) subscribe to monitor "up" events
# (1) fork service monitors
# (2) after all monitors have reported "up" once,
# (3) spawn the service tree
#
cat <<EOF >monitor.sh
#!/usr/bin/execlineb
foreground {
  s6-ftrig-listen -a {
    ${monitor_dir}/apiserver-monitor/event U
    ${monitor_dir}/scheduler-monitor/event U
    ${monitor_dir}/controller-manager-monitor/event U
  } /usr/bin/s6-svscan -t${S6_RESCAN:-30000} ${monitor_dir}
}
/usr/bin/s6-svscan -t${S6_RESCAN:-30000} ${service_dir}
EOF

chmod +x monitor.sh
exec ./monitor.sh
