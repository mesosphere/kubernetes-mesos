#!/usr/bin/env bash

set -ex

script_dir=$(cd $(dirname $0) && pwd -P)
cd ${script_dir}

cp ../bin/resolveip .
trap "rm resolveip" EXIT

# build docker image
echo "Building kubernetes-mesos-slave docker image"
docker build -t mesosphere/kubernetes-mesos-slave .
