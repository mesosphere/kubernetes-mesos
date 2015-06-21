#!/usr/bin/env bash

# Installs gcloud to /usr/local/google-cloud-sdk/bin
# To enable: export PATH=$PATH:/usr/local/google-cloud-sdk/bin

set -ex

archive_url=https://dl.google.com/dl/cloudsdk/release/google-cloud-sdk.tar.gz

cd /usr/local

if [ -d ./google-cloud-sdk ]; then
    echo "Uninstalling Google Cloud SDK..."
    rm -rf ./google-cloud-sdk
fi

echo "Downloading Google Cloud SDK..."
curl -s -L ${archive_url} | tar xvz

echo "Installing Google Cloud SDK..."
./google-cloud-sdk/install.sh

source ~/.bashrc
gcloud info
gcloud config set --scope=user disable_usage_reporting true
