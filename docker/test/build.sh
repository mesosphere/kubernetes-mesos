#!/usr/bin/env bash

set -ex

script_dir=$(cd $(dirname $0) && pwd -P)
cd ${script_dir}

# build docker image
echo "Building kubernetes-mesos-test docker image"
docker build -t mesosphere/kubernetes-mesos-test .
