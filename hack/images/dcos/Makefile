TARGET	?= /target/km
IMAGE	?= km-dcos
REPO	?= jdef/k8sm-$(IMAGE)
TAG	?= 0.14.2-0.5-dev
SUDO	?= $(shell test "$$(whoami)" = "root" || echo sudo)

COMMON_ARCH ?= linux-amd64

S6_GIT	?= https://github.com/just-containers/skaware.git
S6_VER	?= 2.1.3.0
S6_ARCH	?= $(COMMON_ARCH)
S6_OBJ	?= s6-$(S6_VER)-$(S6_ARCH)-bin.tar.gz

EXECLINE_VER ?= 2.1.1.1
EXECLINE_ARCH ?= $(COMMON_ARCH)
EXECLINE_OBJ ?= execline-$(EXECLINE_VER)-$(EXECLINE_ARCH)-bin.tar.gz

S6_BINS ?= $(S6_OBJ) $(EXECLINE_OBJ)

.PHONY:	clean build push s6 $(S6_BINS) deps

clean:
	rm -rf km

deps: $(S6_BINS)

build: clean deps
	cp -pv $(TARGET) .
	$(SUDO) docker build -t $(REPO):$(TAG) .

push:	build
	$(SUDO) docker push $(REPO):$(TAG)

s6:
	mkdir -pv _build/s6 _build/dist && chmod -v o+rw _build/dist
	test -f _build/s6/.gitignore || git clone --depth=1 $(S6_GIT) _build/s6
	test -f _build/dist/manifest.txt || ( \
		cd _build/s6 && $(SUDO) docker build . | tail -n 1 | awk '{ print $$3; }' | \
		xargs $(SUDO) docker run --rm -v $$(readlink -f ../dist):/skarnet-builder/dist)

$(S6_BINS): s6
	tar xzf _build/dist/$@

all:	push
