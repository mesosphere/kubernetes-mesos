#!/bin/sh
#
# this bootstrap script is intended to be the startup script for a docker
# container that runs the k8s-mesos framework scheduler that, at this point
# in time, consists of several k8s master processes and possibly an embedded
# etcd server.
#
# it is assumed that this docker container is being executed in BRIDGED mode,
# or at least in a way that isolates the network namespace of this container
# from ths host netns.
#

kubectl=/opt/kubectl

if test "$1" = "kc"; then
  shift
  exec ${kubectl} "$@"
fi

die() {
  test ${#} -eq 0 || echo "$@" >&2
  exit 1
}

indent() {
  sed 's/^/    /'
}

echo "* Environment:"
env | sort | indent
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

# build APISERVER_RUNTIME_CONFIG variable
build_runtime_config

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
apiserver_proxy_port=${APISERVER_PROXY_PORT:-6443}
apiserver_secure_port=${APISERVER_SECURE_PORT:-16443}
echo "* apiserver: $apiserver_host:$apiserver_port"
echo "* proxied apiserver: $apiserver_host:$apiserver_proxy_port"
echo "* secure apiserver: $apiserver_host:$apiserver_secure_port"

scheduler_host=${SCHEDULER_HOST:-${default_dns_name}}
scheduler_port=${SCHEDULER_PORT:-10251}
scheduler_driver_port=${SCHEDULER_DRIVER_PORT:-25501}
echo "* scheduler: $scheduler_host:$scheduler_port"
echo "* scheduler driver port: $scheduler_driver_port"

controller_manager_host=${CONTROLLER_MANAGER_HOST:-${default_dns_name}}
controller_manager_port=${CONTROLLER_MANAGER_PORT:-10252}
echo "* controller manager: $controller_manager_host:$controller_manager_port"

[ -n "${MESOS_AUTHENTICATION_PRINCIPAL:-}" ] && echo "* mesos authentication principal: ${MESOS_AUTHENTICATION_PRINCIPAL}"
[ -n "${MESOS_AUTHENTICATION_SECRET_FILE:-}" ] && echo "* mesos authentication secret file: ${MESOS_AUTHENTICATION_SECRET_FILE}"

# would be nice if this was auto-discoverable. if this value changes
# between launches of the framework, there can be dangling executors,
# so it is important that this point to some frontend load balancer
# of some sort, or is otherwise addressed by a fixed domain name or
# else a static IP.
etcd_server_port=${ETCD_SERVER_PORT:-4001}
etcd_server_peer_port=${ETCD_SERVER_PEER_PORT:-4002}

ETCD_MESOS_FRAMEWORK_NAME=${ETCD_MESOS_FRAMEWORK_NAME:-disabled}
etcd_advertise_server_host=${ETCD_ADVERTISE_SERVER_HOST:-127.0.0.1}
etcd_server_host=${ETCD_SERVER_HOST:-127.0.0.1}

etcd_initial_advertise_peer_urls=${ETCD_INITIAL_ADVERTISE_PEER_URLS:-http://${etcd_advertise_server_host}:${etcd_server_peer_port}}
etcd_listen_peer_urls=${ETCD_LISTEN_PEER_URLS:-http://${etcd_server_host}:${etcd_server_peer_port}}

etcd_advertise_client_urls=${ETCD_ADVERTISE_CLIENT_URLS:-http://${etcd_advertise_server_host}:${etcd_server_port}}
etcd_listen_client_urls=${ETCD_LISTEN_CLIENT_URLS:-http://${etcd_server_host}:${etcd_server_port}}

etcd_server_name=${ETCD_SERVER_NAME:-k8sm-etcd}
etcd_server_data=${ETCD_SERVER_DATA:-${sandbox}/etcd-data}
etcd_server_list=${etcd_listen_client_urls}

# run service procs as "nobody"
apply_uids="s6-applyuidgid -u $(id -u nobody) -g $(id -g nobody)"

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
kube_master_proxy="http://${apiserver_host}:${apiserver_proxy_port}"

# framework addresses
framework_name=${FRAMEWORK_NAME:-kubernetes}
framework_weburi=${FRAMEWORK_WEBURI:-${kube_master_proxy}}
echo "* framework name: $framework_name"
echo "* framework_weburi: $framework_weburi"

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
  mkdir -p ${etcd_server_data}
  prepare_service ${monitor_dir} ${service_dir} etcd-server ${ETCD_SERVER_RESPAWN_DELAY:-1} << EOF
#!/usr/bin/execlineb
#TODO(jdef) don't run this as root
#TODO(jdef) would be super-cool to have socket-activation here so that clients can connect before etcd is really ready
fdmove -c 2 1
/opt/etcd
  -advertise-client-urls ${etcd_advertise_client_urls}
  -data-dir ${etcd_server_data}
  -initial-advertise-peer-urls ${etcd_initial_advertise_peer_urls}
  -initial-cluster ${etcd_server_name}=${etcd_initial_advertise_peer_urls}
  -listen-client-urls ${etcd_listen_client_urls}
  -listen-peer-urls ${etcd_listen_peer_urls}
  -name ${etcd_server_name}
EOF

  local deps="scheduler"
  if [ -n "${apiserver_depends}" ]; then
    deps="${deps} apiserver-depends"
  else
    deps="${deps} apiserver"
  fi
  prepare_service_depends etcd-server ${etcd_server_list}/v2/stats/store getsSuccess ${deps}
}

prepare_etcd_proxy() {
  mkdir -p ${etcd_server_data}
  prepare_service ${monitor_dir} ${service_dir} etcd-server ${ETCD_SERVER_RESPAWN_DELAY:-1} << EOF
#!/usr/bin/execlineb
#TODO(jdef) don't run this as root
#TODO(jdef) would be super-cool to have socket-activation here so that clients can connect before etcd is really ready
fdmove -c 2 1
/opt/etcd
  -proxy=on
  -advertise-client-urls ${etcd_advertise_client_urls}
  -data-dir ${etcd_server_data}
  -listen-client-urls ${etcd_listen_client_urls}
  -discovery-srv=${ETCD_MESOS_FRAMEWORK_NAME}.mesos
EOF

  local deps="scheduler"
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
mkdir -p /etc/kubernetes
admin_token="$(openssl rand -hex 32)"
echo "${admin_token},admin,admin" > /etc/kubernetes/token-users

apiserver_tls_cert_file=/etc/ssl/apiserver.crt
apiserver_tls_private_key_file=/etc/ssl/apiserver.key
mkdir -p /etc/ssl/override
chmod 750 /etc/ssl/override

if [ -n "${PKI_APISERVER_CRT}" ] && [ -n "${PKI_APISERVER_KEY}" ] && ! echo "${PKI_APISERVER_CRT}${PKI_APISERVER_KEY}" | egrep -q -e '^disabled|disabled$' 2>/dev/null; then
      apiserver_tls_cert_file=/etc/ssl/override/apiserver.crt
      apiserver_tls_private_key_file=/etc/ssl/override/apiserver.key
      echo "Using user-specified TLS configuration for apiserver"
      echo "${PKI_APISERVER_CRT}"|base64 -d >${apiserver_tls_cert_file}
      echo "${PKI_APISERVER_KEY}"|base64 -d >${apiserver_tls_private_key_file}
else
      echo "WARNING: Using insecure TLS configuration for apiserver"
fi

apiserver_service_account_key_file=/etc/ssl/service-accounts.key
controller_service_account_private_key_file=${apiserver_service_account_key_file}
controller_root_ca_file=/etc/ssl/root-ca.crt

if [ -n "${PKI_SERVICE_ACCOUNTS_KEY}" ] && [ -n "${PKI_ROOT_CA_CRT}" ] && ! echo "${PKI_SERVICE_ACCOUNTS_KEY}${PKI_ROOT_CA_CRT}" | egrep -q -e '^disabled|disabled$' 2>/dev/null; then
      apiserver_service_account_key_file=/etc/ssl/override/service-accounts.key
      controller_service_account_private_key_file=${apiserver_service_account_key_file}
      controller_root_ca_file=/etc/ssl/override/root-ca.crt
      echo "Using user-specified security for service account configuration"
      echo "${PKI_SERVICE_ACCOUNTS_KEY}"|base64 -d >${apiserver_service_account_key_file}
      echo "${PKI_ROOT_CA_CRT}"|base64 -d >${controller_root_ca_file}
else
      echo "WARNING: Using insecure service account configuration"
fi

prepare_service ${monitor_dir} ${service_dir} apiserver ${APISERVER_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
${apply_uids}
/opt/km apiserver
  --advertise-address=${host_ip}
  --bind-address=0.0.0.0
  --insecure-bind-address=0.0.0.0
  --cloud-config=${cloud_config}
  --cloud-provider=mesos
  --etcd-servers=${etcd_server_list}
  --insecure-port=${apiserver_port}
  --secure-port=${apiserver_secure_port}
  --admission-control=NamespaceLifecycle,LimitRanger,SecurityContextDeny,ServiceAccount,ResourceQuota
  --authorization-mode=AlwaysAllow
  --token-auth-file=/etc/kubernetes/token-users
  --service-cluster-ip-range=${SERVICE_CLUSTER_IP_RANGE:-10.10.10.0/24}
  --service-account-key-file=${apiserver_service_account_key_file}
  --tls-cert-file=${apiserver_tls_cert_file}
  --tls-private-key-file=${apiserver_tls_private_key_file}
  --v=${APISERVER_GLOG_v:-${logv}}
  $(if [ -n "${APISERVER_RUNTIME_CONFIG:-}" ]; then echo "--runtime-config=${APISERVER_RUNTIME_CONFIG}"; fi)
  $(if [ -n "${SERVICE_NODE_PORT_RANGE:-}" ]; then echo "--service-node-port-range=${SERVICE_NODE_PORT_RANGE}"; fi)
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
  --address=0.0.0.0
  --cloud-config=${cloud_config}
  --cloud-provider=mesos
  --master=${kube_master}
  --port=${controller_manager_port}
  --service-account-private-key-file=${controller_service_account_private_key_file}
  --root-ca-file=${controller_root_ca_file}
  --v=${CONTROLLER_MANAGER_GLOG_v:-${logv}}
EOF

# ... after all custom certs/keys have been written to /etc/ssl/overrides
chmod 640 /etc/ssl/override/* || true
chown root:nobody /etc/ssl/override/* || true

#
# prepare secure kubectl configuration
#
#
echo "Preparing secure kubectl configuration"
export KUBECONFIG=/etc/kubernetes/config
# KUBECONFIG determines the file we write to, but it may not exist yet
if [[ ! -e "${KUBECONFIG}" ]]; then
  mkdir -p $(dirname "${KUBECONFIG}")
  touch "${KUBECONFIG}"
fi

kube_context=dcos
"${kubectl}" config set-cluster "${kube_context}" --server="${kube_master}" --certificate-authority="${controller_root_ca_file}"
"${kubectl}" config set-context "${kube_context}" --cluster="${kube_context}" --user="admin"
"${kubectl}" config set-credentials admin --token="${admin_token}"
"${kubectl}" config use-context "${kube_context}" --cluster="${kube_context}"

#
# nginx, proxying the apiserver and serving kubectl binaries
#
sed "s,<PORT>,${apiserver_proxy_port},;s,<APISERVER>,https://localhost:${apiserver_secure_port},;s,<TOKEN>,${admin_token}," /etc/nginx/conf.d/default.conf.template > /etc/nginx/conf.d/default.conf
prepare_service ${monitor_dir} ${service_dir} nginx ${NGINX_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
/usr/sbin/nginx -c /etc/nginx/nginx.conf
EOF

#
# namespace: kube-system
#
cat >${sandbox}/kube-system-ns.yaml <<EOF
kind: "Namespace"
apiVersion: "v1"
metadata:
  name: "kube-system"
  labels:
    name: "kube-system"
EOF

prepare_service ${monitor_dir} ${service_dir} kube_system ${KUBE_SYSTEM_RESPAWN_DELAY:-3} <<EOF
#!/bin/sh
exec 2>&1

export KUBERNETES_MASTER="${kube_master}"

${kubectl} get namespace kube-system >/dev/null && \
  touch kill && exit 0

${kubectl} create -f ${sandbox}/kube-system-ns.yaml
EOF

sed -i -e '$i test -f kill && exec s6-svc -d $(pwd) || exec \\' ${service_dir}/kube_system/finish

#
# add-on: kube_dns
#
prepare_kube_dns() {
  kube_cluster_dns=${DNS_SERVER_IP:-10.10.10.10}
  kube_cluster_domain=${DNS_DOMAIN:-cluster.local}

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

${kubectl} get rc --namespace=kube-system -l k8s-app=kube-dns | grep kube-dns >/dev/null && \
  ${kubectl} get service --namespace=kube-system kube-dns >/dev/null && \
  touch kill && exit 0

${kubectl} create -f ${sandbox}/skydns-rc.yaml
${kubectl} create -f ${sandbox}/skydns-svc.yaml
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
# add-on: kube-ui, deployed as pod and service, later available under
# <apiserver-url>/api/v1/proxy/namespaces/default/services/kube-ui
#
prepare_kube_ui() {
  prepare_service ${monitor_dir} ${service_dir} kube_ui ${KUBE_UI_RESPAWN_DELAY:-3} <<EOF
#!/bin/sh
exec 2>&1

export KUBERNETES_MASTER="${kube_master}"

${kubectl} get rc --namespace=kube-system -l k8s-app=kube-ui | grep -q kube-ui >/dev/null && \
  ${kubectl} get service --namespace=kube-system kube-ui >/dev/null && \
  touch kill && exit 0

${kubectl} create -f /opt/kube-ui-rc.yaml
${kubectl} create -f /opt/kube-ui-svc.yaml
EOF
  sed -i -e '$i test -f kill && exec s6-svc -d $(pwd) || exec \\' ${service_dir}/kube_ui/finish
  apiserver_depends="${apiserver_depends} kube_ui"
}

if [ "${ENABLE_UI:-true}" == true ]; then
  prepare_kube_ui
fi

# create dependency service for all services that need apiserver to be started
if [ -n "${apiserver_depends}" ]; then
  prepare_service_depends apiserver http://127.0.0.1:${apiserver_port}/healthz ok ${apiserver_depends}
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
  --address=0.0.0.0
  --hostname-override=127.0.0.1
  --advertised-address=${scheduler_host}:${scheduler_port}
  --api-servers=${kube_master}
  --driver-port=${scheduler_driver_port}
  --service-address=${SCHEDULER_SERVICE_ADDRESS:-10.10.10.9}
  --etcd-servers=${etcd_server_list}
  --framework-name=${framework_name}
  --framework-weburi=${framework_weburi}
  --mesos-master=${mesos_master}
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
  --mesos-executor-cpus=${SCHEDULER_MESOS_EXECUTOR_CPUS:-0.25}
  --mesos-executor-mem=${SCHEDULER_MESOS_EXECUTOR_MEM:-128}
  --proxy-mode=${PROXY_MODE:-userspace}
  --mesos-framework-roles="${SCHEDULER_MESOS_FRAMEWORK_ROLES:-*}"
  --mesos-default-pod-roles="${SCHEDULER_MESOS_DEFAULT_POD_ROLES:-*}"
  --mesos-sandbox-overlay=/opt/sandbox-overlay.tar.gz
  --mesos-generate-task-discovery=${SCHEDULER_GENERATE_MESOS_TASK_DISCOVERY:-false}
  $(if [ -n "${K8SM_FAILOVER_TIMEOUT:-}" ]; then echo "--failover-timeout=${K8SM_FAILOVER_TIMEOUT}"; fi)
  $(if [ -n "${kube_cluster_dns}" ]; then echo "--cluster-dns=${kube_cluster_dns}"; fi)
  $(if [ -n "${kube_cluster_domain}" ]; then echo "--cluster-domain=${kube_cluster_domain}"; fi)
  $(if [ -n "${MESOS_AUTHENTICATION_PRINCIPAL:-}" ] ; then echo "--mesos-authentication-principal=${MESOS_AUTHENTICATION_PRINCIPAL}"; fi)
  $(if [ -n "${MESOS_AUTHENTICATION_SECRET_FILE:-}" ] ; then echo "--mesos-authentication-secret-file=${MESOS_AUTHENTICATION_SECRET_FILE}"; fi)
EOF

if [ "$ETCD_MESOS_FRAMEWORK_NAME" == disabled ]; then
  prepare_etcd_service
else
  prepare_etcd_proxy
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
