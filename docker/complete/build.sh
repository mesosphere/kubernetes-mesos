#!/usr/bin/env bash

EXPECTED_SCRIPT_DIR=docker/complete
EXPECTED_BINARY_DIR=bin

set -ex

script_dir=$(cd $(dirname $0) && pwd -P)

# defence against lazy refactoring
[[ "$script_dir" != *"$EXPECTED_SCRIPT_DIR" ]] && echo "Build script has moved! Expected location: $EXPECTED_SCRIPT_DIR" && exit 1

project_dir=$(cd "${script_dir}/../.." && pwd -P)
cd ${project_dir}

# create temp dir in project dir to avoid permission issues
WORKSPACE=$(env TMPDIR=$PWD mktemp -d -t "k8sm-workspace")
echo "Workspace created: $WORKSPACE"

cleanup() {
    rm -rf ${WORKSPACE}
    echo "Workspace deleted: $WORKSPACE"
}
trap 'cleanup' EXIT

mkdir ${WORKSPACE}/bin

echo "Building kubernetes-mesos binaries"
docker run --rm -v ${WORKSPACE}/bin:/target mesosphere/kubernetes-mesos:build

echo "Binaries produced:"
ls ${WORKSPACE}/bin

[ ! -e ${WORKSPACE}/bin/km ] && echo "Binaries not produced...?" && exit 1

# setup workspace to mirror project dir (for resources required by the dockerfile)
echo "Setting up workspace"
mkdir -p ${WORKSPACE}/docker/bin
cp ${project_dir}/docker/bin/* ${WORKSPACE}/docker/bin/
cp ${project_dir}/docker/mesos-cloud.conf ${WORKSPACE}/docker/

# Dockerfile must be within build context
cp ${project_dir}/${EXPECTED_SCRIPT_DIR}/Dockerfile ${WORKSPACE}/

cd ${WORKSPACE}

# build docker image
echo "Building kubernetes-mesos-complete docker image"
docker build -t mesosphere/kubernetes-mesos-complete .
