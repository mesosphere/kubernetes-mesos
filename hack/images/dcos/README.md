## Usage
```
## build and push the docker image
:; make push TARGET=$HOME/bin/km
```

## Files
* Makefile - coordinate the build process: build s6, bundle it with km into a Docker and push the repo
* Dockerfile - recipe for building the docker
* bootstrap.sh - default target of Dockerfile, launches the framework: apiserver, scheduler, controller
* executor.sh - wrapper script that runs the executor on a slave host
* leapsecs.dat - requirement for s6 to work properly
* webhook.sh - helper that can communicate with simple HTTP webhook endpoints

## TODO
- [x] process supervision for the executor is broken, dandling procs end up as children of slave
- [ ] makes a big assumption about the location of etcd, need a better story here
  - **STARTED:** probably want to support multiple deployment scenarios for etcd
    - case 1: etcd cluster is deployed elsewhere and k8sm will tap into it
    - case 2: etcd-server is deployed in the k8sm container, dies with the k8sm framework
      - this can leave lingering exec's mapped to a framework that is not recognized if k8sm is redeployed
      - [ ] etcd needs to start before everything else otherwise bad things happen (apiserver!)
- [ ] s6 and k8s require files to exist in certain places on the host. this is terrible practice
  - mentioned this article to elizabeth, she has similar concerns with the HDFS framework
    - http://www.ibm.com/developerworks/library/l-mount-namespaces/
