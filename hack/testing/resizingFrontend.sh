#!/bin/bash
test -n "$KUBECFG" || KUBECFG=./bin/kubecfg
test -x "$KUBECFG" || {
    echo "error: missing kubecfg executable at $KUBECFG" >&2
    exit 1
}
function ontrap() {
    echo "signal received, exiting $0"
    exit 0
}
trap ontrap INT HUP TERM
echo "starting resizing loop for frontendController"
while true; do
    x=$((RANDOM % 20))
    "$KUBECFG" resize frontendController $(( x + 1 )) > /dev/null
    sleep $(( (RANDOM % 30) + 30 ))
done
