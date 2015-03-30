#!/bin/bash

function die() {
	test "x$1" = "x" || echo -E "$*" >&2
	exit 1
}

test -n "$GOPATH" || die Missing GOPATH
export READLINK=readlink
case $(uname|tr '[:upper:]' '[:lower:]') in
  darwin*)
	# homebrew coreutils package provides GNU-compat readlink
	pkg="${GOPATH%%:*}"
	READLINK=greadlink
    ;;
  cygwin*)
	pkg="${GOPATH%%;*}"
    ;;
  *)
	pkg="${GOPATH%%:*}"
    ;;
esac

test -n "$USR_LOCAL_BASH" || export USR_LOCAL_BASH=/usr/local/bin/bash
major_version=$(echo $BASH_VERSION | cut -f1 -d. 2>/dev/null)
test "${major_version}" = "4" || {
	# work-around for users (homebrew) that may have a newer bash installed
	echo Detected older major version of bash $major_version checking for a newer version at $USR_LOCAL_BASH
	test -x $USR_LOCAL_BASH || die No newer of bash found at $USR_LOCAL_BASH
	major_version=$(exec $USR_LOCAL_BASH -c 'echo $BASH_VERSION'|cut -f1 -d. 2>/dev/null)
	test "${major_version}" = '4' || die "Bash version at $USR_LOCAL_BASH is too old: major version = "${major_version}
	exec "$USR_LOCAL_BASH" "$0" "${@}"
}

echo GO packages in $pkg will be patched

test -n "$pkg" || die Invalid GOPATH=$GOPATH
home=$(dirname "$0")
home=$($READLINK -f "$home")
echo Patch directory $home

# Add new k/v pairs for each project repo that may require patching
# and update the README.md as entries are modified here
declare -A pmap
pmap=(
  [k8s]=github.com/GoogleCloudPlatform/kubernetes
  [mgo]=github.com/mesos/mesos-go
)

# TODO(jdef) at some point we should be able to apply patches with
# multiple hunks, ala:
# http://unix.stackexchange.com/questions/65698/how-to-make-patch-ignore-already-applied-hunks

for k in "${!pmap[@]}"; do
  repo="${pmap["${k}"]}"
  echo "Checking patches for ${k}.. ($repo)"
  find "${home}" -type f -name "${k}---issue*.patch" | while IFS= read -r f; do
    cmd=( patch -p1 -s -r- -i"$f" )
    echo -n -E "${cmd[@]}"
    output=$(cd "${pkg}/src/${repo}" && pwd && "${cmd[@]}") && echo || {
      echo -E "$output" | \
        grep -q 'Reversed (or previously applied) patch detected' && \
          echo " (already applied)" || \
          { echo; die "Failed to apply patch ${f}: ${output}"; }
    }
  done
done
