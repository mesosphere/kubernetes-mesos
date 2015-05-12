#!/bin/sh

sandbox=${MESOS_SANDBOX:-${MESOS_DIRECTORY}}
test -n "$sandbox" || die failed to identify mesos sandbox. neither MESOS_DIRECTORY or MESOS_SANDBOX was specified

. /opt/functions.sh
cp /opt/.version ${sandbox}

mesos_leader=$(leading_master_ip) || die
mesos_master=${mesos_leader}:5050

default_dns_name=${DEFAULT_DNS_NAME:-k8sm.marathon.mesos}

apiserver_host=${APISERVER_HOST:-${default_dns_name}}
apiserver_port=${APISERVER_PORT:-8888}
apiserver_ro_port=${APISERVER_RO_PORT:-8889}
apiserver_ro_host=${APISERVER_RO_HOST:-${default_dns_name}}
apiserver_secure_port=${APISERVER_SECURE_PORT:-6443}

scheduler_host=${SCHEDULER_HOST:-${default_dns_name}}
scheduler_port=${SCHEDULER_PORT:-10251}
scheduler_driver_port=${SCHEDULER_DRIVER_PORT:-25501}

framework_weburi=${FRAMEWORK_WEBURI:-http://${apiserver_host}:${apiserver_port}/static/}

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
  etcd_advertise_server_host=${ETCD_ADVERTISE_SERVER_HOST:-127.0.0.1}
  etcd_server_host=${ETCD_SERVER_HOST:-127.0.0.1}

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
host_ip=$(echo $HOST | grep -e '[0-9]\+\.[0-9]\+\.[0-9]\+\.[0-9]\+' >/dev/null 2>&1)
test -n "$host_ip" || host_ip=$(lookup_ip "$HOST")
echo host_ip=$host_ip

# mesos cloud provider configuration
cloud_config=${sandbox}/cloud.cfg
cat <<EOF >${cloud_config}
[mesos-cloud]
  mesos-master		= ${mesos_master}
  http-client-timeout	= ${K8SM_CLOUD_HTTP_CLIENT_TIMEOUT:-5s}
  state-cache-ttl	= ${K8SM_CLOUD_STATE_CACHE_TTL:-20s}
EOF

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

  local deps="apiserver controller-manager scheduler"
  test -n "$ENABLE_DNS" && deps="$deps kube_dns"
  prepare_service_depends etcd-server ${etcd_server_list}/version $deps
}

#
# apiserver, uses frontend service proxy to connect with etcd
#
prepare_service ${monitor_dir} ${service_dir} apiserver ${APISERVER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
$apply_uids
/opt/km apiserver
  --address=$host_ip
  --port=$apiserver_port
  --read_only_port=$apiserver_ro_port
  --secure_port=${apiserver_secure_port}
  --etcd_servers=${etcd_server_list}
  --portal_net=${PORTAL_NET:-10.10.10.0/24}
  --cloud_provider=mesos
  --cloud_config=${cloud_config}
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
  --address=$host_ip
  --port=$controller_manager_port
  --master=http://$host_ip:$apiserver_port
  --cloud_provider=mesos
  --cloud_config=${cloud_config}
  --v=${CONTROLLER_MANAGER_GLOG_v:-${logv}}
EOF

#
# scheduler, uses frontend service proxy to access apiserver and
# etcd. it spawns executors configured with the same address for
# --api_servers and if the IPs change (because this container changes
# hosts) then the executors become zombies.
#
mesos_role="${K8SM_MESOS_ROLE}"
test -n "$mesos_role" || mesos_role="*"
failover_timeout="${K8SM_FAILOVER_TIMEOUT}"
if test -n "$failover_timeout"; then
  failover_timeout="--failover_timeout=$failover_timeout"
else
  unset failover_timeout
fi

# pick a fixed scheduler service address if DNS enabled because we don't want to
# accidentally conflict with it if the scheduler randomly chooses the same addr.
scheduler_service_address=""
test -n "$ENABLE_DNS" && scheduler_service_address="--service_address=${SCHEDULER_SERVICE_ADDRESS:-10.10.10.9}"

prepare_service ${monitor_dir} ${service_dir} scheduler ${SCHEDULER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
$apply_uids
/opt/km scheduler $failover_timeout $scheduler_service_address
  --address=$host_ip
  --port=$scheduler_port
  --mesos_master=${mesos_master}
  --api_servers=http://${apiserver_host}:${apiserver_port}
  --etcd_servers=${etcd_server_list}
  --mesos_user=${K8SM_MESOS_USER:-root}
  --mesos_role="$mesos_role"
  --v=${SCHEDULER_GLOG_v:-${logv}}
  --km_path=${sandbox}/executor-installer.sh
  --advertised_address=${scheduler_host}:${scheduler_port}
  --framework_weburi=${framework_weburi}
  --driver_port=${scheduler_driver_port}
EOF

prepare_kube_dns() {
  local obj="skydns-rc.yaml skydns-svc.yaml"
  local f
  kube_cluster_dns=${DNS_SERVER_IP:-10.10.10.10}
  kube_cluster_domain=${DNS_DOMAIN:-kubernetes.local}
  local kube_nameservers=$(cat /etc/resolv.conf|grep -e ^nameserver|head -3|cut -f2 -d' '|sed -e 's/$/:53/g'|xargs echo -n|tr ' ' ,)
  kube_nameservers=${kube_nameservers:-${DNS_NAMESERVERS:-8.8.8.8:53,8.8.4.4:53}}
  for f in $obj; do
    cat /opt/$f.in | sed \
      -e "s/___dns_domain___/${kube_cluster_domain}/g" \
      -e "s/___dns_replicas___/${DNS_REPLICAS:-1}/g" \
      -e "s/___dns_server___/${kube_cluster_dns}/g" \
      -e "s/___dns_nameservers___/${kube_nameservers}/g" \
      >${sandbox}/$f
  done
  prepare_service ${monitor_dir} ${service_dir} kube_dns ${KUBE_DNS_RESPAWN_DELAY:-3} <<EOF
#!/bin/sh
exec 2>&1

KUBERNETES_MASTER=http://${host_ip}:${apiserver_port}
export KUBERNETES_MASTER

/opt/kubectl get rc kube-dns >/dev/null && \
  /opt/kubectl get service kube-dns >/dev/null && \
  touch kill && exit 0

for i in $obj; do
  /opt/kubectl create -f ${sandbox}/\$i
done
EOF

  sed -i -e '$i test -f kill && exec s6-svc -d $(pwd) || exec \\' ${service_dir}/kube_dns/finish

  # it's ok if etcd-server-depends is also monitoring apiserver, the order of ops is this:
  # 1. etcd-server-depends starts etcd, waits for it to come up
  # 2. etcd-server-depends starts apiserver, control-manager, scheduler, kube_dns
  # 3. kube_dns-depends starts apiserver (already started), waits for it to come up
  # 4. kube_dns-depends starts kube_dns
  prepare_service_depends apiserver http://${host_ip}:${apiserver_port}/healthz kube_dns
}

test -n "$DISABLE_ETCD_SERVER" || prepare_etcd_service

kube_cluster_dns=""
kube_cluster_domain=""
if test -n "$ENABLE_DNS"; then
  prepare_kube_dns
  kube_cluster_dns="--cluster_dns=$kube_cluster_dns"
  kube_cluster_domain="--cluster_domain=$kube_cluster_domain"
fi

cd ${sandbox}

>executor-extractor echo '#!/bin/bash
echo "Self Extracting Installer"
ARCHIVE=`awk "/^__ARCHIVE_BELOW__/ {print NR + 1; exit 0; }" $0`
tail -n+$ARCHIVE $0 | tar xzv
echo mounts before unshare:
cat /proc/$$/mounts
KUBERNETES_MASTER=http://'"${apiserver_host}:${apiserver_port}"'
KUBERNETES_RO_SERVICE_HOST='"${host_ip}"'
KUBERNETES_RO_SERVICE_PORT='"${apiserver_ro_port}"'
KUBE_CLUSTER_DNS='"${kube_cluster_dns}"'
KUBE_CLUSTER_DOMAIN='"${kube_cluster_domain}"'
export KUBERNETES_MASTER
export KUBERNETES_RO_SERVICE_HOST
export KUBERNETES_RO_SERVICE_PORT
export KUBE_CLUSTER_DNS
export KUBE_CLUSTER_DOMAIN
exec unshare -m -- ./opt/executor.sh "$@"
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
