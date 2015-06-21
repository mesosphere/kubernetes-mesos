#!/usr/bin/env bash

# paths relative to project dir
EXPECTED_SCRIPT_DIR=docker/build
EXPECTED_BINARY_DIR=bin
IMAGE_REPO=mesosphere/kubernetes-mesos-build

generate_image_tag() {
  local imagetag=$(
    source ${project_dir}/hack/lib/version.sh
    export KUBE_ROOT=${project_dir}
    kube::version::get_version_vars
    printf "v%s.%s-dev" $KUBE_GIT_MAJOR $KUBE_GIT_MINOR | tr -d '+'
  )
  test "$imagetag" == "v.-dev" && echo "Failed to determine version from git working directory, aborting" && exit 1
  echo "$imagetag"
}

set -ex

script_dir=$(cd $(dirname $0) && pwd -P)

# defence against lazy refactoring
[[ "$script_dir" != *"$EXPECTED_SCRIPT_DIR" ]] && echo "Build script has moved! Expected location: $EXPECTED_SCRIPT_DIR" && exit 1

project_dir=$(cd "${script_dir}/../.." && pwd -P)
export project_dir
cd ${project_dir}

# update this as needed between releases
IMAGE_TAG=${IMAGE_TAG:-$(generate_image_tag)}

# create temp dir in project dir to avoid permission issues
WORKSPACE=$(env TMPDIR=$PWD mktemp -d -t "k8sm-workspace-XXXXXX")
echo "Workspace created: $WORKSPACE"

cleanup() {
    rm -rf ${WORKSPACE}
    echo "Workspace deleted: $WORKSPACE"
}
trap 'cleanup' EXIT

mkdir ${WORKSPACE}/bin

# setup workspace to mirror project dir (for resources required by the dockerfile)
echo "Setting up workspace"
mkdir -p ${WORKSPACE}/docker/bin
cp ${project_dir}/docker/bin/* ${WORKSPACE}/docker/bin/
mkdir -p ${WORKSPACE}/docker/build/bin
cp ${project_dir}/docker/build/bin/* ${WORKSPACE}/docker/build/bin/

# Dockerfile must be within build context
cp ${project_dir}/${EXPECTED_SCRIPT_DIR}/Dockerfile ${WORKSPACE}/

cd ${WORKSPACE}

# build docker image
echo "Building docker image"
exec docker build -t "${IMAGE_REPO}:${IMAGE_TAG}" .
echo "Built docker image: ${IMAGE_REPO}:${IMAGE_TAG}"
