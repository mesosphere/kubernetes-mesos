#!/bin/bash

function die() {
	test "x$1" = "x" || echo -E "$1" >&2
	exit 1
}

test -n "$GOPATH" || die Missing GOPATH
pkg="${GOPATH%%:*}"
echo GO packages in $pkg will be patched

test -n "$pkg" || die Invalid GOPATH=$GOPATH
home=$(dirname "$0")
home=$(readlink -f "$home")
echo Patch directory $home

# Add new k/v pairs for each project repo that may require patching
# and update the README.md as entries are modified here
declare -A pmap
pmap=(
  [k8s]=github.com/GoogleCloudPlatform/kubernetes
)

# TODO(jdef) at some point we should be able to apply patches with
# multiple hunks, ala:
# http://unix.stackexchange.com/questions/65698/how-to-make-patch-ignore-already-applied-hunks

for k in "${!pmap[@]}"; do
  repo="${pmap["${k}"]}"
  echo "Checking patches for ${k}.. ($repo)"
  find "${home}" -type f -name "${k}---issue*.patch" | while IFS= read -r f; do
    #ff="${f%%.patch}"
    #test "x$f" != "x$ff" || continue
    cmd=( patch -p1 -s -r- -i"$f" )
    echo -n -E "${cmd[@]}"
    output=$(cd "${pkg}/src/${repo}" && "${cmd[@]}") && echo || {
      echo -E "$output" | \
        grep -q 'Reversed (or previously applied) patch detected' && \
          echo " (already applied)" || \
          { echo; die "Failed to apply patch ${f}: ${output}"; }
    }
  done
done
