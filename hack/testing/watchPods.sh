#!/bin/bash
test -n "$KUBECFG" || KUBECFG=./bin/kubectl
test -x "$KUBECFG" || {
    echo "error: missing kubectl executable at $KUBECFG" >&2
    exit 1
}
watch "$KUBECFG"' get pods | (
    read l1
    read l2
    echo "$l1"
    echo "$l2"
    cat | sed -e "/^\$/d" | sort -k3 -k5
)'
