kubernetes-mesos
================

When [Google Kubernetes](https://github.com/GoogleCloudPlatform/kubernetes) meets [Apache Mesos](http://mesos.apache.org/)


[![GoDoc] (https://godoc.org/github.com/mesosphere/kubernetes-mesos?status.png)](https://godoc.org/github.com/mesosphere/kubernetes-mesos)

**Roadmap**:

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

