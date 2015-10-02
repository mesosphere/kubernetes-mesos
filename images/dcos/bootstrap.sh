#!/bin/sh

if test "$1" = "kc"; then
  shift
  exec /opt/kubectl "$@"
fi

die() {
  test ${#} -eq 0 || echo "$@" >&2
  exit 1
}

indent() {
  sed 's/^/    /'
}

echo "* Environment:"
env | indent
echo

#TODO(jdef) we may want additional flags here
# -C for failing when files are clobbered
set -ue

if [ "${DEBUG:-false}" == "true" ]; then
  set -x
fi

# NOTE: uppercase env variables are generally indended to be possibly customized
# by callers. lowercase env variables are generally defined within this script.

echo -n "* Sandbox: "
sandbox=${MESOS_SANDBOX:-${MESOS_DIRECTORY:-}}
test -n "$sandbox" || die "failed to identify mesos sandbox. neither MESOS_DIRECTORY or MESOS_SANDBOX was specified"
echo "$sandbox"

# source utility functions
. /opt/functions.sh

echo "* Version: $(cat /opt/.version)"
cp /opt/.version ${sandbox}

# find the leader
echo -n "* Mesos master leader: "
mesos_master="${K8SM_MESOS_MASTER:-}"
test -n "${mesos_master}" || mesos_master="$(leading_master_ip):5050" || die "cannot find Mesos master leader"
echo "$mesos_master"

# set configuration values
default_dns_name=${DEFAULT_DNS_NAME:-k8sm.marathon.mesos}
echo "* DNS name: $default_dns_name"

apiserver_host=${APISERVER_HOST:-${default_dns_name}}
apiserver_port=${APISERVER_PORT:-8888}
apiserver_secure_port=${APISERVER_SECURE_PORT:-6443}
echo "* apiserver: $apiserver_host:$apiserver_port"
echo "* secure apiserver: $apiserver_host:$apiserver_secure_port"

scheduler_host=${SCHEDULER_HOST:-${default_dns_name}}
scheduler_port=${SCHEDULER_PORT:-10251}
scheduler_driver_port=${SCHEDULER_DRIVER_PORT:-25501}
echo "* scheduler: $scheduler_host:$scheduler_port"
echo "* scheduler driver port: $scheduler_driver_port"

framework_name=${FRAMEWORK_NAME:-kubernetes}
framework_weburi=${FRAMEWORK_WEBURI:-http://${apiserver_host}:${apiserver_port}/static/}
echo "* framework name: $framework_name"
echo "* framework_weburi: $framework_weburi"

controller_manager_host=${CONTROLLER_MANAGER_HOST:-${default_dns_name}}
controller_manager_port=${CONTROLLER_MANAGER_PORT:-10252}
echo "* controller manager: $controller_manager_host:$controller_manager_port"

# assume that the leading mesos master is always running a marathon
# service proxy, perhaps using haproxy.
service_proxy=${SERVICE_PROXY:-leader.mesos}
echo "* service proxy: $service_proxy"

# would be nice if this was auto-discoverable. if this value changes
# between launches of the framework, there can be dangling executors,
# so it is important that this point to some frontend load balancer
# of some sort, or is otherwise addressed by a fixed domain name or
# else a static IP.
etcd_server_port=${ETCD_SERVER_PORT:-4001}
etcd_server_peer_port=${ETCD_SERVER_PEER_PORT:-4002}

ENABLE_ETCD_SERVER=${ENABLE_ETCD_SERVER:-false}
if [ "${ENABLE_ETCD_SERVER}" != true ]; then
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

# find IP address of the container
echo -n "* host IP: "
host_ip=$(lookup_ip $HOST)
test -n "$host_ip" || die "cannot find host IP"
echo "$host_ip"

# mesos cloud provider configuration
cloud_config=${sandbox}/cloud.cfg
cat <<EOF >${cloud_config}
[mesos-cloud]
  mesos-master		= ${mesos_master}
  http-client-timeout	= ${K8SM_CLOUD_HTTP_CLIENT_TIMEOUT:-5s}
  state-cache-ttl	= ${K8SM_CLOUD_STATE_CACHE_TTL:-20s}
EOF

# address of the apiserver
kube_master="http://${apiserver_host}:${apiserver_port}"

#
# create services directories and scripts
#
mkdir -p ${log_dir}
prepare_var_run || die Failed to initialize apiserver run directory

prepare_service_script ${service_dir} .s6-svscan finish <<EOF
#!/usr/bin/execlineb
  define hostpath /var/run/kubernetes
  foreground { if { test -L \${hostpath} } rm -f \${hostpath} } exit 0
EOF

prepare_etcd_service() {
  prepare_service ${monitor_dir} ${service_dir} etcd-server ${ETCD_SERVER_RESPAWN_DELAY:-1} << EOF
#!/bin/sh
#TODO(jdef) don't run this as root
#TODO(jdef) would be super-cool to have socket-activation here so that clients can connect before etcd is really ready
exec 2>&1
mkdir -p ${etcd_server_data}
PATH="/opt:${PATH}"
export PATH
exec /opt/etcd \\
  -advertise-client-urls ${etcd_advertise_client_urls} \\
  -data-dir ${etcd_server_data} \\
  -initial-advertise-peer-urls ${etcd_initial_advertise_peer_urls} \\
  -initial-cluster ${etcd_server_name}=${etcd_initial_advertise_peer_urls} \\
  -listen-client-urls ${etcd_listen_client_urls} \\
  -listen-peer-urls ${etcd_listen_peer_urls} \\
  -name ${etcd_server_name}
EOF

  local deps="controller-manager scheduler"
  if [ -n "${apiserver_depends}" ]; then
    deps="${deps} apiserver-depends"
  else
    deps="${deps} apiserver"
  fi
  prepare_service_depends etcd-server ${etcd_server_list}/v2/stats/store getsSuccess ${deps}
}

#
# apiserver, uses frontend service proxy to connect with etcd
#
prepare_service ${monitor_dir} ${service_dir} apiserver ${APISERVER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
${apply_uids}
/opt/km apiserver
  --insecure-bind-address=${host_ip}
  --bind-address=${host_ip}
  --cloud-config=${cloud_config}
  --cloud-provider=mesos
  --etcd-servers=${etcd_server_list}
  --port=${apiserver_port}
  --secure-port=${apiserver_secure_port}
  --service-cluster-ip-range=${SERVICE_CLUSTER_IP_RANGE:-10.10.10.0/24}
  --v=${APISERVER_GLOG_v:-${logv}}
EOF
apiserver_depends=""

#
# controller-manager, doesn't need to use frontend proxy to access
# apiserver like the scheduler, it can access it directly here.
#
prepare_service ${monitor_dir} ${service_dir} controller-manager ${CONTROLLER_MANAGER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
${apply_uids}
/opt/km controller-manager
  --address=${host_ip}
  --cloud-config=${cloud_config}
  --cloud-provider=mesos
  --master=http://${host_ip}:${apiserver_port}
  --port=${controller_manager_port}
  --v=${CONTROLLER_MANAGER_GLOG_v:-${logv}}
EOF

prepare_kube_dns() {
  kube_cluster_dns=${DNS_SERVER_IP:-10.10.10.10}
  kube_cluster_domain=${DNS_DOMAIN:-cluster.local}
  local kube_nameservers=$(cat /etc/resolv.conf|grep -e ^nameserver|head -3|cut -f2 -d' '|sed -e 's/$/:53/g'|xargs echo -n|tr ' ' ,)
  kube_nameservers=${kube_nameservers:-${DNS_NAMESERVERS:-8.8.8.8:53,8.8.4.4:53}}

  sed -e "s/{{ pillar\['dns_replicas'\] }}/1/g" \
      -e "s,\(command = \"/kube2sky\"\),\\1\\"$'\n'"        - --kube_master_url=${kube_master}," \
      -e "s/{{ pillar\['dns_domain'\] }}/${kube_cluster_domain}/g" \
      /opt/skydns-rc.yaml.in > ${sandbox}/skydns-rc.yaml
  sed -e "s/{{ pillar\['dns_server'\] }}/${kube_cluster_dns}/g" \
    /opt/skydns-svc.yaml.in > ${sandbox}/skydns-svc.yaml

  prepare_service ${monitor_dir} ${service_dir} kube_dns ${KUBE_DNS_RESPAWN_DELAY:-3} <<EOF
#!/bin/sh
exec 2>&1

export KUBERNETES_MASTER="${kube_master}"

/opt/kubectl get rc --namespace=kube-system -l k8s-app=kube-dns | grep kube-dns >/dev/null && \
  /opt/kubectl get service --namespace=kube-system kube-dns >/dev/null && \
  touch kill && exit 0

/opt/kubectl create -f ${sandbox}/skydns-rc.yaml
/opt/kubectl create -f ${sandbox}/skydns-svc.yaml
EOF

  sed -i -e '$i test -f kill && exec s6-svc -d $(pwd) || exec \\' ${service_dir}/kube_dns/finish

  apiserver_depends="${apiserver_depends} kube_dns"
}

# launch kube-dns if enabled
kube_cluster_dns=""
kube_cluster_domain=""
if [ "${ENABLE_DNS:-true}" == true ]; then
  prepare_kube_dns
fi

#
# kube-ui, deployed as pod and service, later available under
# <apiserver-url>/api/v1/proxy/namespaces/default/services/kube-ui
#
prepare_kube_ui() {
  prepare_service ${monitor_dir} ${service_dir} kube_ui ${KUBE_UI_RESPAWN_DELAY:-3} <<EOF
#!/bin/sh
exec 2>&1

export KUBERNETES_MASTER="${kube_master}"

/opt/kubectl get rc --namespace=kube-system -l k8s-app=kube-ui | grep -q kube-ui >/dev/null && \
  /opt/kubectl get service --namespace=kube-system kube-ui >/dev/null && \
  touch kill && exit 0

/opt/kubectl create -f /opt/kube-ui-rc.yaml
/opt/kubectl create -f /opt/kube-ui-svc.yaml
EOF
  sed -i -e '$i test -f kill && exec s6-svc -d $(pwd) || exec \\' ${service_dir}/kube_ui/finish
  apiserver_depends="${apiserver_depends} kube_ui"
}

if [ "${ENABLE_UI:-true}" == true ]; then
  prepare_kube_ui
fi

# create dependency service for all services that need apiserver to be started
if [ -n "${apiserver_depends}" ]; then
  prepare_service_depends apiserver ${kube_master}/healthz ok ${apiserver_depends}
fi

#
# scheduler, uses frontend service proxy to access apiserver and
# etcd. it spawns executors configured with the same address for
# --api_servers and if the IPs change (because this container changes
# hosts) then the executors become zombies.
#
prepare_service ${monitor_dir} ${service_dir} scheduler ${SCHEDULER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
${apply_uids}
/opt/km scheduler
  --address=${host_ip}
  --advertised-address=${scheduler_host}:${scheduler_port}
  --api-servers=http://${apiserver_host}:${apiserver_port}
  --driver-port=${scheduler_driver_port}
  --service-address=${SCHEDULER_SERVICE_ADDRESS:-10.10.10.9}
  --etcd-servers=${etcd_server_list}
  --framework-name=${framework_name}
  --framework-weburi=${framework_weburi}
  --mesos-master=${mesos_master}
  --mesos-role="${K8SM_MESOS_ROLE:-*}"
  --mesos-user=${K8SM_MESOS_USER:-root}
  --mesos-cgroup-prefix=${CGROUP_PREFIX:-/mesos}
  --port=${scheduler_port}
  --v=${SCHEDULER_GLOG_v:-${logv}}
  --executor-logv=${EXECUTOR_GLOG_v:-${logv}}
  --proxy-logv=${PROXY_GLOG_v:-${logv}}
  --default-container-cpu-limit=${DEFAULT_CONTAINER_CPU_LMIIT:-0.25}
  --default-container-mem-limit=${DEFAULT_CONTAINER_MEM_LMIIT:-64}
  --minion-path-override=${PATH}:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
  --contain-pod-resources=${SCHEDULER_CONTAIN_POD_RESOURCES:-true}
  --account-for-pod-resources=${SCHEDULER_ACCOUNT_FOR_POD_RESOURCES:-true}
  --mesos-executor-cpus=${SCHEDULER_MESOS_EXECUTOR_CPUS:-0.25}
  --mesos-executor-mem=${SCHEDULER_MESOS_EXECUTOR_MEM:-128}
  $(if [ -n "${K8SM_FAILOVER_TIMEOUT:-}" ]; then echo "--failover-timeout=${K8SM_FAILOVER_TIMEOUT}"; fi)
  $(if [ -n "${kube_cluster_dns}" ]; then echo "--cluster-dns=${kube_cluster_dns}"; fi)
  $(if [ -n "${kube_cluster_domain}" ]; then echo "--cluster-domain=${kube_cluster_domain}"; fi)
EOF

if [ "$ENABLE_ETCD_SERVER" == true ]; then
  prepare_etcd_service
fi

#--- service monitor
#
# (0) subscribe to monitor "up" events
# (1) fork service monitors
# (2) after all monitors have reported "up" once,
# (3) spawn the service tree
#
cd ${sandbox}
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

echo -n "* Monitoring apiserver, controller-manager and scheduler..."
chmod +x monitor.sh
exec ./monitor.sh
