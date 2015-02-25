## GuestBook example

This example shows how to build a simple multi-tier web application using Kubernetes and Docker.
The example combines a web frontend, a redis master for storage and a replicated set of redis slaves.

### Step Zero: Prerequisites

This example assumes that you have forked the repository and [turned up a Kubernetes-Mesos cluster](https://github.com/mesosphere/kubernetes-mesos#build).
It also assumes that `${KUBERNETES_MASTER}` is the HTTP URI of the host running the Kubernetes-Mesos framework, which currently hosts the Kubernetes API server (e.g. http://10.2.3.4:8888).

### Step One: Turn up the redis master.

Create a file named `redis-master.json` describing a single pod, which runs a redis key-value server in a container.

```javascript
{
  "id": "redis-master-2",
  "kind": "Pod",
  "apiVersion": "v1beta1",
  "desiredState": {
    "manifest": {
      "version": "v1beta1",
      "id": "redis-master-2",
      "containers": [{
        "name": "master",
        "image": "dockerfile/redis",
        "ports": [{
          "containerPort": 6379,
          "hostPort": 31010
        }]
      }]
    }
  },
  "labels": {
    "name": "redis-master"
  }
}
```

Once you have that pod file, you can create the redis pod in your Kubernetes cluster using `kubectl` or the REST API:

```shell
$ bin/kubectl create -f examples/guestbook/redis-master.json
# -- or --
$ curl ${KUBERNETES_MASTER}/api/v1beta1/pods -XPOST -d@examples/guestbook/redis-master.json
```

Once that's up you can list the pods in the cluster, to verify that the master is running:

```shell
$ bin/kubectl get pods
# -- or --
$ curl ${KUBERNETES_MASTER}/api/v1beta1/pods
```

You'll see a single redis master pod. It will also display the machine that the pod is running on.

```
POD             IP           CONTAINER(S)  IMAGE(S)          HOST                       LABELS             STATUS
redis-master-2  172.17.6.19  master        dockerfile/redis  10.22.211.18/10.22.211.18  name=redis-master  Running
```

If you ssh to that machine, you can run `docker ps` to see the actual pod:

```shell
$ vagrant ssh mesos-2

vagrant@mesos-2:~$ sudo docker ps
CONTAINER ID  IMAGE                    COMMAND               CREATED  STATUS  PORTS                    NAMES
bd1b823fe48d  dockerfile/redis:latest  "redis-server /etc/r  46m ago  Up 46m                           k8s_master.6be2adec_redis-master-2.default.mesos_56a2e404-bd11-11e4-90fa-42010adf71e3_420420b2
de2457a1487a  kubernetes/pause:go      "/pause"              46m ago  Up 46m  0.0.0.0:31010->6379/tcp  k8s_POD.b0f8ea85_redis-master-2.default.mesos_56a2e404-bd11-11e4-90fa-42010adf71e3_fed7f14e
```

(Note that initial `docker pull` may take a few minutes, depending on network conditions.)

### Step Two: Turn up the master service.
A Kubernetes 'service' is a named load balancer that proxies traffic to one or more containers.
The services in a Kubernetes cluster are discoverable inside other containers via environment variables.
Services find the containers to load balance based on pod labels.

The pod that you created in Step One has the label `name=redis-master`.
The selector field of the service determines which pods will receive the traffic sent to the service.
Create a file named `redis-master-service.json` that contains:

```js
{
  "id": "redismaster",
  "kind": "Service",
  "apiVersion": "v1beta1",
  "port": 10000,
  "selector": {
    "name": "redis-master"
  }
}
```

This will cause all pods to see the redis master apparently running on `localhost:10000`.

Once you have that service description, you can create the service with the REST API:

```shell
$ bin/kubectl create -f examples/guestbook/redis-master-service.json
# -- or --
$ curl ${KUBERNETES_MASTER}/api/v1beta1/services -XPOST -d@examples/guestbook/redis-master-service.json
```

Observe that the service has been created.
```
$ bin/kubectl get services
NAME            LABELS                                    SELECTOR            IP            PORT
kubernetes      component=apiserver,provider=kubernetes   <none>              10.10.10.2    443
kubernetes-ro   component=apiserver,provider=kubernetes   <none>              10.10.10.1    80
redismaster     <none>                                    name=redis-master   10.10.10.49   10000
```

Once created, the service proxy on each minion is configured to set up a proxy on the specified port (in this case port 10000).

### Step Three: Turn up the replicated slave pods.
Although the redis master is a single pod, the redis read slaves are a 'replicated' pod.
In Kubernetes a replication controller is responsible for managing multiple instances of a replicated pod.

Create a file named `redis-slave-controller.json` that contains:

```js
{
  "id": "redis-slave-controller",
  "kind": "ReplicationController",
  "apiVersion": "v1beta1",
  "desiredState": {
    "replicas": 2,
    "replicaSelector": {"name": "redisslave"},
    "podTemplate": {
      "desiredState": {
         "manifest": {
           "version": "v1beta1",
           "id": "redis-slave-controller",
           "containers": [{
             "name": "slave",
             "image": "jdef/redis-slave",
             "ports": [{"containerPort": 6379, "hostPort": 31020}]
           }]
         }
       },
       "labels": {"name": "redisslave"}
      }},
  "labels": {"name": "redisslave"}
}
```

Then you can create the service by running:

```shell
$ bin/kubectl create -f examples/guestbook/redis-slave-controller.json
# -- or --
$ curl ${KUBERNETES_MASTER}/api/v1beta1/replicationControllers -XPOST -d@examples/guestbook/redis-slave-controller.json
```
```
$ bin/kubectl get rc
CONTROLLER               CONTAINER(S)    IMAGE(S)            SELECTOR            REPLICAS
redis-slave-controller   slave           jdef/redis-slave    name=redisslave     2
```

The redis slave configures itself by looking for the Kubernetes service environment variables in the container environment.
In particular, the redis slave is started with the following command:

```shell
redis-server --slaveof $SERVICE_HOST $REDISMASTER_SERVICE_PORT
```

Once that's up you can list the pods in the cluster, to verify that the master and slaves are running:

```shell
$ bin/kubectl get pods
POD                            IP             CONTAINER(S)   IMAGE(S)            HOST                        LABELS              STATUS
redis-master-2                 172.17.6.19    master         dockerfile/redis    10.22.211.18/10.22.211.18   name=redis-master   Running
redis-slave-controller-0133o   172.17.9.2     slave          jdef/redis-slave    10.150.52.19/10.150.52.19   name=redisslave     Running
redis-slave-controller-oh43e   172.17.82.98   slave          jdef/redis-slave    10.72.72.178/10.72.72.178   name=redisslave     Running
```

You will see a single redis master pod and two redis slave pods.

### Step Four: Create the redis slave service.

Just like the master, we want to have a service to proxy connections to the read slaves.
In this case, in addition to discovery, the slave service provides transparent load balancing to clients.
As before, create a service specification:

```js
{
  "id": "redisslave",
  "kind": "Service",
  "apiVersion": "v1beta1",
  "port": 10001,
  "labels": {
    "name": "redisslave"
  },
  "selector": {
    "name": "redisslave"
  }
}
```

This time the selector for the service is `name=redisslave`, because that identifies the pods running redis slaves.
It may also be helpful to set labels on your service itself--as we've done here--to make it easy to locate them with:

* `bin/kubectl get services -l "name=redisslave"`, or
* `curl ${KUBERNETES_MASTER}/api/v1beta1/services?labels=name=redisslave`

Now that you have created the service specification, create it in your cluster via the REST API:

```shell
$ bin/kubectl create -f examples/guestbook/redis-slave-service.json
# -- or --
$ curl ${KUBERNETES_MASTER}/api/v1beta1/services -XPOST -d@examples/guestbook/redis-slave-service.json

$ bin/kubectl get services
NAME            LABELS                                    SELECTOR            IP            PORT
kubernetes      component=apiserver,provider=kubernetes   <none>              10.10.10.2    443
kubernetes-ro   component=apiserver,provider=kubernetes   <none>              10.10.10.1    80
redismaster     <none>                                    name=redis-master   10.10.10.49   10000
redisslave      name=redisslave                           name=redisslave     10.10.10.109  10001
```

### Step Five: Create the frontend pod.

This is a simple PHP server that is configured to talk to either the slave or master services depending on whether the request is a read or a write.
It exposes a simple AJAX interface and serves an angular-based UX.
Like the redis read slaves it is a replicated service instantiated by a replication controller.

Create a file named `frontend-controller.json`:

```js
{
  "id": "frontend-controller",
  "kind": "ReplicationController",
  "apiVersion": "v1beta1",
  "desiredState": {
    "replicas": 3,
    "replicaSelector": {"name": "frontend"},
    "podTemplate": {
      "desiredState": {
         "manifest": {
           "version": "v1beta1",
           "id": "frontend-controller",
           "containers": [{
             "name": "php-redis",
             "image": "jdef/php-redis",
             "ports": [{"containerPort": 80, "hostPort": 31030}]
           }]
         }
       },
       "labels": {"name": "frontend"}
      }},
  "labels": {"name": "frontend"}
}
```

With this file, you can turn up your frontend with:

```shell
$ bin/kubectl create -f examples/guestbook/frontend-controller.json
# -- or --
$ curl ${KUBERNETES_MASTER}/api/v1beta1/replicationControllers -XPOST -d@examples/guestbook/frontend-controller.json

$ bin/kubectl get rc
CONTROLLER               CONTAINER(S)    IMAGE(S)            SELECTOR            REPLICAS
frontend-controller      php-redis       jdef/php-redis      name=frontend       3
redis-slave-controller   slave           jdef/redis-slave    name=redisslave     2
```

Once that's up you can list the pods in the cluster, to verify that the master, slaves and frontends are running:

```shell
$ bin/kubectl get pods
POD                            IP             CONTAINER(S)  IMAGE(S)          HOST                       LABELS             STATUS
frontend-controller-hh2gd      172.17.9.3     php-redis     jdef/php-redis    10.150.52.19/10.150.52.19  name=frontend      Running
frontend-controller-ls6k1      172.17.6.21    php-redis     jdef/php-redis    10.22.211.18/10.22.211.18  name=frontend      Running
frontend-controller-nyxxv      172.17.82.99   php-redis     jdef/php-redis    10.72.72.178/10.72.72.178  name=frontend      Running
redis-master-2                 172.17.6.19    master        dockerfile/redis  10.22.211.18/10.22.211.18  name=redis-master  Running
redis-slave-controller-0133o   172.17.9.2     slave         jdef/redis-slave  10.150.52.19/10.150.52.19  name=redisslave    Running
redis-slave-controller-oh43e   172.17.82.98   slave         jdef/redis-slave  10.72.72.178/10.72.72.178  name=redisslave    Running
```

You will see a single redis master pod, two redis slaves, and three frontend pods.

The code for the PHP service looks like this:

```php
<?
set_include_path('.:/usr/share/php:/usr/share/pear:/vendor/predis');

error_reporting(E_ALL);
ini_set('display_errors', 1);

require 'predis/autoload.php';

if (isset($_GET['cmd']) === true) {
  header('Content-Type: application/json');
  if ($_GET['cmd'] == 'set') {
    $client = new Predis\Client([
      'scheme' => 'tcp',
      'host'   => getenv('SERVICE_HOST'),
      'port'   => getenv('REDISMASTER_SERVICE_PORT'),
    ]);
    $client->set($_GET['key'], $_GET['value']);
    print('{"message": "Updated"}');
  } else {
    $read_port = getenv('REDISMASTER_SERVICE_PORT');

    if (isset($_ENV['REDISSLAVE_SERVICE_PORT'])) {
      $read_port = getenv('REDISSLAVE_SERVICE_PORT');
    }
    $client = new Predis\Client([
      'scheme' => 'tcp',
      'host'   => getenv('SERVICE_HOST'),
      'port'   => $read_port,
    ]);

    $value = $client->get($_GET['key']);
    print('{"data": "' . $value . '"}');
  }
} else {
  phpinfo();
} ?>
```

To play with the service itself, find the IP address of a Mesos slave that is running a frontend pod and visit `http://<host-ip>:31030`.
```shell
# You'll actually want to interact with this app via a browser but you can test its access using curl:
$ curl http://10.22.211.18:31030
<html ng-app="redis">
  <head>
    <title>Guestbook</title>
...
```

For a list of Mesos tasks associated with the Kubernetes pods, you can use the Mesos CLI interface:

```shell
$ mesos-ps
   TIME   STATE    RSS     CPU   %MEM  COMMAND  USER                   ID
 0:00:22    R    30.49 MB  0.5  47.64    none   root  718d395f-bd1d-11e4-ba12-42010adf71e3
 0:00:22    R    30.82 MB  0.5  48.16    none   root  718d24ec-bd1d-11e4-ba12-42010adf71e3
 0:00:59    R    31.50 MB  0.5  49.22    none   root  718a6728-bd1d-11e4-ba12-42010adf71e3
 0:00:22    R    30.82 MB  0.5  48.16    none   root  9497635c-bd1c-11e4-ba12-42010adf71e3
 0:00:22    R    30.49 MB  0.5  47.64    none   root  9494dfe7-bd1c-11e4-ba12-42010adf71e3
 0:00:59    R    31.50 MB  0.5  49.22    none   root  56cb0f99-bd11-11e4-ba12-42010adf71e3
```

Or for more details, you can use the Mesos REST API (assuming that the Mesos master is running on `$servicehost`):

```shell
$ curl http://${servicehost}:5050/master/state.json
{
    "activated_slaves":3,
...
            "tasks": [
                {
                    "executor_id": "KubeleteExecutorID",
                    "name": "Pod",
                    "framework_id": "20150221-134408-3815890698-5050-18127-0016",
                    "state": "TASK_RUNNING",
                    "statuses": [
                        {
                            "timestamp": 1424889489,
                            "state": "TASK_STARTING"
                        },
                        {
                            "timestamp": 1424889490,
                            "state": "TASK_RUNNING"
                        }
                    ],
                    "slave_id": "20150221-132953-3815890698-5050-10694-S0",
                    "id": "718d24ec-bd1d-11e4-ba12-42010adf71e3",
                    "resources": {
                        "mem": 64,
                        "disk": 0,
                        "cpus": 0.25,
                        "ports": "[31030-31030]"
                    }
                },
...
    "slaves": [
        {
            "hostname": "10.22.211.18",
            "pid": "slave(1)@10.22.211.18:5051",
            "attributes": {
                "host": "development-3273-173.c.k8s-mesos.internal"
            },
            "registered_time": 1424526249.50596,
            "reregistered_time": 1424526249.50769,
            "id": "20150221-132953-3815890698-5050-10694-S1",
            "resources": {
                "mem": 6475,
                "disk": 4974,
                "cpus": 2,
                "ports": "[31000-32000]"
            }
        },
...
```
