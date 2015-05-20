#!/usr/bin/env bash

set -e

GOPATH=${GOPATH:-~/go}
GO_ARCHIVE_URL=${GO_ARCHIVE_URL:-https://storage.googleapis.com/golang/go1.4.2.linux-amd64.tar.gz}
GO_ARCHIVE=/tmp/$(basename ${GO_ARCHIVE_URL})

echo "Downloading go (${GO_ARCHIVE_URL})..."
mkdir -p $(dirname $GOROOT)
wget -q $GO_ARCHIVE_URL -O $GO_ARCHIVE
echo "Installing go (${GOROOT})..."
tar xf $GO_ARCHIVE -C $(dirname $GOROOT)

if [ ! -d $TMPDIR ]; then
    mkdir -p $TMPDIR
fi

rm -f $GO_ARCHIVE
