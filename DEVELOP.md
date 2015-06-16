Development of Kubernetes-Mesos
==

Development of all Kubernetes-Mesos components has been pushed upstream.
Please refer to the [development documentation][1] of the Kubernetes project for general environment setup.

The core upstream build and test scripts do not build Kubernetes-Mesos by default.
To enable this capability you must add the following to your environment:
```shell
KUBERNETES_CONTRIB=mesos
```

You should now be able to `make` or `./hack/build-go.sh` to generate the Kubernetes-Mesos binaries.

[1]: https://github.com/GoogleCloudPlatform/kubernetes/blob/master/docs/devel/development.md
