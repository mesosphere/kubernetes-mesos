#!/bin/bash -v
test -n "$KUBECFG" || KUBECFG=./bin/kubecfg
test -x "$KUBECFG" || {
    echo "error: missing kubecfg executable at $KUBECFG" >&2
    exit 1
}

test -n "$servicehost" || servicehost=$(hostname -i|cut -f1 -d' ')
test -n "$servicehost" || {
    echo "failed to determine service host" >&2
    exit 1
}

ts=$(date +'%Y%m%d%H%M%S')

for prof in heap block; do
    curl http://${servicehost}:10251/debug/pprof/$prof >framework.$ts.$prof
    minions=$($KUBECFG list minions|sed -e '1,2d' -e '/^$/d')
    for m in $minions; do
        curl http://${m}:10250/debug/pprof/$prof >minion.${m}.$ts.$prof
    done
done
