VERSION ?= $(shell date -Iseconds | sed -e 's/[:+]//g')
IMAGE   ?= mesosphere/kubernetes:kube2sky-$(shell cat .version)

all: kube2sky

version:
	echo "$(VERSION)" >.version

kube2sky: kube2sky.go
	echo GOPATH=$$GOPATH
	env CGO_ENABLED=0 go build -a -o $$(pwd)/kube2sky -installsuffix cgo --ldflags '-w' ./kube2sky.go

container: version
	sudo docker build -t $(IMAGE) .

push:
	sudo docker push $(IMAGE)

clean:
	rm -f kube2sky
