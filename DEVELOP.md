Development of Kubernetes-Mesos
==

* [Prerequisites](#prerequisites)
* [Build](README.md#build) the framework
* [Testing](#testing)

### Prerequisites
To get started with development you'll need to install some prerequisites:
* Install Mesos, C++, and some configuration management tools
* Install protobuf and Go
* Install [godep][2]

#### Debian

```shell
$ export DEB_VERSION_MESOS=0.20.1-1.0.debian75
$ apt-key adv --keyserver keyserver.ubuntu.com --recv E56151BF &&
    echo "deb http://repos.mesosphere.io/debian wheezy main" |
          tee /etc/apt/sources.list.d/mesosphere.list &&
    apt-get -y update &&
    apt-get -y install mesos=$DEB_VERSION_MESOS g++ make curl mercurial git

$ curl -L https://protobuf.googlecode.com/files/protobuf-2.5.0.tar.gz |
  tar xz && (cd protobuf-2.5.0/ && ./configure --prefix=/usr && make &&
    make install)

$ (cd /usr/local &&
  curl -L https://storage.googleapis.com/golang/go1.3.3.linux-amd64.tar.gz |
    tar xvz)

$ export PATH=/usr/local/go/bin:$PATH

$ mkdir -pv /opt && (export GOPATH=/opt; cd /opt &&
  go get github.com/tools/godep &&
  ln -sv /opt/bin/godep /usr/local/bin/godep)
```

#### Ubuntu
```shell
$ DISTRO=$(lsb_release -is | tr '[:upper:]' '[:lower:]')
$ CODENAME=$(lsb_release -cs)
$ apt-key adv --keyserver keyserver.ubuntu.com --recv E56151BF &&
    echo "deb http://repos.mesosphere.io/${DISTRO} ${CODENAME} main" |
          tee /etc/apt/sources.list.d/mesosphere.list &&
    apt-get -y update &&
    apt-get -y install mesos g++ make curl mercurial git golang libprotobuf-dev

$ mkdir -pv /opt && (export GOPATH=/opt; cd /opt &&
  go get github.com/tools/godep &&
  ln -sv /opt/bin/godep /usr/local/bin/godep)
```

#### CentOS 6
```shell
$ rpm -Uvh http://repos.mesosphere.io/el/6/noarch/RPMS/mesosphere-el-repo-6-2.noarch.rpm &&
    rpm -Uvh http://mirrors.einstein.yu.edu/epel/6/i386/epel-release-6-8.noarch.rpm &&
    yum -y update &&
    yum -y install mesos git mercurial curl gcc gcc-c++ tar golang which

$ curl -L https://protobuf.googlecode.com/files/protobuf-2.5.0.tar.gz |
    tar xz && (cd protobuf-2.5.0/ && ./configure --prefix=/usr && make &&
    make install)

$ mkdir -pv /opt && (export GOPATH=/opt; cd /opt &&
    go get github.com/tools/godep &&
    ln -sv /opt/bin/godep /usr/local/bin/godep)
```

### Testing

There is a Makefile target called `test` that will execute unit tests for packages that have them.
```shell
$ make test
```

[1]: https://github.com/mesosphere/kubernetes-mesos#build
[2]: https://github.com/tools/godep
