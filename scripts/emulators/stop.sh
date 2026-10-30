#!/usr/bin/env bash
# Copyright 2026 Appvia Ltd.
# SPDX-License-Identifier: Apache-2.0

# Stops the emulators recorded in this state dir by start-azurite.sh and
# start-s3.sh, waits for their ports to close, and removes their data
# directories. Exits 0 only when every recorded process and port is gone (or
# nothing is recorded and no port is held); otherwise exits 1 and keeps the data.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
. "$script_dir/lib.sh"

any_port_open() {
	local p

	for p in "$@"; do
		if port_open 127.0.0.1 "$p"; then
			return 0
		fi
	done

	return 1
}

stop_one() {
	local name=$1
	shift

	if [[ ! -s "$STATE_DIR/$name.procs" && ! -s "$STATE_DIR/$name.pgid" ]]; then
		if any_port_open "$@"; then
			log "error: nothing recorded for $name, but a port in $* is open; not signalling a process this state dir did not start"
			return 1
		fi

		log "$name: nothing recorded, nothing to stop"
		return 0
	fi

	if [[ -n "$(live_recorded "$name")" ]]; then
		signal_recorded "$name" TERM

		if ! wait_recorded_gone "$name" 10; then
			log "$name: still running 10s after SIGTERM, sending SIGKILL"
			signal_recorded "$name" KILL

			if ! wait_recorded_gone "$name" 5; then
				log "error: $name still running after SIGKILL: $(live_recorded "$name" | tr '\n' ' ')"
				return 1
			fi
		fi
	fi

	if any_port_open "$@"; then
		log "error: $name processes exited but a port in $* is still open"
		return 1
	fi

	# A recorded pid can be reused by an unrelated process; report it, never signal it.
	while IFS=$'\t' read -r rpid rcmd; do
		if [[ -n "$rpid" ]] && kill -0 "$rpid" 2>/dev/null && [[ "$(cmd_of "$rpid")" != "$rcmd" ]]; then
			log "$name: recorded pid $rpid now runs \"$(cmd_of "$rpid")\" instead of \"$rcmd\"; not signalled"
		fi
	done <"$STATE_DIR/$name.procs"

	rm -f "$STATE_DIR/$name.pid" "$STATE_DIR/$name.pgid" "$STATE_DIR/$name.procs"
	log "$name: stopped, ports $* closed"
}

failed=0
stop_one azurite "${IPAM_AZURITE_PORT:-10000}" || failed=1
stop_one minio "${IPAM_S3_PORT:-9000}" "${IPAM_S3_CONSOLE_PORT:-9001}" || failed=1

if ((failed)); then
	die "emulators are still running or ports are open; data directories kept"
fi

if [[ -s "$STATE_DIR/azurite.data" ]]; then
	rm -rf "$(cat "$STATE_DIR/azurite.data")"
	rm -f "$STATE_DIR/azurite.data"
fi

if [[ -s "$STATE_DIR/s3.json" ]]; then
	data_dir="$(jq -r '.data_dir // ""' "$STATE_DIR/s3.json")"

	if [[ -n "$data_dir" ]]; then
		rm -rf "$data_dir"
	fi
fi
