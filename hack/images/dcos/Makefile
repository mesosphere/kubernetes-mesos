mkfile_path     := $(abspath $(lastword $(MAKEFILE_LIST)))
current_dir     := $(patsubst %/,%,$(dir $(mkfile_path)))

BUILD_DIR = _build

K8S_LINUX_BIN_DIR = $(BUILD_DIR)/kubernetes/_output/dockerized/bin/linux/amd64
K8S_BINARIES = km kubectl
K8S_BINARIES_SOURCE = $(addprefix $(K8S_LINUX_BIN_DIR)/,$(K8S_BINARIES))
K8S_BINARIES_DEST = $(addprefix $(BUILD_DIR)/,$(K8S_BINARIES))

ETCD_IMAGE = etcd
ETCD_TAG = 2.0.12
ETCD_BINARIES = etcd etcdctl
ETCD_BINARIES_SOURCE = $(addprefix $(BUILD_DIR)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64/,$(ETCD_BINARIES))
ETCD_BINARIES_DEST = $(addprefix $(BUILD_DIR)/,$(ETCD_BINARIES))
ETCD_SOURCE = https://github.com/coreos/etcd/releases/download/v$(ETCD_TAG)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz

KUBE_DNS_TEMPLATES = $(BUILD_DIR)/skydns-rc.yaml.in $(BUILD_DIR)/skydns-svc.yaml.in
KUBE_UI_TEMPLATES = $(BUILD_DIR)/kube-ui-rc.yaml $(BUILD_DIR)/kube-ui-svc.yaml

GIT_REF ?= v1.0.1-v0.6.1
GIT_REPO ?= mesosphere/kubernetes
GIT_REF_BASE_URL = https://raw.githubusercontent.com/mesosphere/kubernetes/$(GIT_REF)

VERSION = v1.0.1-$(shell git describe --tags --dirty)-alpha$(shell if [ -d $(BUILD_DIR) ]; then echo "-unclean"; fi)

DOCKER_ORG ?= mesosphere
DOCKER_REPO ?= $(DOCKER_ORG)/kubernetes
DOCKER_IMAGE ?= $(DOCKER_REPO):$(VERSION)

BUILDER_IMAGE ?= mesosphere/kubernetes-mesos-build

.PHONY: clean build test clone_k8s push deps etcd version info

all: build

clean:
	rm -rf $(BUILD_DIR)

version:
	@echo "$(VERSION)" | tee $(BUILD_DIR)/.version

clone:
	cd $(BUILD_DIR) && \
	if [ ! -d "kubernetes" ]; then \
		git clone --branch $(GIT_REF) https://github.com/$(GIT_REPO).git kubernetes; \
	else \
		cd kubernetes && git checkout $(GIT_REF); \
	fi

test: clone
	cd "$(BUILD_DIR)/kubernetes" && KUBERNETES_CONTRIB=mesos build/run.sh hack/test-go.sh
	cd "$(BUILD_DIR)/kubernetes" && KUBERNETES_CONTRIB=mesos build/run.sh hack/test-integration.sh

$(K8S_BINARIES_SOURCE):
	cd "$(BUILD_DIR)/kubernetes" && KUBERNETES_CONTRIB=mesos build/run.sh hack/build-go.sh
	touch $(BUILD_DIR)/built

$(K8S_BINARIES_DEST): $(K8S_BINARIES_SOURCE)
	cp -av $(K8S_BINARIES_SOURCE) $(BUILD_DIR)

$(ETCD_BINARIES_SOURCE):
	cd $(BUILD_DIR) && curl -L -O $(ETCD_SOURCE) && tar xzvf $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz

$(ETCD_BINARIES_DEST): $(ETCD_BINARIES_SOURCE)
	cp -av $(ETCD_BINARIES_SOURCE) $(BUILD_DIR)

$(KUBE_DNS_TEMPLATES):
	curl -f $(GIT_REF_BASE_URL)/cluster/addons/dns/$(shell basename $@) > $@

$(KUBE_UI_TEMPLATES):
	curl -f $(GIT_REF_BASE_URL)/cluster/addons/kube-ui/$(shell basename $@) > $@

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

deps: $(BUILD_DIR) $(ETCD_BINARIES_DEST) $(K8S_BINARIES_DEST) $(KUBE_DNS_TEMPLATES) $(KUBE_UI_TEMPLATES)

build: deps version
	docker build -q -t $(DOCKER_IMAGE) .
	docker tag -f $(DOCKER_IMAGE) $(DOCKER_REPO):latest

push:
	echo pushing $(DOCKER_IMAGE); docker push $(DOCKER_IMAGE)

info:
	@docker version
	@echo ".version : $$(cat $(BUILD_DIR)/.version 2>/dev/null)"
