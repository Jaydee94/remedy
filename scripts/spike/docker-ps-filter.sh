#!/bin/sh
# Throwaway measurement for operation-6 spec section 5 (see docs/research/docker-ps-filter.md).
#
#   sh scripts/spike/docker-ps-filter.sh docker   # part (a): which names does `docker ps -a --filter name=...` list?
#   sh scripts/spike/docker-ps-filter.sh sudo     # part (b): how does real sudo read the sudoers line with the filter?
#   sh scripts/spike/docker-ps-filter.sh          # both
#
# Part (a) starts five throwaway containers (remedy-a, remedy-drill, remedy-drill2, xremedy-c, paperless-db) from a small
# local image and REFUSES to run if a container with one of these names exists. It removes only these on exit.
# Part (b) builds its own image from dev/ssh/Dockerfile (tag ops6-spike-ssh:test) and runs one container
# (ops6-spike-sudo), with throwaway sudoers files inside it only. It does not use dev/ssh/up.sh, ~/remedy-ssh or the
# testbed's names. Both are removed on exit. POSIX sh; nothing here touches any other container.
set -u

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
IMAGE=${SPIKE_IMAGE:-alpine:latest}
NAMES="remedy-a remedy-drill remedy-drill2 xremedy-c paperless-db"
SUDO_IMG=ops6-spike-ssh:test
SUDO_CTR=ops6-spike-sudo
MADE_NAMES=""
MADE_SUDO=0

cleanup() {
	for n in $MADE_NAMES; do
		docker rm -f "$n" >/dev/null 2>&1
	done
	if [ "$MADE_SUDO" = 1 ]; then
		docker rm -f "$SUDO_CTR" >/dev/null 2>&1
		docker rmi "$SUDO_IMG" >/dev/null 2>&1
	fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM

# ---------------------------------------------------------------- part (a)

list() {
	# $1 = the filter value; prints the names in sorted order on one line
	printf '  --filter %-22s ->' "$1"
	docker ps -a --filter "$1" --format '{{.Names}}' | sort | tr '\n' ' '
	printf '\n'
}

part_docker() {
	echo "== (a) docker's name filter"
	echo "docker server version: $(docker version --format '{{.Server.Version}}')"
	for n in $NAMES; do
		if [ -n "$(docker ps -a -q --filter "name=^/${n}\$")" ]; then
			echo "refusing to run: a container named $n exists already" >&2
			return 1
		fi
	done
	for n in $NAMES; do
		# Registered before the start so a half-made container is removed too; the check above proved the name is free.
		MADE_NAMES="$MADE_NAMES $n"
		docker run -d --pull=never --name "$n" "$IMAGE" sleep 300 >/dev/null || return 1
	done
	echo "containers made: $NAMES"
	echo "all names the daemon lists for them (name=remedy|paperless):"
	docker ps -a --format '  {{.Names}}' --filter 'name=remedy' --filter 'name=paperless' | sort
	echo "inspect .Name (the stored form):"
	for n in $NAMES; do
		printf '  %s\n' "$(docker inspect --format '{{.Name}}' "$n")"
	done
	echo "filters:"
	list 'name=^remedy-'
	list 'name=^remedy-drill$'
	list 'name=^/remedy-'
	list 'name=^/remedy-drill$'
	list 'name=remedy-'
	list 'name=^remedy-drill'
	list 'name=remedy-*'
	list 'name=^remedy-.*'
}

# ---------------------------------------------------------------- part (b)

inbox() {
	# run a command in the throwaway container as root
	docker exec "$SUDO_CTR" "$@"
}

asremedy() {
	docker exec -u remedy "$SUDO_CTR" "$@"
}

putfile() {
	# $1 = path in the container, $2 = the content (one line)
	printf '%s\n' "$2" | docker exec -i "$SUDO_CTR" sh -c "cat > '$1' && chmod 0440 '$1'"
}

visudo_check() {
	# $1 = path
	echo "  \$ visudo -cf $1"
	out=$(inbox visudo -cf "$1" 2>&1)
	rc=$?
	printf '%s\n' "$out" | sed 's/^/    /'
	echo "    exit status: $rc"
}

sudo_try() {
	# $@ = the command as sudo is asked about it
	echo "  \$ sudo -n -l $*   (as remedy)"
	out=$(asremedy sudo -n -l "$@" 2>&1)
	rc=$?
	printf '%s\n' "$out" | sed 's/^/    /'
	echo "    exit status: $rc"
}

part_sudo() {
	echo "== (b) sudo's reading of the sudoers line"
	if docker ps -a -q --filter "name=^/${SUDO_CTR}\$" | grep -q .; then
		echo "refusing to run: a container named $SUDO_CTR exists already" >&2
		return 1
	fi
	docker build -q -t "$SUDO_IMG" "$ROOT/dev/ssh" >/dev/null || return 1
	MADE_SUDO=1
	docker run -d --name "$SUDO_CTR" "$SUDO_IMG" sleep 600 >/dev/null || return 1
	# Only the lines of this spike count: remove the testbed image's own sudoers file inside this container.
	inbox rm -f /etc/sudoers.d/remedy
	echo "sudo: $(inbox sudo -V | head -n 1)"
	echo "docker in the image: $(inbox sh -c 'ls -l /usr/bin/docker')"

	D=/usr/bin/docker
	P='remedy ALL=(root) NOPASSWD: '$D' ps -a --filter name\=^remedy-'
	E='remedy ALL=(root) NOPASSWD: '$D' ps -a --filter name\=^remedy-drill$'
	N='remedy ALL=(root) NOPASSWD: '$D' ps -a --filter name=^remedy-'

	echo
	echo "-- B1: prefix line with the backslash"
	echo "  line: $P"
	putfile /etc/sudoers.d/ops6-prefix "$P"
	visudo_check /etc/sudoers.d/ops6-prefix
	sudo_try $D ps -a --filter 'name=^remedy-'
	sudo_try $D ps -a
	sudo_try $D ps -a --filter 'name=^other-'
	sudo_try $D ps -a --filter 'name=^remedy-drill$'
	sudo_try $D ps -a --filter 'name=remedy-'
	sudo_try $D ps -a --filter 'name=^remedy-' --all
	sudo_try $D ps -a --filter 'name=^remedy-' --filter 'name=paperless'
	echo "  the real execution (the fake docker echoes its argv):"
	echo "  \$ sudo -n $D ps -a --filter name=^remedy-"
	asremedy sudo -n $D ps -a --filter 'name=^remedy-' 2>&1 | head -n 5 | sed 's/^/    /'
	echo "  \$ sudo -n $D ps -a"
	asremedy sudo -n $D ps -a 2>&1 | head -n 5 | sed 's/^/    /'
	inbox rm -f /etc/sudoers.d/ops6-prefix

	echo
	echo "-- B2: exact-name line with the backslash"
	echo "  line: $E"
	putfile /etc/sudoers.d/ops6-exact "$E"
	visudo_check /etc/sudoers.d/ops6-exact
	sudo_try $D ps -a --filter 'name=^remedy-drill$'
	sudo_try $D ps -a --filter 'name=^remedy-drill2$'
	sudo_try $D ps -a --filter 'name=^remedy-'
	sudo_try $D ps -a
	inbox rm -f /etc/sudoers.d/ops6-exact

	echo
	echo "-- B3: prefix line WITHOUT the backslash"
	echo "  line: $N"
	putfile /etc/sudoers.d/ops6-nobs "$N"
	visudo_check /etc/sudoers.d/ops6-nobs
	sudo_try $D ps -a --filter 'name=^remedy-'
	sudo_try $D ps -a
	inbox rm -f /etc/sudoers.d/ops6-nobs

	echo
	echo "-- B4: the line with a star instead (what the old docker ps -a plus a star would mean)"
	echo "  line: remedy ALL=(root) NOPASSWD: $D ps -a --filter name=^remedy-*"
	putfile /etc/sudoers.d/ops6-star "remedy ALL=(root) NOPASSWD: $D ps -a --filter name\\=^remedy-*"
	visudo_check /etc/sudoers.d/ops6-star
	sudo_try $D ps -a --filter 'name=^remedy-'
	sudo_try $D ps -a --filter 'name=^remedy-x --all'
	inbox rm -f /etc/sudoers.d/ops6-star
}

mode=${1:-all}
rc=0
case $mode in
docker) part_docker || rc=1 ;;
sudo) part_sudo || rc=1 ;;
all)
	part_docker || rc=1
	cleanup
	MADE_NAMES=""
	echo
	part_sudo || rc=1
	;;
*)
	echo "usage: sh $0 [docker|sudo|all]" >&2
	rc=2
	;;
esac
exit $rc
