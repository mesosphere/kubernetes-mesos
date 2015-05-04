Development of Kubernetes-Mesos
==

* [Prerequisites](#prerequisites)
* [Build](README.md#build) the framework
* [Testing](#testing)
* [Profiling](#profiling)

### Prerequisites
To get started with development you'll need to install some prerequisites.
See the following sections for distro-specific instructions.
* Go and some configuration management tools
* [godep][2]

The following distro-specific steps for installing prerequisites assume that you are logged in as `root`, otherwise you will need to insert `sudo` where appropriate.

#### Mac OS X

1. Install golang 1.4.2+ (usually via installer) via https://golang.org/dl/
  1. Make a go workspace with `mkdir ~/go`
  1. Add go environment variables to `~/.bash_profile`: 
    
    ```bash
    export GOPATH=~/go
    export PATH=$PATH:~/go/bin
    ```
      
  1. Update current terminal with `source ~/.bash_profile` (or open a new terminal)
1. Install homebrew via http://brew.sh/
1. Install mercurial with `brew install hg`
1. Install bash 4+ with `brew install bash`
1. Install coreutils with `brew install coreutils`

(Tested on Mac OS 10.10.2)

#### Debian

```shell
$ apt-get -y install g++ make curl mercurial git

$ (cd /usr/local &&
  curl -L https://storage.googleapis.com/golang/go1.4.2.linux-amd64.tar.gz |
    tar xvz)

$ export PATH=/usr/local/go/bin:$PATH

$ mkdir -pv /opt && (export GOPATH=/opt; cd /opt &&
  go get github.com/tools/godep &&
  ln -sv /opt/bin/godep /usr/local/bin/godep)
```

#### Ubuntu
```shell
$ apt-get -y install g++ make curl mercurial git golang

$ mkdir -pv /opt && (export GOPATH=/opt; cd /opt &&
  go get github.com/tools/godep &&
  ln -sv /opt/bin/godep /usr/local/bin/godep)
```

#### CentOS 6
```shell
$ rpm -Uvh http://mirrors.einstein.yu.edu/epel/6/i386/epel-release-6-8.noarch.rpm &&
    yum -y install git mercurial curl tar golang which gcc gcc-c++

$ mkdir -pv /opt && (export GOPATH=/opt; cd /opt &&
    go get github.com/tools/godep &&
    ln -sv /opt/bin/godep /usr/local/bin/godep)
```

### Testing

There is a Makefile target called `test` that will execute unit tests for packages that have them.
```shell
$ make test
```

### Profiling

The project Makefile supports the inclusion of arbitrary Go build flags.
To generate a build with profiling enabled, include `TAGS=profile`.
If you're not using the Makefile then you must supply `-tags 'profile'` as part of your `go build` command.
If you're using the dockerbuilder then read the [instructions][3] for profiling in the accompanying documentation.

```shell
$ make TAGS=profile
```

Profiling, when enabled, is supported for both the `kubernetes-mesos` (framework) and `kubernetes-executor` binaries:
```shell
$ ts=$(date +'%Y%m%d%H%M%S')
$ curl http://${servicehost}:10251/debug/pprof/heap >framework.heap.$ts
$ curl http://10.132.189.${slave}:10250/debug/pprof/heap >${slave}.heap.$ts
```

If you have captured two or more heaps it's trivial to use the Go pprof tooling to generate reports:
```shell
$ go tool pprof --base=./${slave}.heap.20141117175634 --inuse_objects --pdf \
  ./bin/k8sm-executor ./${slave}.heap.20141120162503 >${slave}-20141120a.pdf
```

[1]: https://github.com/mesosphere/kubernetes-mesos#build
[2]: https://github.com/tools/godep
[3]: hack/dockerbuild/README.md#profiling
