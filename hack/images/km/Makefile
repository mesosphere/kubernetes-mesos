.PHONY:	clean build push

TARGET	?= /target/km
IMAGE	?= km
REPO	?= jdef/k8sm-$(IMAGE)
TAG	?= 0.14.2-0.5-dev
SUDO	?= $(shell test "$$(whoami)" = "root" || echo sudo)

clean:
	rm -rf km

build: clean
	cp -pv $(TARGET) .
	$(SUDO) docker build -t $(REPO):$(TAG) .

push:	build
	$(SUDO) docker push $(REPO):$(TAG)

all:	push
