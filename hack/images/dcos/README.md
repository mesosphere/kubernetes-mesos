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
- [ ] process supervision for the executor is broken, dandling procs end up as children of slave
- [ ] makes a big assumption about the location of etcd, need a better story here
- [ ] s6 and k8s require files to exist in certain places on the host. this is terrible practice
