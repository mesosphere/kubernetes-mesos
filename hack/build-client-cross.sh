#!/bin/bash

# Copyright 2014 The Kubernetes Authors All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# -----------------------------------------------------------------------------
# Cross-compiles the kubectl client binaries and compresses them into platform specific tarballs.
#
# Assumes GOPATH has already been set up.
# Assumes all source is in the GOPATH.
# Builds into `../_output`.
#
# Use `make client-cross` to handle the GOPATH.
# TODO: move gopath env setup into this file, using lib scripts in k8s master
# Run in mesosphere/kubernetes-mesos-build docker image for best results.

set -o errexit
set -o nounset
set -o pipefail

echo "Using GOPATH: ${GOPATH}"

KUBE_ROOT=$(cd $(dirname "${BASH_SOURCE}")/.. && pwd)

source "${KUBE_ROOT}/hack/lib/version.sh"

readonly KUBE_GO_PACKAGE=github.com/GoogleCloudPlatform/kubernetes

readonly LOCAL_OUTPUT_ROOT="${KUBE_ROOT}/_output"
readonly LOCAL_OUTPUT_BINPATH="${LOCAL_OUTPUT_ROOT}/bin"
readonly RELEASE_DIR="${LOCAL_OUTPUT_ROOT}/release-tars"

readonly KUBE_CLIENT_PLATFORMS=(
  linux/amd64
  darwin/amd64
)

# The set of client targets that we are building for all platforms
readonly KUBE_CLIENT_TARGETS=(
  cmd/kubectl
)
readonly KUBE_CLIENT_BINARIES=("${KUBE_CLIENT_TARGETS[@]##*/}")

kube::version::get_version_vars

echo "Building version ${KUBE_GIT_VERSION}"

echo "Building client binaries"

for platform in "${KUBE_CLIENT_PLATFORMS[@]}"; do
  # export env for go build
  GOOS=${platform%/*}
  GOARCH=${platform##*/}

  # Fetch the version.
  version_ldflags=$(kube::version::ldflags)

  for target in "${KUBE_CLIENT_TARGETS[@]}"; do
    binary=${target##*/}
    dest_dir="${LOCAL_OUTPUT_BINPATH}/${GOOS}/${GOARCH}"
    env GOPATH=${GOPATH} GOOS=${GOOS} GOARCH=${GOARCH} CGO_ENABLED=0 \
      go build \
        -ldflags "-extldflags '-static' ${version_ldflags}" \
        -o "${dest_dir}/${binary}" \
        "${KUBE_GO_PACKAGE}/${target}"
  done
done

echo "Compressing client binaries"

mkdir -p "${RELEASE_DIR}"

for platform in "${KUBE_CLIENT_PLATFORMS[@]}"; do
  GOOS=${platform%/*}
  GOARCH=${platform##*/}
  for binary in "${KUBE_CLIENT_BINARIES[@]}"; do
    cd "${LOCAL_OUTPUT_BINPATH}/${GOOS}/${GOARCH}"
    dest="${RELEASE_DIR}/${binary}-${KUBE_GIT_VERSION}-${GOOS}-${GOARCH}.tgz"
    tar -cvz -f "${dest}" "${binary}"
    echo "Created: ${dest}"
  done
done
