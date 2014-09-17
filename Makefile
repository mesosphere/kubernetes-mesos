
# the assumption is that current_dir is something along the lines of:
# 	/home/bob/yourproject/src/github.com/mesosphere/kubernetes-mesos

mkfile_path	:= $(abspath $(lastword $(MAKEFILE_LIST)))
current_dir	:= $(patsubst %/,%,$(dir $(mkfile_path)))
fail		:= ${MAKE} --no-print-directory --quiet -f $(current_dir)/Makefile error

frmwk_gopath	:= $(shell readlink -f $(current_dir)/../../../..)
vendor_dir	:= $(current_dir)/third_party

# HACK: this version needs to match the k8s version in Godeps.json
k8s_version	:= 1853c66ddfdfb7d673371e9a2f4be65c066ac81b
k8s_pkg		:= github.com/GoogleCloudPlatform/kubernetes
k8s_repo	:= https://$(k8s_pkg).git
k8s_dir		:= $(vendor_dir)/src/$(k8s_pkg)
k8s_git		:= $(k8s_dir)/.git
k8s_gopath	:= $(vendor_dir):$(k8s_dir)/Godeps/_workspace

PROXY_SRC	:= $(k8s_pkg)/cmd/proxy
PROXY_OBJ	:= $(subst $(k8s_pkg)/cmd/,$(vendor_dir)/bin/,$(PROXY_SRC))

FRAMEWORK_SRC	:= github.com/mesosphere/kubernetes-mesos/kubernetes-mesos \
		   github.com/mesosphere/kubernetes-mesos/kubernetes-executor
FRAMEWORK_OBJ	:= $(subst github.com/mesosphere/kubernetes-mesos/,/pkg/bin/,$(FRAMEWORK_SRC))

OBJS		:= $(PROXY_OBJ) $(FRAMEWORK_OBJ)

DESTDIR		?= /target

.PHONY: all error require-godep framework require-k8s require-vendor proxy install

ifneq ($(WITH_MESOS_DIR),)

CFLAGS		+= -I$(WITH_MESOS_DIR)/include
CPPFLAGS	+= -I$(WITH_MESOS_DIR)/include
CXXFLAGS	+= -I$(WITH_MESOS_DIR)/include
LDFLAGS		+= -L$(WITH_MESOS_DIR)/lib

CGO_CFLAGS	+= -I$(WITH_MESOS_DIR)/include
CGO_CPPFLAGS	+= -I$(WITH_MESOS_DIR)/include
CGO_CXXFLAGS	+= -I$(WITH_MESOS_DIR)/include
CGO_LDFLAGS	+= -L$(WITH_MESOS_DIR)/lib

WITH_MESOS_CGO_FLAGS :=  \
	  CGO_CFLAGS="$(CGO_CFLAGS)" \
	  CGO_CPPFLAGS="$(CGO_CPPFLAGS)" \
	  CGO_CXXFLAGS="$(CGO_CXXFLAGS)" \
	  CGO_LDFLAGS="$(CGO_LDFLAGS)"

endif

all: $(OBJS)

error:
	echo -E "$@: ${MSG}" >&2
	false

require-godep:
	@which godep >/dev/null || ${fail} MSG="Missing godep tool, aborting"

proxy: $(PROXY_OBJ)

$(PROXY_OBJ): require-k8s
	env GOPATH=$(k8s_gopath)$${GOPATH:+:$$GOPATH} go install $(PROXY_SRC)

require-k8s: | $(k8s_git)

$(k8s_git): require-vendor
	mkdir -p $(k8s_dir)
	test -d $(k8s_git) || git clone $(k8s_repo) $(k8s_dir)
	cd $(k8s_dir) && git checkout $(k8s_version)

require-vendor:

framework: $(FRAMEWORK_OBJ)

$(FRAMEWORK_OBJ): require-godep
	env GOPATH=$(frmwk_gopath):$(k8s_gopath)$${GOPATH:+:$$GOPATH} $(WITH_MESOS_CGO_FLAGS) \
	  godep get github.com/mesosphere/kubernetes-mesos/$(notdir $@)

install: $(OBJS)
	mkdir -p $(DESTDIR)
	/bin/cp -vpf -t $(DESTDIR) $(OBJS)
