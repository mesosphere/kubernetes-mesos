kubernetes-mesos
================

[Google Kubernetes](https://github.com/GoogleCloudPlatform/kubernetes) on [Apache Mesos](http://mesos.apache.org/)

Disclaimer: Kubernetes-Mesos is pre-release quality, and not yet recommended for production systems.

Status: Mesos integration has now been upstreamed into the Kubernetes repo.
This repo includes: build scripts, docker images, DCOS integration, and docs.

----------------

Kubernetes is an open source container-orchestration project introduced by Google.

Kubernetes itself is deployable to almost any cluster of (virtual or real) machines, in your own datacenter or on third
party infrastructures. With the Mesos integration layer, Kubernetes acts as a Mesos framework, scheduling pods as Mesos
tasks. In addition to integration with vanilla Mesos, Kubernetes can also be installed on [Mesosphere DCOS](https://mesosphere.com/learn/).

Kubernetes Features:
- Group containers into pods
- Service abstraction allows for discovery & load-balancing via pod labels
- Replication Controllers maintain a set number of pod instances

Kubernetes on Mesos Additional Features:
- Auto-Scaling - Kubernetes minion nodes are created automatically, up to the size of the provisioned Mesos cluster.
- Resource Sharing - Co-location of Kubernetes with other popular next-generation services on the same cluster (e.g. Marathon, Spark, Hadoop, Cassandra, etc.)

Kubernetes on DCOS Additional Features:
- High Availability - Kubernetes components themselves run within Marathon, which manages restarting/recreating them if they fail (fail-over NYI)
- Easy Installation - Via the DCOS command-line cluster management tool

The latest Kubernetes DCOS package is based on [Kubernetes-Mesos v0.5.0](https://github.com/mesosphere/kubernetes-mesos/tree/v0.5.0).

Refer to the [documentation pages](docs/README.md) for a review of core concepts, system architecture, and [known issues](docs/issues.md).

Please see the [development documentation](DEVELOP.md) if you are interested in building from source.

### Tutorial

The [getting started guide](https://github.com/GoogleCloudPlatform/kubernetes/blob/master/docs/getting-started-guides/mesos.md)
is in the Kubernetes repo.

The [introductory usage guide](docs/usage.md) is in the docs dir.
