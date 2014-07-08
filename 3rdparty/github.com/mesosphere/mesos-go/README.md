Go bindings for Apache Mesos
========

Early Go language bindings for Apache Mesos.

## Install instructions

ssh into your current leading master node.
Record the IP of the HDFS name node to publish your built executor.

### Install build dependencies

Install protoc-gen-go, you will also need protobuf, protobuf-c, gcc and g++

```bash
go get code.google.com/p/goprotobuf/{proto,protoc-gen-go}
```

### Fetch and compile example framework and executor

```bash
go get github.com/mesosphere/mesos-go/example_framework
go get github.com/mesosphere/mesos-go/example_executor
```

### Use library itself

```bash
go get github.com/mesosphere/mesos-go/mesos
```

### Install example executor in HDFS

```bash
hdfs dfs -mkdir /go-tmp
hdfs dfs -put $GOPATH/bin/example_executor /go-tmp
```

### Run example framework:

```bash
$ cd $GOPATH
$ ./bin/example_framework -executor-uri hdfs://<hdfs-name-node>/go-tmp/example_executor
Launching task: 5
Received task status: Go task is running!
Received task status: Go task is done!
Launching task: 4
Received task status: Go task is running!
Received task status: Go task is done!
Launching task: 3
Received task status: Go task is running!
Received task status: Go task is done!
Launching task: 2
Received task status: Go task is running!
Received task status: Go task is done!
Launching task: 1
Received task status: Go task is running!
Received task status: Go task is done!
```

