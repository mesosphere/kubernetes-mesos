#!/bin/bash -vx
ontrap() {
	local rc=$?
	test -n "$file" -a -f "$file" && rm -f "$file"
	exit $rc
}

trap ontrap EXIT

die() {
	echo "ERROR: $*" >&2
	exit 1
}
# open a fifo for writing, instant success even there is no reader
#   redirfd -w -nb n fifo prog..
#
#   redirfd -w n fifo ...  # (blocks until the fifo is read)
# ... later
#   redirfd -rnb 0 fifo ... # Opening a fifo for reading, with instant success even if there is no writer, and blocking at the first attempt to read from it
#   s6-log -bp t /mnt/tmpfs/uncaught-logs  # (reads from the blocked fifo)
redir=$(dirname $0)/../_build/bin/k8sm-redirfd
test -x $redir || die echo "missing $redir"

#
# black magic testing
#
($redir -n -b w  1 /tmp/fifo1 echo "fifo was read") &

for i in {1..2}; do echo sleeping $i; sleep 1; done

$redir -n -b r 0 /tmp/fifo1 cat
wait

#
# test simple input redirection
#
file=/tmp/file1.$$
echo "file contents" >$file
x=$($redir r 0 $file cat)
test "$x" = "file contents" || die Invalid contents of $file
rm -f $file

#
# test simple output redirection
#
file=/tmp/file2
$redir w 1 $file echo "hello james" || die Failed to redirect output to $file
test "$(cat $file)" = "hello james" || die Rediect output: file contents mismatch .. $file
rm -f $file

file=/tmp/file2.1
echo "anna" >$file
$redir w 1 $file echo "hello james" || die Failed to redirect output to $file
test "$(cat $file)" = "hello james" || die Rediect output: file contents mismatch .. $file
rm -f $file

file=/tmp/file3
echo -n "bill " >$file
$redir a 1 $file echo "hello james" || die Failed to redirect output to $file
test "$(cat $file)" = "bill hello james" || die Rediect output: file contents mismatch .. $file
rm -f $file

file=/tmp/file4
$redir c 1 $file echo "hello james" && die Expected redirect output to fail for $file
rm -f $file

file=/tmp/file5
echo -n "bill " >$file
$redir c 1 $file echo "hello james" || die Failed to redirect output to $file
test "$(cat $file)" = "bill hello james" || die Rediect output: file contents mismatch .. $file
rm -f $file

file=/tmp/file6
echo -n "bill " >$file
$redir x 1 $file echo "hello james" && die Expected redirect output to fail for $file
rm -f $file

file=/tmp/file7
$redir x 1 $file echo "hello james" || die Failed to redirect output to $file
test "$(cat $file)" = "hello james" || die Rediect output: file contents mismatch .. $file
rm -f $file

