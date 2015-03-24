
# the assumption is that current_dir is something along the lines of:
# 	/home/bob/yourproject/src/github.com/mesosphere/kubernetes-mesos

mkfile_path	:= $(abspath $(lastword $(MAKEFILE_LIST)))
current_dir	:= $(patsubst %/,%,$(dir $(mkfile_path)))
fail		:= ${MAKE} --no-print-directory --quiet -f $(current_dir)/Makefile error

KUBE_GO_PACKAGE	?= github.com/GoogleCloudPlatform/kubernetes
K8SM_GO_PACKAGE	?= github.com/mesosphere/kubernetes-mesos

K8S_CMD		:= \
                   ${KUBE_GO_PACKAGE}/cmd/kubectl		\
                   ${KUBE_GO_PACKAGE}/cmd/kube-apiserver	\
                   ${KUBE_GO_PACKAGE}/cmd/kube-proxy

CMD_DIRS := $(shell cd $(current_dir) && find ./cmd -type f -name '*.go'|sort|while read f; do echo -E "$$(dirname "$$f")"; done|sort|uniq|cut -f1 -d/ --complement)

FRAMEWORK_CMD	:= ${CMD_DIRS:%=${K8SM_GO_PACKAGE}/%}

LIB_DIRS := $(shell cd $(current_dir) && find ./pkg -type f -name '*.go'|sort|while read f; do echo -E "$$(dirname "$$f")"; done|sort|uniq|cut -f1 -d/ --complement)

FRAMEWORK_LIB	:= ${LIB_DIRS:%=${K8SM_GO_PACKAGE}/%}
TESTS		?= $(LIB_DIRS)

GIT_VERSION_FILE := $(current_dir)/.kube-version

SHELL		:= /bin/bash

# a list of upstream projects for which we test the availability of patches
PATCH_SCRIPT	:= $(current_dir)/hack/patches/apply.sh

# TODO: make this something more reasonable
DESTDIR		?= /target

# default build tags
TAGS		?=

BUILDDIR	?= $(current_dir)/_build

.PHONY: all error require-godep require-vendor install info bootstrap format test patch version test.v clean lint vet fix prepare

# FRAMEWORK_FLAGS := -v -x -tags '$(TAGS)'
FRAMEWORK_FLAGS := -tags '$(TAGS)'

ifneq ($(WITH_RACE),)
FRAMEWORK_FLAGS += -race
endif

export SHELL
export KUBE_GO_PACKAGE
export K8SM_GO_PACKAGE

all: patch version
	env GOPATH=$(BUILDDIR) go install $(K8S_CMD)
	env GOPATH=$(BUILDDIR) go install -ldflags "$(shell cat $(GIT_VERSION_FILE))" $(FRAMEWORK_FLAGS) $(FRAMEWORK_CMD)

error:
	echo -E "$@: ${MSG}" >&2
	false

require-godep:
	@which godep >/dev/null || ${fail} MSG="Missing godep tool, aborting"

require-vendor:

clean:
	rm -f $(GIT_VERSION_FILE)
	test -n "$(BUILDDIR)" && rm -rf $(BUILDDIR)/*

format:
	env GOPATH=$(BUILDDIR) go fmt $(FRAMEWORK_CMD) $(FRAMEWORK_LIB)

lint:
	for pkg in $(FRAMEWORK_CMD) $(FRAMEWORK_LIB); do env GOPATH=$(BUILDDIR) go$@ $$pkg; done

vet fix:
	env GOPATH=$(BUILDDIR) go $@ $(FRAMEWORK_CMD) $(FRAMEWORK_LIB)

test test.v:
	test "$@" = "test.v" && args="-test.v" || args=""; \
		test -n "$(WITH_RACE)" && args="$$args -race" || true; \
		env GOPATH=$(BUILDDIR) go test $$args $(TESTS:%=${K8SM_GO_PACKAGE}/%)

install: all
	mkdir -p $(DESTDIR)
	(pkg="$(BUILDDIR)"; for x in $(notdir $(K8S_CMD) $(FRAMEWORK_CMD)); do \
	 /bin/cp -vpf -t $(DESTDIR) "$${pkg}"/bin/$$x; done)

info:
	@echo RACE_FLAGS=$${WITH_RACE:+-race}
	@echo TAGS=$(TAGS)
	@echo LIB_DIRS=$(LIB_DIRS)
	@echo FRAMEWORK_LIB=$(FRAMEWORK_LIB)
	@echo CMD_DIRS=$(CMD_DIRS)
	@echo FRAMEWORK_CMD=$(FRAMEWORK_CMD)

# noop for now; may be needed if vendoring, dep mgmt tooling changes
bootstrap:

prepare:
	test -L $(BUILDDIR)/src/$(K8SM_GO_PACKAGE) && rm -f $(BUILDDIR)/src/$(K8SM_GO_PACKAGE) || true
	rsync -au $(current_dir)/Godeps/_workspace/ $(BUILDDIR)
	(xdir=$$(dirname $(BUILDDIR)/src/$(K8SM_GO_PACKAGE)); mkdir -p $$xdir && cd $$xdir && ln -s $(current_dir) $$(basename $(K8SM_GO_PACKAGE)))

patch: prepare $(PATCH_SCRIPT)
	env GOPATH=$(BUILDDIR) $(PATCH_SCRIPT)

version: $(GIT_VERSION_FILE)

$(GIT_VERSION_FILE):
	@(pkg="$(BUILDDIR)"; cd "$${pkg%%:*}/src/$(K8SM_GO_PACKAGE)" && \
	  source $(current_dir)/hack/kube-version.sh && \
	  K8SM_GO_PACKAGE=$(K8SM_GO_PACKAGE) kube::version::ldflags) >$@

$(PATCH_SCRIPT):
	test -x $@ || chmod +x $@
