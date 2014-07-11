#!/bin/bash

echo "Building executor_runner"
cd executor_runner
go build -v

echo
echo "Building framework_runner"
cd ../framework_runner
go build -v
