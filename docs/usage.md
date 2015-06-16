### Intro to Kubernetes Usage

This guide will tell you how to create, access, and delete a Kubernetes pod & service.

For the most part, this process is identical to using vanilla Kubernetes.

See the [README](../README.md) for more general information about Kubernetes-Mesos.

- [Deploy Kubernetes-Mesos](#deploy)
- [Configure the Kubernetes Endpoint](#config)
- [Create a Kubernetes Pod Definition](#pod)
- [Create a Kubernetes Service Definition](#service)
- [Launch a Kubernetes Pod via REST API](#api)
- [Launch a Kubernetes Pod via kubectl](#kubectl)
- [View Usage Metrics](#metrics)

<a name="deploy"></a>
#### Deploy Kubernetes-Mesos

See the [getting started guide](https://github.com/GoogleCloudPlatform/kubernetes/blob/master/docs/getting-started-guides/mesos.md)
for instructions on how to deploy Kubernetes as a Mesos framework.

If you're using the bleeding edge (HEAD of master branch) or some other unreleased Kubernetes source you'll need to have
the source code checked out locally.

Example (HEAD of master):

```
git clone https://github.com/GoogleCloudPlatform/kubernetes
cd kubernetes
```

Otherwise, just create a directory to use as a temporary workspace:

```
mkdir kubernetes
cd kubernetes
```

<a name="config"></a>
#### Configure the Kubernetes Endpoint

The Kubernetes CLI (kubectl) can be configured in more complex ways, but the simplest is to define the
`KUBERNETES_MASTER` environment variable as the URL to the Kubernetes API Server. Our manual REST api calls will also
use `KUBERNETES_MASTER` for consistency.

In most cases, the Kubernetes API Server should be accessible via IP and port:

```
export KUBERNETES_MASTER=http://<ip>:<port>/api
```

On DCOS, the Kubernetes API Server is proxied through the Mesos Master:

```
export KUBERNETES_MASTER=http://<hostname>/service/kubernetes/api
```

<a name="pod"></a>
#### Create a Kubernetes Pod Definition

A [pod](https://github.com/GoogleCloudPlatform/kubernetes/blob/master/docs/pods.md) is one or more containers that get
co-located on the same host, sharing a common network stack (IP & ports). In this example we’re creating a pod with one
container, which runs nginx.

1. Download the [example nginx pod definition](/examples/pod-nginx.json):

    ```
    mkdir -p examples
    curl -o examples/pod-nginx.json \
      https://raw.githubusercontent.com/mesosphere/kubernetes-mesos/master/examples/pod-nginx.json
    ```

    (If not using the master branch, the above should be modified to match your current branch.)

<a name="service"></a>
#### Create a Kubernetes Service Definition

A [Kubernetes service](https://github.com/GoogleCloudPlatform/kubernetes/blob/master/docs/services.md) (not to be
confused with a [DCOS service](https://docs.mesosphere.com/services/)) is an abstraction that creates a named proxy
endpoint for load balancing multiple similar pods. It can also be used to proxy to an external service. In this case,
the example service creates a proxy to all pods that have the label `"name"="nginx"`.

1. Download the [example nginx service definition](/examples/service-nginx.json):

    ```
    mkdir -p examples
    curl -o examples/service-nginx.json \
      https://raw.githubusercontent.com/mesosphere/kubernetes-mesos/master/examples/service-nginx.json
    ```

    (If not using the master branch, the above should be modified to match your current branch.)

<a name="api"></a>
#### Launch a Kubernetes Pod via REST API

The REST API is simple enough to use without a complex client. Here, we demo a few of the basic commands.

1. Create an nginx pod:

    ```
    curl $KUBERNETES_MASTER/v1beta3/namespaces/default/pods -XPOST -H'Content-type: json' -d@examples/pod-nginx.json
    ```

1. List all created Kubernetes pods:

    ```
    curl $KUBERNETES_MASTER/v1beta3/namespaces/default/pods
    ```

1. Create an nginx service:

    ```
    curl $KUBERNETES_MASTER/v1beta3/namespaces/default/services -XPOST -H'Content-type: json' -d@examples/service-nginx.json
    ```

1. List all created Kubernetes services:

    ```
    curl $KUBERNETES_MASTER/v1beta3/namespaces/default/services
    ```

1. Use the service through the API server proxy (requires kube-proxy):

    ```
    curl $KUBERNETES_MASTER/v1beta3/proxy/namespaces/default/services/nginx
    ```

1. Delete the nginx service:

    ```
    curl $KUBERNETES_MASTER/v1beta3/namespaces/default/services/nginx -XDELETE
    ```

1. Delete the nginx pod:

    ```
    curl $KUBERNETES_MASTER/v1beta3/namespaces/default/pods/nginx-id-01 -XDELETE
    ```

<a name="kubectl"></a>
#### Launch a Kubernetes Pod via kubectl

kubectl is a command-line tool for interacting with the Kubernetes API Server. It lets you enter simple commands and
translates to/from the REST API.

1. Install kubectl:
    kubectl can be installed anywhere on your PATH. In this case, we're temporarily installing to the current directory.

    Create a `bin` dir to install to and add it to your PATH:

    ```
    mkdir -p bin
    export PATH=$(pwd)/bin:$PATH
    ```

    (For a more permanent installation, copy the kubectl binary to `/usr/local/bin/` instead.)

    - If you're using a released version of Kubernetes:
        Download the kubectl binary [specific to that release](https://github.com/mesosphere/kubernetes-mesos/releases),
        appropriate for your OS and architecture. For example, v0.5.0 release binaries can be found on the
        [v0.5.0 release tag](https://github.com/mesosphere/kubernetes-mesos/releases/tag/v0.5.0).

        Example (v0.5.0, Mac OS X, 64-bit):

        ```
        curl -L https://github.com/mesosphere/kubernetes-mesos/releases/download/v0.5.0/kubectl-v0.5.0-darwin-amd64.tgz | tar -xz -C bin
        ```

    - If you're using an unreleased or modified version of Kubernetes:
        Build your own binary from source.

        Example (HEAD of master, Linux, 64-bit):

        ```
        export KUBERNETES_CONTRIB=mesos
        make
        ln -s kubernetes/_output/local/bin/linux/amd64/kubectl bin/
        ```

        (The above creates a symlink to the binary so that you can rebuild without having to re-install.)


1. Create an nginx pod:

    ```
    kubectl create -f examples/pod-nginx.json
    ```

1. List all created Kubernetes pods:

    ```
    kubectl get pods
    ```

1. Create an nginx service:

    ```
    kubectl create -f examples/service-nginx.json
    ```

1. List all created Kubernetes services:

    ```
    kubectl get services
    ```

1. Use the service through the API server proxy:

    ```
    curl $KUBERNETES_MASTER/v1beta3/proxy/namespaces/default/services/nginx
    ```

1. Delete the nginx service:

    ```
    kubectl delete service nginx
    ```

1. Delete the nginx pod:

    ```
    kubectl delete pod nginx-id-01
    ```

For more on how to use kubectl, see the [kubectl documentation](https://github.com/GoogleCloudPlatform/kubernetes/blob/master/docs/kubectl.md).

<a name="metrics"></a>
#### View Usage Metrics

The Kubernetes API Server also provides usage metrics at `http://<ip>:<port>/metrics`.

For DCOS it's `http://<hostname>/service/kubernetes/metrics`.