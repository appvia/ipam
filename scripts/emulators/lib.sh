# Copyright 2026 Appvia Ltd.
# SPDX-License-Identifier: Apache-2.0

# Shared helpers for the emulator scripts. Source it; do not execute it.
# shellcheck shell=bash

set -euo pipefail

tmp_root="${TMPDIR:-/tmp}"
tmp_root="${tmp_root%/}"

# STATE_DIR holds pid files, logs and the state files the probe reads.
STATE_DIR="${IPAM_EMULATOR_STATE_DIR:-$tmp_root/ipam-emulators}"
mkdir -p "$STATE_DIR"

log() { printf '%s\n' "$*" >&2; }

die() {
	log "error: $*"
	exit 1
}

# port_open HOST PORT succeeds when something accepts TCP connections.
port_open() { (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null; }

# wait_port HOST PORT TIMEOUT_SECONDS PID LOGFILE waits until PORT accepts
# connections, failing fast when PID exits or the deadline passes.
wait_port() {
	local host=$1 port=$2 timeout=$3 pid=$4 logfile=$5
	local deadline=$((SECONDS + timeout))

	until port_open "$host" "$port"; do
		if ! kill -0 "$pid" 2>/dev/null; then
			log "--- last lines of $logfile"
			tail -n 20 "$logfile" >&2 || true
			die "process $pid exited before listening on $host:$port"
		fi

		if ((SECONDS >= deadline)); then
			die "timed out after ${timeout}s waiting for $host:$port (log: $logfile)"
		fi

		sleep 0.5
	done
}

# bounded SECONDS CMD... runs CMD with a hard deadline.
bounded() {
	local secs=$1
	shift

	if command -v timeout >/dev/null 2>&1; then
		timeout "$secs" "$@"
	else
		perl -e 'alarm shift; exec @ARGV or die "exec: $!"' "$secs" "$@"
	fi
}

# descendants PID prints the descendants of PID, children before parents.
descendants() {
	local child

	for child in $(pgrep -P "$1" 2>/dev/null || true); do
		descendants "$child"
		printf '%s\n' "$child"
	done
}

# cmd_of PID prints the command line of PID, or nothing once it has exited.
cmd_of() { ps -o command= -p "$1" 2>/dev/null | sed 's/^ *//;s/ *$//' || true; }

# stat_of PID prints the process state of PID, or nothing once it has exited.
stat_of() { ps -o stat= -p "$1" 2>/dev/null | tr -d ' ' || true; }

# record_launch NAME PID saves the process group of PID and the command lines of
# PID and its descendants to $STATE_DIR/NAME.pgid and NAME.procs. Launches run
# under set -m, so the group id equals PID and survives the npm wrapper exiting.
# A process that has already exited has no command line and is not recorded.
record_launch() {
	local name=$1 pid=$2 child cmd

	ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ' >"$STATE_DIR/$name.pgid" || true
	: >"$STATE_DIR/$name.procs"
	for child in "$pid" $(descendants "$pid"); do
		cmd="$(cmd_of "$child")"
		if [[ -n "$cmd" ]]; then
			printf '%s\t%s\n' "$child" "$cmd" >>"$STATE_DIR/$name.procs"
		fi
	done
}

recorded_cmd() {
	local _pid cmd

	while IFS=$'\t' read -r _pid cmd; do
		if [[ "$cmd" == "$2" ]]; then
			return 0
		fi
	done <"$STATE_DIR/$1.procs"

	return 1
}

# live_recorded NAME prints the recorded processes that still run their recorded
# command line, and the members of their process group that run a recorded one.
# A pid with no process has an empty command line, so it never matches and is gone.
live_recorded() {
	local name=$1 pid cmd pgid stat g

	if [[ -f "$STATE_DIR/$name.procs" ]]; then
		while IFS=$'\t' read -r pid cmd; do
			# An empty recorded command matches nothing. A zombie has exited and cannot be signalled.
			if [[ -n "$pid" && -n "$cmd" && "$(cmd_of "$pid")" == "$cmd" && "$(stat_of "$pid")" != Z* ]]; then
				printf '%s\n' "$pid"
			fi
		done <"$STATE_DIR/$name.procs"
	fi

	[[ -s "$STATE_DIR/$name.pgid" ]] || return 0
	g="$(cat "$STATE_DIR/$name.pgid")"

	ps -axo pid=,pgid=,stat=,command= | while read -r pid pgid stat cmd; do
		if [[ "$pgid" == "$g" && "$stat" != Z* ]] && recorded_cmd "$name" "$cmd"; then
			printf '%s\n' "$pid"
		fi
	done
}

# signal_recorded NAME SIGNAL sends SIGNAL to every live recorded process.
signal_recorded() {
	local pid

	live_recorded "$1" | sort -u | while read -r pid; do
		kill "-$2" "$pid" 2>/dev/null || true
	done
}

# wait_recorded_gone NAME SECONDS succeeds once no recorded process still runs.
wait_recorded_gone() {
	local deadline=$((SECONDS + $2))

	while [[ -n "$(live_recorded "$1")" ]]; do
		((SECONDS < deadline)) || return 1
		sleep 0.5
	done
}
