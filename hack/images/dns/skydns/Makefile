VERSION	?= $(shell date -Iseconds | sed -e 's/[:+]//g')
IMAGE	?= mesosphere/kubernetes:skydns-$(shell cat .version)

.PHONY: all skydns container push clean version

all: skydns

version:
	echo "$(VERSION)" >.version

skydns:
	mkdir -p go && env GOPATH=$$(pwd)/go CGO_ENABLED=0 go get -a -installsuffix cgo --ldflags '-w' github.com/skynetservices/skydns

container: version
	sudo docker build -t $(IMAGE) .

push:
	sudo docker push $(IMAGE)

clean:
	rm -rf skydns go
