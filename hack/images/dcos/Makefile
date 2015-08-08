# input variables
GIT_REF ?= v1.0.2-v0.6.1
GIT_URL ?= https://github.com/mesosphere/kubernetes.git
DOCKER_ORG ?= mesosphere
KUBE_ROOT ?=
SUDO ?=

# fixed values
SHELL := /bin/bash

GIT_REF_MINOR = $(lastword $(subst -, ,$(GIT_REF)))
GIT_REF_MAJOR = $(firstword $(subst -, ,$(GIT_REF)))

BUILD_DIR = _build

ifneq ($(wildcard $(BUILD_DIR)),)
	UNCLEAN_INFIX := -unclean
endif
ifeq ($(KUBE_ROOT),)
	VERSION := $(GIT_REF_MAJOR)-$(shell git describe --match $(GIT_REF_MINOR) --always --tags --dirty)$(UNCLEAN_INFIX)-alpha
else
	VERSION := $(GIT_REF)-dev
endif

DOCKER_REPO = $(DOCKER_ORG)/kubernetes
DOCKER_IMAGE = $(DOCKER_REPO):$(VERSION)
DOCKER_IMAGE_FILE = $(BUILD_DIR)/built-docker-image

# kubernetes binaries distributed in the Docker container
K8S_BINARIES = km kubectl
ifeq ($(KUBE_ROOT),)
	K8S_ROOT = $(BUILD_DIR)/kubernetes
else
	K8S_ROOT = $(KUBE_ROOT)
endif
K8S_BINARIES_SOURCE_DIR = $(K8S_ROOT)/_output/dockerized/bin/$(1)/amd64
K8S_BINARIES_SOURCE = $(addprefix $(call K8S_BINARIES_SOURCE_DIR,linux)/,$(K8S_BINARIES))
K8S_BINARIES_DEST = $(addprefix $(BUILD_DIR)/,$(K8S_BINARIES))
KUBECTL_BINARIES = $(call K8S_BINARIES_SOURCE_DIR,linux)/kubectl $(call K8S_BINARIES_SOURCE_DIR,darwin)/kubectl

# etcd distributed in the Docker container
ETCD_IMAGE = etcd
ETCD_TAG = 2.0.12
ETCD_TAR = $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz
ETCD_BINARIES = etcd etcdctl
ETCD_BINARIES_SOURCE_DIR = $(BUILD_DIR)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64
ETCD_BINARIES_DEST = $(addprefix $(BUILD_DIR)/,$(ETCD_BINARIES))
ETCD_SOURCE = https://github.com/coreos/etcd/releases/download/v$(ETCD_TAG)/$(ETCD_TAR)

# s6 overlay used as init process in the Docker container
S6_TAR = s6-overlay-amd64.tar.gz
S6_URL = https://github.com/just-containers/s6-overlay/releases/download/v1.13.0.0/$(S6_TAR)

# kubernetes addons: kube-dns and kube-ui
KUBE_DNS_TEMPLATES = $(BUILD_DIR)/skydns-rc.yaml.in $(BUILD_DIR)/skydns-svc.yaml.in
KUBE_UI_TEMPLATES = $(BUILD_DIR)/kube-ui-rc.yaml $(BUILD_DIR)/kube-ui-svc.yaml

# github-release utility to upload release assets to Github
GITHUB_RELEASE = $(shell pwd)/$(BUILD_DIR)/go/bin/github-release

# meta targets
.PHONY: clean build build_dir check_dirty checkout clone test push deps version delete_docker_image_file check_github_token release
.INTERMEDIATE: build_kubernetes copy_kubernetes_binaries download_etcd

all: build

clean:
	$(SUDO) rm -rf $(BUILD_DIR)

build_dir:
	@mkdir -p $(BUILD_DIR)

# print out the version and create .version
version: build_dir
	@if [ -n "$(KUBE_ROOT)" ]; then \
		echo "KUBE_ROOT=$(KUBE_ROOT)"; \
	else \
		echo "GIT_URL=$(GIT_URL)" && \
		echo "GIT_REF=$(GIT_REF)"; \
	fi
	@echo "VERSION=$(VERSION)"
	@echo "DOCKER_IMAGE=$(DOCKER_IMAGE)"
	@echo
	@echo "$(VERSION)" > $(BUILD_DIR)/.version

# clone the given kubernetes git ref
clone: build_dir
	@cd $(BUILD_DIR) && \
	if [ ! -d "kubernetes" ]; then \
		git clone --branch $(GIT_REF) $(GIT_URL) kubernetes && \
		touch rebuild; \
	else \
		cd kubernetes && \
		PREV=$$(git rev-parse HEAD) && \
		git fetch && git checkout $(GIT_REF) && \
		if [ "$$PREV" != "$$(git rev-parse HEAD)" ]; then touch ../rebuild; fi; \
	fi

# behave differently for a set or unset KUBE_ROOT
ifeq ($(KUBE_ROOT),)
checkout: clone

# check that everything in the repo is commited
check_dirty:
	@cd $(BUILD_DIR)/kubernetes && \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "unclean $(BUILD_DIR)/kubernetes checkout" && \
		exit 1; \
	fi

# cross-compile the kubernetes binaries inside Docker
build_kubernetes: $(BUILD_DIR)/rebuild
	cd "${K8S_ROOT}" && KUBERNETES_CONTRIB=mesos build/run.sh hack/build-cross.sh
else
checkout:
	@test -d $(KUBE_ROOT)
check_dirty:
build_kubernetes:
	@echo "Build linux/amd64 binaries in ${K8S_ROOT}:"
	@echo
	@echo "    KUBERNETES_CONTRIB=mesos build/run.sh hack/build-go.sh"
	@echo
	@exit 1
endif

$(K8S_BINARIES_SOURCE): build_kubernetes
$(KUBECTL_BINARIES): build_kubernetes

# copy km and kubectl from the kubernetes _output directory to _build
copy_kubernetes_binaries: $(K8S_BINARIES_SOURCE)
	cp -pv $(K8S_BINARIES_SOURCE) $(BUILD_DIR)
	touch $(K8S_BINARIES_DEST)
$(K8S_BINARIES_DEST): copy_kubernetes_binaries

# download etcd and extract it to _output/etcd-...
$(BUILD_DIR)/$(ETCD_TAR):
	cd $(BUILD_DIR) && curl -L -O $(ETCD_SOURCE) && tar xzvf $(ETCD_TAR)
	cp -pv $(addprefix $(ETCD_BINARIES_SOURCE_DIR)/,$(ETCD_BINARIES)) $(BUILD_DIR)
	touch $(ETCD_BINARIES_DEST)
$(ETCD_BINARIES_DEST): $(BUILD_DIR)/$(ETCD_TAR)

# download S6
$(BUILD_DIR)/$(S6_TAR):
	cd "$(BUILD_DIR)" && curl -L -O $(S6_URL)

# copy the kube-dns specs from the kubernetes repo to _build
$(KUBE_DNS_TEMPLATES):
	cp ${K8S_ROOT}/cluster/addons/dns/$(shell basename $@) $@

# copy the kube-ui specs from the kubernetes repo to build_
$(KUBE_UI_TEMPLATES):
	cp ${K8S_ROOT}/cluster/addons/kube-ui/$(shell basename $@) $@

# package build targets
delete_docker_image_file:
	@rm -f "$(DOCKER_IMAGE_FILE)"
deps: build_dir $(ETCD_BINARIES_DEST) $(BUILD_DIR)/$(S6_TAR) $(K8S_BINARIES_DEST) $(KUBE_DNS_TEMPLATES) $(KUBE_UI_TEMPLATES)
build: version delete_docker_image_file checkout check_dirty deps
	docker build -t $(DOCKER_IMAGE) .
	docker tag -f $(DOCKER_IMAGE) $(DOCKER_REPO):latest
	@echo $(DOCKER_IMAGE) > $(DOCKER_IMAGE_FILE)

# push the Docker image to the Docker hub
push:
	@if [ ! -f $(DOCKER_IMAGE_FILE) ]; then \
		echo "No docker image ready to push" && \
		exit 1; \
	else \
		echo "Going to push the docker image $$(< $(DOCKER_IMAGE_FILE))" && \
		if tty -s; then \
			echo && \
			echo -n "Continue? [Y/n] " && read YES && if [ -n "$$YES" -a "$$YES" != Y -a "$$YES" != y ]; then exit 1; fi; \
		fi && \
		docker push $$(< $(DOCKER_IMAGE_FILE)); \
	fi

# run the kubernetes unit and integration tests inside Docker
test: checkout
	cd "${K8S_ROOT}" && KUBERNETES_CONTRIB=mesos build/run.sh hack/test-go.sh
	cd "${K8S_ROOT}" && KUBERNETES_CONTRIB=mesos build/run.sh hack/test-integration.sh

# install github-release, a tool to upload github release assets
$(GITHUB_RELEASE):
	@mkdir -p $(BUILD_DIR)/go && \
	export GOPATH=$$(pwd)/$(BUILD_DIR)/go && \
	go get github.com/aktau/github-release

# check that the environment variable GITHUB_TOKEN is set, needed to uploaded release assets
check_github_token:
	test -n "$$GITHUB_TOKEN"

# upload the kubectl tar.gz for linux/amd64 and darwin/amd64 to github release given by GIT_REF
release: $(GITHUB_RELEASE) check_github_token checkout $(KUBECTL_BINARIES)
	$(GITHUB_RELEASE) release -u mesosphere --repo kubernetes --tag $(GIT_REF) --name "Kubernetes on Mesos $(GIT_REF)" --draft
	@set -x; for OS in linux darwin; do \
		BUILD_DIR=$$PWD/$(BUILD_DIR) && \
		cd $(call K8S_BINARIES_SOURCE_DIR,$$OS) && \
		TAR=kubectl-$(GIT_REF)-$$OS-amd64.tgz && \
		tar -cvzf $$BUILD_DIR/$$TAR kubectl && \
		cd - &>/dev/null && \
		$(GITHUB_RELEASE) upload -u mesosphere --repo kubernetes --tag $(GIT_REF) --file $(BUILD_DIR)/$$TAR --name $$TAR || exit 1; \
	done