mkfile_path     := $(abspath $(lastword $(MAKEFILE_LIST)))
current_dir     := $(patsubst %/,%,$(dir $(mkfile_path)))

FROM	?= $(current_dir)/_build/k8sm
TARGET_OBJ = km kubectl
SOURCE_OBJ = $(TARGET_OBJ:%=$(FROM)/%)

IMAGE	?= kubernetes
REPO	?= mesosphere/$(IMAGE)
DCOS_VERSION ?= dev
SUDO	?= 
READLINK := $(shell if which greadlink &>/dev/null; then echo greadlink; else echo readlink; fi)

COMMON_ARCH ?= linux-amd64

ETCD_IMAGE = etcd
ETCD_TAG = 2.0.12
ETCD_OUTPUT_DIR = $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64
ETCD_BINS = $(ETCD_OUTPUT_DIR)/etcd $(ETCD_OUTPUT_DIR)/etcdctl
ETCD_SOURCE = https://github.com/coreos/etcd/releases/download/v$(ETCD_TAG)/$(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz

KUBE_DNS_TEMPLATES = skydns-rc.yaml.in skydns-svc.yaml.in
KUBE_DNS_TEMPLATES_SOURCES = $(KUBE_DNS_TEMPLATES:%=../dns/%)

#
# normally you'll be generating dev images via:
#	make release 
#
# and promoting (re-tagging them with a release version) them via:
#	make promote
#
# HOWEVER, to generate a fresh release image and push it up (skip the -dev tag cycle):
#	make release FROM=~/bin RELEASE=1
#
RELEASE ?=
RELEASE_VERSION ?= $(shell date +"%Y%m%dT%H%M%S")

ifneq ($(RELEASE),)
DCOS_VERSION = $(RELEASE_VERSION)
endif

DCOS_PREFIX ?= k8s-1.0.pre-k8sm-0.6-dcos
TAG	?= $(DCOS_PREFIX)-$(DCOS_VERSION)
FQIN	?= $(REPO):$(TAG)

.PHONY:	clean build push deps etcd $(ETCD_BINS) $(KUBE_DNS_TEMPLATES) version release promote check-sudo-docker info $(SOURCE_OBJ)

all:	build

clean:
	rm -rf $(TARGET_OBJ)

deps: $(ETCD_BINS) $(KUBE_DNS_TEMPLATES) $(SOURCE_OBJ)

version:
	@echo "$(RELEASE_VERSION)" | tee .version

build: deps version check-sudo-docker
	cp -pv $(SOURCE_OBJ) .
	@echo $(FQIN) >dockertag
	$(SUDO) docker build -q -t $$(cat dockertag) .

push: check-sudo-docker
	(dockertag=$$(cat dockertag); echo pushing $$dockertag; $(SUDO) docker push $$dockertag)

# would be nice to validate that the image contains .version that matches what's in the dir
promote: check-sudo-docker
	echo $(REPO):$(DCOS_PREFIX)-$$(cat .version) >dockertag
	(dockertag=$$(cat dockertag); echo promoting $$dockertag; $(SUDO) docker tag -f $(REPO):$(DCOS_PREFIX)-dev $$dockertag) && \
	$(MAKE) push

release:
	$(MAKE) build push FROM=$(FROM) RELEASE=$(RELEASE)

etcd:
	test 1 = 1 $(ETCD_BINS:%= -a -f "%") || ( \
		curl -L -O $(ETCD_SOURCE) && tar xzvf $(ETCD_IMAGE)-v$(ETCD_TAG)-linux-amd64.tar.gz)

$(ETCD_BINS): etcd
	test -f "$$(basename $@)" || cp -v $@ .

$(KUBE_DNS_TEMPLATES): $(KUBE_DNS_TEMPLATES_SOURCES)
	@if test -f $@ -a ../dns/$@ -ot $@; then true; else cp -v ../dns/$@ $@; fi

$(KUBE_DNS_TEMPLATES_SOURCES):

check-sudo-docker:
	@$(SUDO) docker version >/dev/null || (echo You do not have privileges to execute \"$(SUDO) docker\" commands, or else docker is not properly installed/configured >&2; exit 1)

info: check-sudo-docker
	@$(SUDO) docker version
	@echo ".version : $$(cat .version 2>/dev/null)"
	@echo "dockertag: $$(cat dockertag 2>/dev/null)"

# by default we build kubernetes-mesos components from the kubernetes@master branch.
# you can override this on the command like like so:
#     make KUBERNTES_BRANCH=v0.19.1
KUBERNETES_BRANCH ?= master
K8SM_BUILDER ?= mesosphere/kubernetes-mesos-build

$(SOURCE_OBJ): check-sudo-docker
	test -x $@ || \
		$(SUDO) docker run --rm -ti -v $(FROM):/target -e GIT_BRANCH=$(KUBERNETES_BRANCH) $(K8SM_BUILDER) \
		bash -c './hack/build-cross.sh && cp -vp _output/local/bin/linux/amd64/* /target/ && find _output/local/bin/ -name kubectl | while IFS=/ read -r a b c os arch app; do cp -vp "$$a/$$b/$$c/$$os/$$arch/$$app" "/target/$${app}-$${os}-$${arch}"; done'
