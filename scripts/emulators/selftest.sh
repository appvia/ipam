#!/usr/bin/env bash
# Copyright 2026 Appvia Ltd.
# SPDX-License-Identifier: Apache-2.0

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
stop="$script_dir/stop.sh"

log() { printf '%s\n' "$*" >&2; }

failures=0
work="$(mktemp -d "${TMPDIR:-/tmp}/ipam-selftest.XXXXXX")"
spawned=""

cleanup() {
	local p
	for p in $spawned; do
		kill -KILL "$p" 2>/dev/null || true
	done
	rm -rf "$work"
}
trap cleanup EXIT

check() {
	local desc=$1
	shift

	if "$@"; then
		log "ok: $desc"
	else
		log "FAIL: $desc"
		failures=$((failures + 1))
	fi
}

port_open() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

next_free_port() {
	local p=$1

	while port_open "$p"; do
		p=$((p + 1))
	done
	echo "$p"
}

gone() {
	local s

	s="$(ps -o stat= -p "$1" 2>/dev/null | tr -d ' ')"
	[[ -z "$s" || "$s" == Z* ]]
}

az_port="$(next_free_port 20000)"
s3_port="$(next_free_port 20010)"
console_port="$(next_free_port 20020)"

run_stop() {
	IPAM_EMULATOR_STATE_DIR="$1" IPAM_AZURITE_PORT="$az_port" \
		IPAM_S3_PORT="$s3_port" IPAM_S3_CONSOLE_PORT="$console_port" \
		bash "$stop" >"$1.out" 2>&1
}

case_ghost_pid() {
	local dir="$work/ghost" start rc elapsed

	check "precondition: pid 99999 has no process" gone 99999
	mkdir -p "$dir"
	printf '99999\t\n' >"$dir/azurite.procs"

	start=$SECONDS
	run_stop "$dir"
	rc=$?
	elapsed=$((SECONDS - start))

	check "ghost pid 99999 stop exits 0" test "$rc" -eq 0
	check "ghost pid 99999 stop takes at most 2s" test "$elapsed" -le 2
}

case_reused_pid_decoy() {
	local dir="$work/decoy" decoy rc

	mkdir -p "$dir"
	sleep 300 &
	decoy=$!
	spawned="$spawned $decoy"
	printf '%s\t%s\n' "$decoy" "a command the decoy does not run" >"$dir/azurite.procs"

	run_stop "$dir"
	rc=$?

	check "reused-pid decoy: stop exits 0" test "$rc" -eq 0
	check "reused-pid decoy: still running after stop" kill -0 "$decoy"
	check "reused-pid decoy: reported as not signalled" grep -q "not signalled" "$dir.out"
	kill -KILL "$decoy" 2>/dev/null || true
}

case_sigterm_ignored() {
	local dir="$work/term" dummy rc

	mkdir -p "$dir"
	set -m
	bash -c 'trap "" TERM; echo ready; while :; do sleep 1; done' >"$dir/ready" 2>&1 &
	dummy=$!
	set +m
	spawned="$spawned $dummy"

	local tries=0
	until grep -q ready "$dir/ready" 2>/dev/null || ((tries >= 100)); do
		sleep 0.1
		tries=$((tries + 1))
	done
	check "SIGTERM-ignoring dummy reported ready" grep -q ready "$dir/ready"

	# shellcheck source-path=SCRIPTDIR
		# shellcheck source=lib.sh
		(IPAM_EMULATOR_STATE_DIR="$dir" && . "$script_dir/lib.sh" && record_launch azurite "$dummy")

	run_stop "$dir"
	rc=$?

	check "SIGTERM-ignoring dummy: stop exits 0" test "$rc" -eq 0
	check "SIGTERM-ignoring dummy: escalated to SIGKILL" grep -q "sending SIGKILL" "$dir.out"
	check "SIGTERM-ignoring dummy: killed" gone "$dummy"
}

case_idle_stop() {
	local dir="$work/idle" rc

	mkdir -p "$dir"
	run_stop "$dir"
	rc=$?
	check "idle stop exits 0" test "$rc" -eq 0
}

case_record_skips_exited() {
	local dir="$work/record"

	mkdir -p "$dir"
	# shellcheck source-path=SCRIPTDIR
		# shellcheck source=lib.sh
		(IPAM_EMULATOR_STATE_DIR="$dir" && . "$script_dir/lib.sh" && record_launch azurite 99999)
	check "record_launch on an exited pid writes no line" test -z "$(cat "$dir/azurite.procs")"
}

case_ghost_pid
case_reused_pid_decoy
case_sigterm_ignored
case_idle_stop
case_record_skips_exited

if ((failures)); then
	log "selftest: $failures check(s) failed"
	exit 1
fi
log "selftest: all checks passed"
