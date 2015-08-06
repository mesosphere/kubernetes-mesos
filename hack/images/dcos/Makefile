# input variables
GIT_REF ?= v1.0.2-v0.6.1
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

# kubernetes binaries distributed in the Docker container
K8S_BINARIES = km kubectl
K8S_BINARIES_SOURCE_DIR = $(BUILD_DIR)/kubernetes/_output/dockerized/bin/$(1)/amd64
K8S_BINARIES_SOURCE = $(addprefix $(call K8S_BINARIES_SOURCE_DIR,linux)/,$(K8S_BINARIES))
K8S_BINARIES_DEST = $(addprefix $(BUILD_DIR)/,$(K8S_BINARIES))
KUBECTL_BINARIES = $(call K8S_BINARIES_SOURCE_DIR,linux)/kubectl $(call K8S_BINARIES_SOURCE_DIR,darwin)/kubectl

# etcd distributed in the Docker container
ETCD_IMAGE = etcd
ETCD_TAG = 2.0.12
ETCD_BINARIES = etcd etcdctl
ETCD_BINARIES_SOURCE_DIR = $(BUILD_DIR)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64
ETCD_BINARIES_DEST = $(addprefix $(BUILD_DIR)/,$(ETCD_BINARIES))
ETCD_SOURCE = https://github.com/coreos/etcd/releases/download/v$(ETCD_TAG)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz

# kubernetes addons: kube-dns and kube-ui
KUBE_DNS_TEMPLATES = $(BUILD_DIR)/skydns-rc.yaml.in $(BUILD_DIR)/skydns-svc.yaml.in
KUBE_UI_TEMPLATES = $(BUILD_DIR)/kube-ui-rc.yaml $(BUILD_DIR)/kube-ui-svc.yaml

# github-release utility to upload release assets to Github
GITHUB_RELEASE = $(shell pwd)/$(BUILD_DIR)/go/bin/github-release

# meta targets
.PHONY: clean build build_dir check_dirty clone test push deps version check_github_token release
.INTERMEDIATE: build_kubernetes copy_kubernetes_binaries download_etcd

all: build

clean:
	rm -rf $(BUILD_DIR)

build_dir:
	@mkdir -p $(BUILD_DIR)

# print out the version and create .version
version: build_dir
	@echo "GIT_REF=$(GIT_REF)"
	@echo "VERSION=$(VERSION)"
	@echo
	@echo "$(VERSION)" > $(BUILD_DIR)/.version

# clone the given kubernetes git ref
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

# check that everything in the repo is commited
check_dirty:
	@cd $(BUILD_DIR)/kubernetes && \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "unclean $(BUILD_DIR)/kubernetes checkout" && \
		exit 1; \
	fi

# run the kubernetes unit and integration tests inside Docker
test: clone
	cd "$(BUILD_DIR)/kubernetes" && KUBERNETES_CONTRIB=mesos build/run.sh hack/test-go.sh
	cd "$(BUILD_DIR)/kubernetes" && KUBERNETES_CONTRIB=mesos build/run.sh hack/test-integration.sh

# cross-compile the kubernetes binaries inside Docker
build_kubernetes: $(BUILD_DIR)/rebuild
	cd "$(BUILD_DIR)/kubernetes" && KUBERNETES_CONTRIB=mesos build/run.sh hack/build-cross.sh
$(K8S_BINARIES_SOURCE): build_kubernetes
$(KUBECTL_BINARIES): build_kubernetes

# copy km and kubectl from the kubernetes _output directory to _build
copy_kubernetes_binaries: $(K8S_BINARIES_SOURCE)
	cp -pv $(K8S_BINARIES_SOURCE) $(BUILD_DIR)
	touch $(K8S_BINARIES_DEST)
$(K8S_BINARIES_DEST): copy_kubernetes_binaries

# download etcd and extract it to _output/etcd-...
download_etcd:
	cd $(BUILD_DIR) && curl -L -O $(ETCD_SOURCE) && tar xzvf $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz
	cp -pv $(addprefix $(ETCD_BINARIES_SOURCE_DIR)/,$(ETCD_BINARIES)) $(BUILD_DIR)
	touch $(ETCD_BINARIES_DEST)
$(ETCD_BINARIES_DEST): download_etcd

# copy the kube-dns specs from the kubernetes repo to _build
$(KUBE_DNS_TEMPLATES): clone
	cp $(BUILD_DIR)/kubernetes/cluster/addons/dns/$(shell basename $@) $@

# copy the kube-ui specs from the kubernetes repo to build_
$(KUBE_UI_TEMPLATES): clone
	cp $(BUILD_DIR)/kubernetes/cluster/addons/kube-ui/$(shell basename $@) $@

# package build targets
deps: build_dir $(ETCD_BINARIES_DEST) $(K8S_BINARIES_DEST) $(KUBE_DNS_TEMPLATES) $(KUBE_UI_TEMPLATES)
build: version clone check_dirty deps
	docker build -t $(DOCKER_IMAGE) .
	docker tag -f $(DOCKER_IMAGE) $(DOCKER_REPO):latest

# push the Docker image to the Docker hub
push:
	echo pushing $(DOCKER_IMAGE); docker push $(DOCKER_IMAGE)

# install github-release, a tool to upload github release assets
$(GITHUB_RELEASE):
	@mkdir -p $(BUILD_DIR)/go && \
	export GOPATH=$$(pwd)/$(BUILD_DIR)/go && \
	go get github.com/aktau/github-release

# check that the environment variable GITHUB_TOKEN is set, needed to uploaded release assets
check_github_token:
	test -n "$$GITHUB_TOKEN"

# upload the kubectl tar.gz for linux/amd64 and darwin/amd64 to github release given by GIT_REF
release: $(GITHUB_RELEASE) check_github_token clone $(KUBECTL_BINARIES)
	$(GITHUB_RELEASE) release -u mesosphere --repo kubernetes --tag $(GIT_REF) --name "Kubernetes on Mesos $(GIT_REF)" --draft
	@set -x; for OS in linux darwin; do \
		BUILD_DIR=$$PWD/$(BUILD_DIR) && \
		cd $(call K8S_BINARIES_SOURCE_DIR,$$OS) && \
		TAR=kubectl-$(GIT_REF)-$$OS-amd64.tgz && \
		tar -cvzf $$BUILD_DIR/$$TAR kubectl && \
		cd - &>/dev/null && \
		$(GITHUB_RELEASE) upload -u mesosphere --repo kubernetes --tag $(GIT_REF) --file $(BUILD_DIR)/$$TAR --name $$TAR || exit 1; \
	done