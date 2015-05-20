#!/usr/bin/env bash

SUBPROCS=()
SUBPROC_NAMES=()

# Add the pid of a sub-process or return 1 if it is not running
add_subproc () {
    local SUBPROC=$1
    local SUBPROC_NAME="$2"
    echo "Started ${SUBPROC_NAME} (${SUBPROC})"
    if ! ps aux | grep -v grep | grep "${SUBPROC_NAME}" &> /dev/null; then
        echo "Failed to start ${SUBPROC_NAME}"
        return 1
    fi
#    echo "Running ${SUBPROC_NAME} (${SUBPROC})"
    SUBPROCS+=(${SUBPROC})
    SUBPROC_NAME=${SUBPROC_NAME// /_} # replace spaces with underscores (bash arrays are space deliniated)
    SUBPROC_NAMES+=(${SUBPROC_NAME})
}

# Kill subprocesses in reverse order of being started (to avoid log spam)
kill_subprocs () {
    SIGNAL=$1
    echo "Shutting down [km-complete] (Signal: ${SIGNAL})"
    if [ ! -z "${SUBPROCS}" ]; then
        for ((i=${#SUBPROCS[@]}-1; i>=0; i--)); do
            local SUBPROC=${SUBPROCS[i]}
            local SUBPROC_NAME=${SUBPROC_NAMES[i]}
            local SUBPROC_NAME=${SUBPROC_NAME//_/ } # replace underscores with spaces
            if pid_running "${SUBPROC}"; then
                echo "Killing ${SUBPROC_NAME} (${SUBPROC})"
                #TODO(karl): propagate the recieved signal, instead of just TERM
                kill ${SUBPROC} || true
            fi
        done
    fi
    if [ "${SIGNAL}" != "EXIT" ]; then
        # Disable exit trap if we already killed everything in INT/TERM
        trap EXIT
    fi
}

# Check if a process ID is running
pid_running () {
    local CMD_PID=$1
    return $(kill -0 ${CMD_PID} &> /dev/null)
}

# Block until all subprocs have exited
await_subprocs () {
    for subproc in "${SUBPROCS[@]}"; do
        if pid_running "${subproc}"; then
            echo "Waiting on ${subproc}"
            wait ${subproc}
        fi
    done
}
