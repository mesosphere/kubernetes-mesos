#!/bin/sh
if [ -n "$ETCD_DISCOVERY" ]; then
  #
  # use external discovery service
  #
  echo starting etcd with discovery $ETCD_DISCOVERY as peer $HOST:$PORT_2380 listener $HOST:$PORT_2379 http via $PORT_4001
  exec /etcd \
    -initial-advertise-peer-urls http://$HOST:$PORT_2380 \
    -listen-client-urls http://$HOST:$PORT_2379,http://$HOST:$PORT_4001 \
    -advertise-client-urls http://$HOST:$PORT_2379 \
    -listen-peer-urls http://$HOST:$PORT_2380 \
    -discovery "$ETCD_DISCOVERY"
else
  #
  # assume that this will run in Marathon
  #
  
  test -n "$CLUSTER_NAME" || CLUSTER_NAME=k8sm-etcd-1
  
  #
  # etcd DNS discovery depends on a marathon app name "etcd-server",
  # since etcd will look up SRV _etcd-server._tcp.{domain}
  #
  test -n "$CLUSTER_DOMAIN" || CLUSTER_DOMAIN=marathon.mesos
  
  echo starting etcd cluster $CLUSTER_NAME on domain $CLUSTER_DOMAIN as peer $HOST:$PORT_2380 listener $HOST:$PORT_2379 http via $PORT_4001
  
  exec /etcd \
    -discovery-srv $CLUSTER_DOMAIN \
    -initial-advertise-peer-urls http://$HOST:$PORT_2380 \
    -initial-cluster-token $CLUSTER_NAME \
    -initial-cluster-state new \
    -advertise-client-urls http://$HOST:$PORT_2379 \
    -listen-client-urls http://$HOST:$PORT_2379 \
    -listen-peer-urls http://$HOST:$PORT_2380
fi
