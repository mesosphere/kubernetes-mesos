
# the assumption is that current_dir is something along the lines of:
# 	/home/bob/yourproject/src/github.com/mesosphere/kubernetes-mesos

mkfile_path	:= $(abspath $(lastword $(MAKEFILE_LIST)))
current_dir	:= $(patsubst %/,%,$(dir $(mkfile_path)))
fail		:= ${MAKE} --no-print-directory --quiet -f $(current_dir)/Makefile error

K8S_CMD		:= \
                   github.com/GoogleCloudPlatform/kubernetes/cmd/controller-manager	\
                   github.com/GoogleCloudPlatform/kubernetes/cmd/kubecfg		\
                   github.com/GoogleCloudPlatform/kubernetes/cmd/proxy
FRAMEWORK_CMD	:= \
                   github.com/mesosphere/kubernetes-mesos/kubernetes-mesos		\
                   github.com/mesosphere/kubernetes-mesos/kubernetes-executor


# TODO: make this something more reasonable
DESTDIR		?= /target

.PHONY: all error require-godep framework require-vendor proxy install info bootstrap require-gopath format

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

all: proxy framework

error:
	echo -E "$@: ${MSG}" >&2
	false

require-godep: require-gopath
	@which godep >/dev/null || ${fail} MSG="Missing godep tool, aborting"

require-gopath:
	@test -n "$(GOPATH)" || ${fail} MSG="GOPATH undefined, aborting"

proxy: require-godep
	go install $(K8S_CMD)

require-vendor:

framework: require-godep
	env $(WITH_MESOS_CGO_FLAGS) go install $${WITH_RACE:+-race} $(FRAMEWORK_CMD)

format: require-gopath
	go fmt	github.com/mesosphere/kubernetes-mesos/kubernetes-mesos \
		github.com/mesosphere/kubernetes-mesos/kubernetes-executor \
		github.com/mesosphere/kubernetes-mesos/scheduler \
		github.com/mesosphere/kubernetes-mesos/service \
		github.com/mesosphere/kubernetes-mesos/master \
		github.com/mesosphere/kubernetes-mesos/executor

install: all
	mkdir -p $(DESTDIR)
	(pkg="$(GOPATH)"; pkg="$${pkg%%:*}"; for x in $(notdir $(K8S_CMD) $(FRAMEWORK_CMD)); do \
	 /bin/cp -vpf -t $(DESTDIR) "$${pkg}"/bin/$$x; done)

info:
	@echo GOPATH=$(GOPATH)
	@echo CGO_CFLAGS="$(CGO_CFLAGS)"
	@echo CGO_CPPFLAGS="$(CGO_CPPFLAGS)"
	@echo CGO_CXXFLAGS="$(CGO_CXXFLAGS)"
	@echo CGO_LDFLAGS="$(CGO_LDFLAGS)"
	@echo RACE_FLAGS=$${WITH_RACE:+-race}

bootstrap: require-godep
	godep restore
