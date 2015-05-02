FROM	?= /target
TARGET_OBJ = km kubectl
IMAGE	?= kubernetes
REPO	?= mesosphere/$(IMAGE)
TAG	?= k8s-0.14.2-k8sm-0.5-dcos-dev
SUDO	?= $(shell test "$$(whoami)" = "root" || echo sudo)

COMMON_ARCH ?= linux-amd64

S6_GIT	?= https://github.com/just-containers/skaware.git
S6_VER	?= 2.1.3.0
S6_ARCH	?= $(COMMON_ARCH)
S6_OBJ	?= s6-$(S6_VER)-$(S6_ARCH)-bin.tar.gz

EXECLINE_VER ?= 2.1.1.1
EXECLINE_ARCH ?= $(COMMON_ARCH)
EXECLINE_OBJ ?= execline-$(EXECLINE_VER)-$(EXECLINE_ARCH)-bin.tar.gz

ETCD_IMAGE = etcd
ETCD_TAG = 2.0.9
ETCD_OUTPUT_DIR = $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64
ETCD_BINS = $(ETCD_OUTPUT_DIR)/etcd $(ETCD_OUTPUT_DIR)/etcdctl
ETCD_SOURCE = https://github.com/coreos/etcd/releases/download/v$(ETCD_TAG)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz

KUBE_DNS_TEMPLATES = skydns-rc.yaml.in skydns-svc.yaml.in
KUBE_DNS_TEMPLATES_SOURCES = $(KUBE_DNS_TEMPLATES:%=../dns/%)

S6_BINS ?= $(S6_OBJ) $(EXECLINE_OBJ)

.PHONY:	clean build push s6 $(S6_BINS) deps etcd $(ETCD_BINS) $(KUBE_DNS_TEMPLATES)

#	rm -rf km $(ETCD_OUTPUT_DIR) usr _build etcd*
clean:
	rm -rf km

deps: $(S6_BINS) $(ETCD_BINS) $(KUBE_DNS_TEMPLATES)

build: deps
	cp -pv $(TARGET_OBJ:%=$(FROM)/%) .
	date -Iseconds >.version
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

etcd:
	test 1 = 1 $(ETCD_BINS:%= -a -f "%") || ( \
		curl -L -O $(ETCD_SOURCE) && tar xzvf $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz)

$(ETCD_BINS): etcd
	test -f "$$(basename $@)" || cp -v $@ .

$(KUBE_DNS_TEMPLATES): $(KUBE_DNS_TEMPLATES_SOURCES)
	@if test -f $@ -a ../dns/$@ -ot $@; then true; else cp -v ../dns/$@ $@; fi

$(KUBE_DNS_TEMPLATES_SOURCES):

all:	push
