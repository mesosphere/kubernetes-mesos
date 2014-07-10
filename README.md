kubernetes-mesos
================

When [Google Kubernetes](https://github.com/GoogleCloudPlatform/kubernetes) meets [Apache Mesos](http://mesos.apache.org/)


[![GoDoc] (https://godoc.org/github.com/mesosphere/kubernetes-mesos?status.png)](https://godoc.org/github.com/mesosphere/kubernetes-mesos)

Kubernetes and Mesos are a match made in heaven. Kubernetes enables the Pod (group of containers) abstraction, along with Pod labels for service discovery, load-balancing, and replication control. Mesos provides the fine-grained resource allocations for pods across nodes in a cluster, and can make Kubernetes play nicely with other frameworks running on the same cluster resources. With the Kubernetes framework for Mesos, the framework scheduler will register Kubernetes with Mesos, and then Mesos will begin offering Kubernetes sets of available resources from the cluster nodes (slaves/minions). The framework scheduler will match Mesos' resource offers to Kubernetes pods to run, and then send a launchTasks message to the Mesos master, which will forward the request onto the appropriate slaves. The slave will then fetch the kubelet/executor and the pod/task to run, start running the pod, and send a TASK_RUNNING update back to the Kubernetes framework scheduler. Once the pods are running on the slaves/minions, they can make use of Kubernetes' pod labels to register themselves in etcd for service discovery, load-balancing, and replication control. 

**Roadmap**:
This is still very much a work-in-progress, but stay tuned for updates as we continue development. If you have ideas or patches, feel free to contribute!

1. [ ] Scheduling/launching pods (WIP)
  1. Implement Kube-scheduler API
  1. Pick a Pod (FCFS), match its resources (from PodInfo.Containers) to an offer.
  1. Launch it!
  1. Kubelet as Executor+Containerizer
2. [ ] Pod Labels: for Service Discovery + Load Balancing
3. [ ] Replication Control
4. [ ] Smarter scheduling
  1. Pluggable to reuse existing Kubernetes schedulers
  1. Implement our own!

