#!/bin/sh
#
# usage: executor.sh {km-executor-parameters...}
#
env

sandbox=${MESOS_SANDBOX:-${MESOS_DIRECTORY}}
test -n "$sandbox" || die failed to identify mesos sandbox. neither MESOS_DIRECTORY or MESOS_SANDBOX was specified

execlineb_home=$sandbox
. ${sandbox}/opt/functions.sh

PATH=${sandbox}/bin:$PATH
PATH=${sandbox}/sbin:$PATH
PATH=${sandbox}/usr/bin:$PATH
PATH=${sandbox}/usr/sbin:$PATH
export PATH
LD_LIBRARY_PATH=${sandbox}/lib
export LD_LIBRARY_PATH
#LD_DEBUG=all
#export LD_DEBUG

#TODO(jdef) if I uncomment this then things break, but I don't understand why - is something (the containerizer?)
#messing with the argv list?
#
#test "$1" = "executor" || die Expected executor mode, not \"$1\"
#shift

# executor startup will block until there is a reader on this FIFO, and then
# executor communicates suicide by closing this FIFO
shutdown_fifo=${sandbox}/shutdown_fifo

mkdir -p ${log_dir}

#TODO(jdef): until there is a better way.. skalibs hardcodes the location of this file
test -f /etc/leapsecs.dat || cp etc/leapsecs.dat /etc/
test -d /usr/libexec || mkdir -p /usr/libexec
test -L /usr/libexec/s6lockd-helper || ln -s ${sandbox}/usr/libexec/s6lockd-helper /usr/libexec/

#
# If shutdown_fd is specified, executor startup should block, waiting for shutdown_fd to be read from.
# Upon suicide, executor closes shutdown_fd
# A sidecar script blocks, reading from shutdown_fd; upon close it sends termination signal to root s6 supervisor.
#
# In this service script, we map a FD to the shutdown_fifo, expecting that upon startup the
# executor will flip into blocking mode until there is a fifo reader (via redirfd -w n fifo)
#
prepare_service ${monitor_dir} ${service_dir} executor ${EXECUTOR_RESPAWN_DELAY:-3} <<EOF
#!/bin/sh
exec 2>&1
unset LD_LIBRARY_PATH
exec redirfd -w -nb 3 $shutdown_fifo \\
  ${sandbox}/opt/km executor ${@} \\
    --run_proxy=false \\
    --hostname_override=$LIBPROCESS_IP \\
    --address=$LIBPROCESS_IP \\
    --shutdown_fd=3 \\
    --shutdown_fifo=$shutdown_fifo
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

# if the proxy dies then it will send a shutdown signal to the executor.
# TODO(jdef) probably don't want this behavior but I can't remember why I added it yesterday..
prepare_service ${monitor_dir} ${service_dir} proxy ${PROXY_RESPAWN_DELAY:-3} <<EOF
#!/bin/sh
exec 2>&1
unset LD_LIBRARY_PATH
exec ${sandbox}/opt/km proxy \\
  --bind_address=${LIBPROCESS_IP:-0.0.0.0} \\
  --logtostderr=true \\
  --master=${KUBERNETES_MASTER}
EOF

#
# handle executor suicide w/ s6 supervision; without special handling, if the executor exits then s6 will spin
# it back up because the executor exited (and that's the job of s6)
#
# pattern is this: open a fifo for writing, instant success even there is no reader
#   redirfd -w -nb n fifo prog..
#
#   redirfd -w n fifo ...  # (blocks until the fifo is read)
# ... later
#   redirfd -rnb 0 fifo ... # Opening a fifo for reading, with instant success even if there is no writer, and blocking at the first attempt to read from it
#   s6-log -bp t /mnt/tmpfs/uncaught-logs  # (reads from the blocked fifo)
#
# If shutdown_fd is specified, executor startup should block, waiting for shutdown_fd to be read from.
# Upon suicide, executor closes shutdown_fd
#
# Shutdown script blocks, reading from shutdown_fd; upon close it:
# 1) disables the executor and proxy services
# 2) waits for them to terminate
# 3) sends termination signal to k8s service s6 supervisor.
#
# + root s6 supervisor
# |-- km executor
# |-- km proxy
# |-- shutdown watcher
#

#--- shutdown watcher
# 0) waits for shutdown FIFO
# 1) waits for them to terminate
#
prepare_service_script ${service_dir} shutdown run <<EOF
#!/bin/sh
exec 2>&1
echo awaiting shutdown signal
exec redirfd -rnb 0 $shutdown_fifo foreground cat '' s6-svc -OD ${service_dir}/executor
EOF

# 2) sends termination signal to k8s service s6 supervisor.
# the finish script can only live for 5s at most
prepare_service_script ${service_dir} shutdown finish <<EOF
#!/bin/sh
touch down \\
  ${service_dir}/proxy/down \\
  ${service_dir}/executor/down \\
  ${monitor_dir}/proxy-monitor/down \\
  ${monitor_dir}/executor-monitor/down
echo shutdown finished \$*
s6-svscanctl -t ${monitor_dir} &
s6-svscanctl -t ${service_dir} &
wait
exit 0
EOF

#--- service monitor
#
# (0) subscribe to monitor "up" events
# (1) fork service monitors
# (2) after all monitors have reported "up" once,
# (3) spawn the service tree
#
cd ${sandbox}
mkdir -p init.d/s1

# TODO(jdef) the supervision hierarchy is messed up here: the monitor svscan escapes
# and becomes owned by the slave instead of the top-level "init/s1" svscan
cat <<EOF >init.d/s1/run
#!/bin/sh
die() {
  local rc=\$?
  echo \$* >&2
  exit \$rc
}
s6-ftrig-listen -a \
  ${monitor_dir}/executor-monitor/event U \
  ${monitor_dir}/proxy-monitor/event U \
  '' s6-svscan ${monitor_dir} || die monitoring s6-ftrig-listen exited \$?
exec s6-svscan ${service_dir}
EOF
chmod +x init.d/s1/run

cat <<EOF >init.d/s1/finish
#!/bin/sh
touch down
echo executor terminating \$*
s6-svscanctl -t ${monitor_dir} &
s6-svscanctl -t ${sandbox}/init &
wait
exit 0
EOF
chmod +x init.d/s1/finish

exec s6-svscan init.d
