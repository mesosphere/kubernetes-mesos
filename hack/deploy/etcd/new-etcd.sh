#!/bin/bash
test -n "$CLUSTER_SIZE" || CLUSTER_SIZE=1
d=$(curl -f https://discovery.etcd.io/new?size=$CLUSTER_SIZE) || {
	echo "ERROR: failed to create discovery URL"
	exit 1
}
cat $(dirname "$0")/etcd.json.template | sed \
	-e "s~ETCD_DISCOVERY_VALUE~$d~g" \
	-e "s~CLUSTER_SIZE_VALUE~$CLUSTER_SIZE~g"
