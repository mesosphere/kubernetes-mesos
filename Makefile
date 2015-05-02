
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

CMD_DIRS := $(shell cd $(current_dir) && find ./cmd -type f -name '*.go'|sort|while read f; do echo -E "$$(dirname "$$f")"; done|sort|uniq|sed -e 's~^[^/]*/~~g')

FRAMEWORK_CMD	:= ${CMD_DIRS:%=${K8SM_GO_PACKAGE}/%}

LIB_DIRS := $(shell cd $(current_dir) && find ./pkg -type f -name '*.go'|sort|while read f; do echo -E "$$(dirname "$$f")"; done|sort|uniq|sed -e 's~^[^/]*/~~g')

FRAMEWORK_LIB	:= ${LIB_DIRS:%=${K8SM_GO_PACKAGE}/%}
TESTS_LOGV	?= 2
TESTS		?= $(LIB_DIRS) $(CMD_DIRS)
TESTS_VV        = $(shell for pkg in $(TESTS); do ls $$pkg/*.go | while read -r f; do basename "$$f"|egrep -v -e '_test.go$$'|grep -v -e '^doc\.go'|sed -e 's/\.go$$/=$(TESTS_LOGV)/g'; done; done | xargs echo -n | tr ' ' ',')

GIT_VERSION_FILE := $(current_dir)/.kube-version

SHELL		:= /bin/bash

# applying patches requires bash 4+ so this variable may be specified with a full path to a newer version of the bash
ALT_BASH_SHELL	?= /usr/local/bin/bash

# a list of upstream projects for which we test the availability of patches
PATCH_SCRIPT	:= $(current_dir)/hack/patches/apply.sh

# TODO: make this something more reasonable
DESTDIR		?= /target

# default build tags
TAGS		?=

BUILDDIR	?= $(current_dir)/_build
_GOPATH		:= $(shell uname | grep -e ^CYGWIN >/dev/null && cygpath --mixed "$(BUILDDIR)" || echo -E "$(BUILDDIR)")

TEST_OBJ	:= $(subst /,___,$(TESTS))

.PHONY: all error require-godep require-vendor install info bootstrap format test patch.v patch version test.v test.vv test.cover clean lint vet fix prepare update $(TEST_OBJ) test.integration kube2sky

# FRAMEWORK_FLAGS := -v -x -tags '$(TAGS)'
FRAMEWORK_FLAGS := -tags '$(TAGS)'

ifneq ($(WITH_RACE),)
FRAMEWORK_FLAGS += -race
endif

export SHELL
export KUBE_GO_PACKAGE
export K8SM_GO_PACKAGE

all: patch version
	env GOPATH=$(_GOPATH) go install $(K8S_CMD)
	env GOPATH=$(_GOPATH) go install -ldflags "$(shell cat $(GIT_VERSION_FILE))" $(FRAMEWORK_FLAGS) $(FRAMEWORK_CMD)

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
	env GOPATH=$(_GOPATH) go fmt $(FRAMEWORK_CMD) $(FRAMEWORK_LIB)

lint:
	for pkg in $(FRAMEWORK_CMD) $(FRAMEWORK_LIB); do env GOPATH=$(_GOPATH) go$@ $$pkg; done

vet fix:
	env GOPATH=$(_GOPATH) go $@ $(FRAMEWORK_CMD) $(FRAMEWORK_LIB)

test test.v: patch
	test "$@" = "test.v" && args="-test.v" || args=""; \
		test -n "$(WITH_RACE)" && args="$$args -race" || true; \
		env GOPATH=$(_GOPATH) go test $$args -tags unit_test $(TESTS:%=${K8SM_GO_PACKAGE}/%)

test.vv: patch
	test -n "$(WITH_RACE)" && args="$$args -race" || args=""; \
		env GOPATH=$(_GOPATH) go test -test.v $$args -tags unit_test $(TESTS:%=${K8SM_GO_PACKAGE}/%) -logtostderr=true -vmodule=$(TESTS_VV)

test.cover: $(TEST_OBJ)
	@(echo "mode: count" && (cat .*.coverage.out|sed -e '/^mode/d')) >all.coverage.out

$(TEST_OBJ):
	@test -n "$(WITH_RACE)" && args="$$args -race" || args=""; \
		env GOPATH=$(_GOPATH) go test -v $$args -covermode=count -coverprofile=.$@.coverage.out -tags unit_test $(K8SM_GO_PACKAGE)/$(subst ___,/,$@)

test.integration:
	$(current_dir)/hack/test_redirfd.sh

install: all
	mkdir -p $(DESTDIR)
	(pkg="$(BUILDDIR)"; for x in $(notdir $(K8S_CMD) $(FRAMEWORK_CMD)); do \
	 /bin/cp -vpf "$${pkg}"/bin/$$x $(DESTDIR); done)

info:
	@echo RACE_FLAGS=$${WITH_RACE:+-race}
	@echo TAGS=$(TAGS)
	@echo LIB_DIRS=$(LIB_DIRS)
	@echo FRAMEWORK_LIB=$(FRAMEWORK_LIB)
	@echo CMD_DIRS=$(CMD_DIRS)
	@echo FRAMEWORK_CMD=$(FRAMEWORK_CMD)
	@echo GOPATH=$(_GOPATH)
	@echo TEST_OBJ=$(TEST_OBJ)

# noop for now; may be needed if vendoring, dep mgmt tooling changes
bootstrap:

prepare:
	test -L $(BUILDDIR)/src/$(K8SM_GO_PACKAGE) && rm -f $(BUILDDIR)/src/$(K8SM_GO_PACKAGE) || true
	rsync -au $(current_dir)/Godeps/_workspace/ $(BUILDDIR)
	(xdir=$$(dirname $(BUILDDIR)/src/$(K8SM_GO_PACKAGE)); mkdir -p $$xdir && cd $$xdir && ln -s $(current_dir) $$(basename $(K8SM_GO_PACKAGE)))

patch patch.v: prepare $(PATCH_SCRIPT)
	env GOPATH=$(_GOPATH) USR_LOCAL_BASH=$(ALT_BASH_SHELL) PATCH_TARGET=$@ $(PATCH_SCRIPT)

version: $(GIT_VERSION_FILE)

$(GIT_VERSION_FILE):
	@(pkg="$(BUILDDIR)"; cd "$${pkg%%:*}/src/$(K8SM_GO_PACKAGE)" && \
	  source $(current_dir)/hack/kube-version.sh && \
	  K8SM_GO_PACKAGE=$(K8SM_GO_PACKAGE) kube::version::ldflags) >$@

$(PATCH_SCRIPT):
	test -x $@ || chmod +x $@

update:
	@egrep -e 'code.google.com|github.com|bitbucket.org' $(current_dir)/Godeps/Godeps.json | grep -v -e '$(K8SM_GO_PACKAGE)' | \
		sed -e 's/^.*Path": "\(.*\)",/\1/g' | cut -f1-3 -d/|sort|uniq|xargs -t -I{} godep update {}/...

kube2sky: prepare
	env GOPATH=$(_GOPATH) $(MAKE) -C $(current_dir)/hack/images/dns/kube2sky
