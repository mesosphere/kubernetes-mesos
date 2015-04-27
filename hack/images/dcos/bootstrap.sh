#!/bin/sh
source /functions.sh

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
