#!/bin/sh
#
# usage:
#
# echo 'param1=value1&param2=value2' | webhook.sh text/plain http://some.user.url?a=b\&c=d
#
die() {
  echo "$*" >&2
  exit 1
}

contentType="$1"
url="$2"

test -n "$contentType" || die Missing content-type param
test -n "$url" || die Missing url param

tmpfile=/tmp/webhook.$$
trap "rm -f $tmpfile || true" EXIT

len=$(cat | tee $tmpfile | wc -c)

proto=$(echo $url | grep :// | sed -e's,^\(.*://\).*,\1,g')
test -n "$proto" || proto=http://
test "http://" = "$proto" || die Cannot POST using protocol \"$proto\"

url=$(echo $url | sed -e s,^$proto,,g)
hostport=$(echo $url | cut -d/ -f1)
port=$(echo $hostport | grep : | sed -e's,^.*:\([0-9]\+\)$,\1,g')
test -n "$port" && host=$(echo $hostport | sed -e's,:'"$port"'$,,g') || port=80 host="$hostport"
test -n "$host" || die Missing HTTP host

path="/$(echo $url | grep / | cut -d/ -f2-)"

echo POSTing to host $host port $port path $path type $contentType len $len

(busybox echo -ne "POST ${path} HTTP/1.0\r\nHost: ${host}\r\nContent-Type: ${contentType}\r\nContent-Length: ${len}\r\n\r\n"; cat $tmpfile) | \
  nc -i 10 $host $port
