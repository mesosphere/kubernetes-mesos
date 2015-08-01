# input variables
GIT_REF ?= v1.0.1-v0.6.1
GIT_REPO ?= mesosphere/kubernetes
DOCKER_ORG ?= mesosphere

# fixed values
GIT_REF_BASE_URL = https://raw.githubusercontent.com/mesosphere/kubernetes/$(GIT_REF)
GIT_REF_MINOR = $(lastword $(subst -, ,$(GIT_REF)))
GIT_REF_MAJOR = $(firstword $(subst -, ,$(GIT_REF)))

BUILD_DIR = _build

ifneq ($(wildcard $(BUILD_DIR)),)
UNCLEAN_INFIX := -unclean
endif
VERSION := $(GIT_REF_MAJOR)-$(shell git describe --match $(GIT_REF_MINOR) --always --tags --dirty)$(UNCLEAN_INFIX)-alpha

DOCKER_REPO = $(DOCKER_ORG)/kubernetes
DOCKER_IMAGE = $(DOCKER_REPO):$(VERSION)

K8S_BINARIES = km kubectl
K8S_BINARIES_SOURCE_DIR = $(BUILD_DIR)/kubernetes/_output/dockerized/bin/linux/amd64
K8S_BINARIES_SOURCE = $(addprefix $(K8S_BINARIES_SOURCE_DIR)/,$(K8S_BINARIES))
K8S_BINARIES_DEST = $(addprefix $(BUILD_DIR)/,$(K8S_BINARIES))

ETCD_IMAGE = etcd
ETCD_TAG = 2.0.12
ETCD_BINARIES = etcd etcdctl
ETCD_BINARIES_SOURCE_DIR = $(BUILD_DIR)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64
ETCD_BINARIES_DEST = $(addprefix $(BUILD_DIR)/,$(ETCD_BINARIES))
ETCD_SOURCE = https://github.com/coreos/etcd/releases/download/v$(ETCD_TAG)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz

KUBE_DNS_TEMPLATES = $(BUILD_DIR)/skydns-rc.yaml.in $(BUILD_DIR)/skydns-svc.yaml.in
KUBE_UI_TEMPLATES = $(BUILD_DIR)/kube-ui-rc.yaml $(BUILD_DIR)/kube-ui-svc.yaml

.PHONY: clean build build_dir check_dirty clone test push deps version
.INTERMEDIATE: build_kubernetes copy_kubernetes_binaries download_etcd

all: build

clean:
	rm -rf $(BUILD_DIR)

build_dir:
	@mkdir -p $(BUILD_DIR)

version: build_dir
	@echo "GIT_REF=$(GIT_REF)"
	@echo "VERSION=$(VERSION)"
	@echo
	@echo "$(VERSION)" > $(BUILD_DIR)/.version

clone: build_dir
	@cd $(BUILD_DIR) && \
	if [ ! -d "kubernetes" ]; then \
		git clone --branch $(GIT_REF) https://github.com/$(GIT_REPO).git kubernetes && \
		touch rebuild; \
	else \
		cd kubernetes && \
		PREV=$$(git rev-parse HEAD) && \
		git fetch && git checkout $(GIT_REF) && \
		if [ "$$PREV" != "$$(git rev-parse HEAD)" ]; then touch ../rebuild; fi; \
	fi

check_dirty:
	@cd $(BUILD_DIR)/kubernetes && \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "unclean $(BUILD_DIR)/kubernetes checkout" && \
		exit 1; \
	fi

test: clone
	cd "$(BUILD_DIR)/kubernetes" && KUBERNETES_CONTRIB=mesos build/run.sh hack/test-go.sh
	cd "$(BUILD_DIR)/kubernetes" && KUBERNETES_CONTRIB=mesos build/run.sh hack/test-integration.sh

build_kubernetes: $(BUILD_DIR)/rebuild
	cd "$(BUILD_DIR)/kubernetes" && KUBERNETES_CONTRIB=mesos build/run.sh hack/build-go.sh
$(K8S_BINARIES_SOURCE): build_kubernetes

copy_kubernetes_binaries: $(K8S_BINARIES_SOURCE)
	cp -pv $(K8S_BINARIES_SOURCE) $(BUILD_DIR)
	touch $(K8S_BINARIES_DEST)
$(K8S_BINARIES_DEST): copy_kubernetes_binaries

download_etcd:
	cd $(BUILD_DIR) && curl -L -O $(ETCD_SOURCE) && tar xzvf $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz
	cp -pv $(addprefix $(ETCD_BINARIES_SOURCE_DIR)/,$(ETCD_BINARIES)) $(BUILD_DIR)
	touch $(ETCD_BINARIES_DEST)
$(ETCD_BINARIES_DEST): download_etcd

$(KUBE_DNS_TEMPLATES):
	curl -f $(GIT_REF_BASE_URL)/cluster/addons/dns/$(shell basename $@) > $@

$(KUBE_UI_TEMPLATES):
	curl -f $(GIT_REF_BASE_URL)/cluster/addons/kube-ui/$(shell basename $@) > $@

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

deps: $(BUILD_DIR) $(ETCD_BINARIES_DEST) $(K8S_BINARIES_DEST) $(KUBE_DNS_TEMPLATES) $(KUBE_UI_TEMPLATES)

build: version clone check_dirty deps
	docker build -q -t $(DOCKER_IMAGE) .
	docker tag -f $(DOCKER_IMAGE) $(DOCKER_REPO):latest

push:
	echo pushing $(DOCKER_IMAGE); docker push $(DOCKER_IMAGE)
