#!/usr/bin/env bash

# Installs etcd into /usr/local/bin

set -ex

ETCD_VERSION=${ETCD_VERSION:-v2.0.11}

full_name=etcd-${ETCD_VERSION}-linux-amd64
archive_url=https://github.com/coreos/etcd/releases/download/${ETCD_VERSION}/${full_name}.tar.gz

download_dir=/tmp/etcd-${ETCD_VERSION}

mkdir ${download_dir}

function cleanup {
    rm -rf ${download_dir}
}

trap cleanup EXIT

cd ${download_dir}

echo "Downloading etcd (${archive_url})..."
curl -s -L ${archive_url} | tar xvz

echo "Installing etcd (/usr/local/bin/etcd)..."
mv ./${full_name}/etcd* /usr/local/bin/
