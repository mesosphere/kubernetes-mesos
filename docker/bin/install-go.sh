#!/usr/bin/env bash

# Installs go into /usr/local/go/bin
# To enable: export PATH=$PATH:/usr/local/go/bin

set -ex

GO_VERSION=${GO_VERSION:-1.4.2}

archive_root=go${GO_VERSION}.linux-amd64
archive_url=https://storage.googleapis.com/golang/${archive_root}.tar.gz

cd /usr/local

if [ -d ./go ]; then
    echo "Uninstalling Go..."
    rm -rf ./go
fi

echo "Downloading Go..."
wget -nv ${archive_url} -O ${archive_root}.tar.gz

echo "Installing Go..."
tar xf ${archive_root}.tar.gz
