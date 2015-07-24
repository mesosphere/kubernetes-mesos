mkfile_path     := $(abspath $(lastword $(MAKEFILE_LIST)))
current_dir     := $(patsubst %/,%,$(dir $(mkfile_path)))

FROM ?= $(current_dir)/_build/k8sm
TARGET_OBJ = km kubectl
SOURCE_OBJ = $(TARGET_OBJ:%=$(FROM)/%)

ETCD_IMAGE = etcd
ETCD_TAG = 2.0.12
ETCD_OUTPUT_DIR = $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64
ETCD_BINS = $(ETCD_OUTPUT_DIR)/etcd $(ETCD_OUTPUT_DIR)/etcdctl
ETCD_SOURCE = https://github.com/coreos/etcd/releases/download/v$(ETCD_TAG)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz

KUBE_DNS_TEMPLATES = skydns-rc.yaml.in skydns-svc.yaml.in
KUBE_UI_TEMPLATES = kube-ui-rc.yaml kube-ui-svc.yaml

REPO ?= mesosphere/kubernetes
REF ?= v1.0.1
VERSION = $(REF)-$(shell git describe --tags --dirty)-alpha
IMAGE ?= $(REPO):$(VERSION)
BUILDER_IMAGE ?= mesosphere/kubernetes-mesos-build

.PHONY: clean build push deps etcd $(ETCD_BINS) $(KUBE_DNS_TEMPLATES) $(KUBE_UI_TEMPLATES) version info $(SOURCE_OBJ)

all: build

clean:
	rm -rf $(TARGET_OBJ) $(KUBE_DNS_TEMPLATES)

deps: $(ETCD_BINS) $(KUBE_DNS_TEMPLATES) $(KUBE_UI_TEMPLATES) $(SOURCE_OBJ)

version:
	@echo "$(VERSION)" | tee .version

build: deps version
	cp -pv $(SOURCE_OBJ) .
	docker build -q -t $(IMAGE) .
	docker tag -f $(IMAGE) $(REPO):latest

push:
	echo pushing $(IMAGE); docker push $(IMAGE)

etcd:
	test 1 = 1 $(ETCD_BINS:%= -a -f "%") || ( \
		curl -L -O $(ETCD_SOURCE) && tar xzvf $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz)

$(ETCD_BINS): etcd
	test -f "$$(basename $@)" || cp -v $@ .

$(KUBE_DNS_TEMPLATES):
	curl -f https://raw.githubusercontent.com/GoogleCloudPlatform/kubernetes/$(REF)/cluster/addons/dns/$@ > $@

$(KUBE_UI_TEMPLATES):
	curl -f https://raw.githubusercontent.com/GoogleCloudPlatform/kubernetes/$(REF)/cluster/addons/kube-ui/$@ > $@

info:
	@docker version
	@echo ".version : $$(cat .version 2>/dev/null)"

$(SOURCE_OBJ):
	test -x $@ || \
		docker run --rm -ti -v $(FROM):/target -e GIT_BRANCH=$(REF) $(BUILDER_IMAGE) \
		bash -c './hack/build-cross.sh && cp -vp _output/local/bin/linux/amd64/* /target/ && find _output/local/bin/ -name kubectl | while IFS=/ read -r a b c os arch app; do cp -vp "$$a/$$b/$$c/$$os/$$arch/$$app" "/target/$${app}-$${os}-$${arch}"; done'
