#!/bin/sh
#
# usage: executor.sh {km-executor-parameters...}
#
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
api_server=${KUBERNETES_MASTER:-http://${service_proxy}:8888}

mkdir -p ${log_dir}

PATH=${MESOS_SANDBOX}/bin:$PATH
PATH=${MESOS_SANDBOX}/sbin:$PATH
PATH=${MESOS_SANDBOX}/usr/bin:$PATH
PATH=${MESOS_SANDBOX}/usr/sbin:$PATH
PATH=${MESOS_SANDBOX}/usr/local/bin:$PATH
PATH=${MESOS_SANDBOX}/usr/local/sbin:$PATH
export PATH

#
# If suicide_fd is specified, executor startup should block, waiting for suicide_fd to be read from.
# Upon suicide, executor closes suicidefd.
# A sidecar script blocks, reading from suicidefd; upon close it sends termination signal to root s6 supervisor.
#
# In this service script, we map a FD to the suicide_fifo, expecting that upon startup the
# executor will flip into blocking mode until there is a fifo reader (via redirfd -w n fifo)
#
prepare_service ${monitor_dir} ${service_dir} executor ${EXECUTOR_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
fdreserve 1
redirfd -w -nb $FD0 ${MESOS_SANDBOX}/suicide_fifo
/km executor "${@}"
  --run_proxy=false
  --suicide_fd=$FD0
EOF

#TODO: support these additional parameters at some point for the kube API client; should
# be able to scrape these from the executor args list?
#  --api_version=
#  --client_certificate=
#  --client_key=
#  --certificate_authority=
#  --insecure_skip_tls_verify=
#  --v
#  --master (should come from kubelet --apiservers list)
#
prepare_service ${monitor_dir} ${service_dir} proxy ${PROXY_RESPAWN_DELAY:-3} <<EOF
#!/usr/bin/execlineb
fdmove -c 2 1
/km proxy
  --bind_address=${LIBPROCESS_IP:-0.0.0.0}
  --logtostderr=true
  --master=${KUBERNETES_MASTER}
EOF

#
# TODO: unsure how to handle executor suicide w/ s6 supervision; if the executor exits
# then s6 will spin it back up because the executor exited NOT because of a signal that s6 sent.
# ? should the executor accept an optional fd that it closes upon suicide?
#
# open a fifo for writing, instant success even there is no reader
#   redirfd -w -nb n fifo prog..
#
#   redirfd -w n fifo ...  # (blocks until the fifo is read)
# ... later
#   redirfd -rnb 0 fifo ... # Opening a fifo for reading, with instant success even if there is no writer, and blocking at the first attempt to read from it
#   s6-log -bp t /mnt/tmpfs/uncaught-logs  # (reads from the blocked fifo)
#
# suppose executor accepts a --suicide_fd param
# via..
# fdreserve 2
# multisubstitute
# {
#   importas fdr FD0
#   importas fdw FD1
#  }
# piperw $fdr $fdw
# prog...
#
# If suicidefd is specified, executor startup should block, waiting for suicide_fd to be read from.
# Upon suicide, executor closes suicidefd
#
# Sidecar script blocks, reading from suicidefd; upon close it sends termination signal to root s6 supervisor.
#

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
