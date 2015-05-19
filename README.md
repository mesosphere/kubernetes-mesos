kubernetes-mesos
================

When [Google Kubernetes][2] meets [Apache Mesos][3]

Kubernetes and Mesos are a match made in heaven.
Kubernetes enables the Pod, an abstraction that represents a group of co-located containers, along with Labels for service discovery, load-balancing, and replication control.
Mesos provides the fine-grained resource allocations for pods across nodes in a cluster, and facilitates resource sharing among Kubernetes and other frameworks running on the same cluster.

This is still very much a work-in-progress, but stay tuned for updates as we continue development.
If you have ideas or patches, feel free to contribute!

Refer to the [documentation pages][5] for a review of core concepts, system architecture, and [known issues][6].

The project has been migrated upstream into the offical [Kubernetes repository][2].
Please see the [build documentation][1] if you are interested in building from source.

### Tutorial

Mesosphere maintains a tutorial for running [Kubernetes-Mesos][4], which is periodically updated to coincide with releases of this project.

[1]: DEVELOP.md
[2]: https://github.com/GoogleCloudPlatform/kubernetes
[3]: http://mesos.apache.org/
[4]: https://github.com/GoogleCloudPlatform/kubernetes/blob/master/docs/getting-started-guides/mesos.md
[5]: docs/README.md
[6]: docs/issues.md
