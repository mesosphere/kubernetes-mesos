#!/bin/bash
echo Running with args "${@}"

set -e
set -o pipefail
set -v

test ${#} -eq 0 && CMD=( make bootstrap install ) || CMD=( "${@}" )

GOPKG=github.com/mesosphere/kubernetes-mesos
test -d $SNAP && {
  parent=$(dirname /pkg/src/${GOPKG})
  mkdir -pv $parent
  ln -sv $SNAP $parent/$(basename $GOPKG)
  cd /pkg/src/${GOPKG}
  if [ "x${GIT_BRANCH}" != "x" ]; then
    if test -d '.git'; then
      git checkout "${GIT_BRANCH}"
    else
      echo "ERROR: cannot checkout a branch from non-git-based snapshot" >&2
      exit 1
    fi
  fi
} || {
  mkdir -pv /pkg/src/${GOPKG}
  cd /pkg/src/${GOPKG}
  git clone https://${GOPKG}.git .
  test "x${GIT_BRANCH}" = "x" || git checkout "${GIT_BRANCH}"
}

exec "${CMD[@]}"
