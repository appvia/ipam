#!/usr/bin/env bash
# Copyright 2026 Appvia Ltd.
# SPDX-License-Identifier: Apache-2.0

# Locates the mise-installed MinIO server binary without Docker, starts it in the
# background on a fresh data directory, creates the test bucket, and selects
# the S3 emulator by running the conditional-write probe (MinIO first, then the
# in-process gofakes3 fallback). The selection is recorded in $STATE_DIR/s3.json.
#
# Exits 0 once the selection completed, including when no emulator passed every
# probe: then s3.json records kind "none" with the failed probes and a WARNING
# line is printed, because S3 integration runs only against real AWS
# (IPAM_TEST_S3_BUCKET). Stop MinIO with scripts/emulators/stop.sh.
#
# Install tools beforehand with mise install --locked.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
. "$script_dir/lib.sh"

repo_root="$(cd "$script_dir/../.." && pwd)"
port="${IPAM_S3_PORT:-9000}"
console_port="${IPAM_S3_CONSOLE_PORT:-9001}"
bucket="${IPAM_S3_BUCKET_NAME:-ipam-test}"
access_key="minioadmin"
secret_key="minioadmin"
logfile="$STATE_DIR/minio.log"
attempts="$STATE_DIR/minio.attempts"
selection="$STATE_DIR/s3-selection.json"

: >"$attempts"
rm -f "$selection"

attempt() { printf '%s\n' "$*" | tee -a "$attempts" >&2; }

minio_bin=""
minio_source=""

locate_minio() {
	local path

	if path="$(command -v minio)"; then
		minio_bin="$path"
		minio_source="PATH:$path"
		return 0
	fi

	attempt "PATH: minio not found; run mise install --locked"

	return 1
}

# write_state SOURCE ENDPOINT DATA_DIR PID records the MinIO candidate for the
# selection probe.
write_state() {
	jq -n --arg source "$1" --arg endpoint "$2" --arg bucket "$bucket" --arg data "$3" \
		--argjson pid "$4" --rawfile install "$attempts" \
		'{kind: "minio", source: $source, endpoint: $endpoint, bucket: $bucket, data_dir: $data,
		  pid: $pid, install_attempts: ($install | split("\n") | map(select(. != "")))}' \
		>"$STATE_DIR/s3.json"
}

start_minio() {
	local data_dir pid deadline

	if port_open 127.0.0.1 "$port"; then
		die "port $port is already in use; run scripts/emulators/stop.sh or free it"
	fi

	# tmp_root is assigned in the sourced lib.sh.
	# shellcheck disable=SC2154
	data_dir="$(mktemp -d "$tmp_root/ipam-minio.XXXXXX")"

	set -m
	MINIO_ROOT_USER="$access_key" MINIO_ROOT_PASSWORD="$secret_key" MINIO_BROWSER=off \
		nohup "$minio_bin" server "$data_dir" \
		--address "127.0.0.1:$port" --console-address "127.0.0.1:$console_port" --quiet \
		</dev/null >"$logfile" 2>&1 &
	pid=$!
	set +m

	echo "$pid" >"$STATE_DIR/minio.pid"
	record_launch minio "$pid"
	write_state "$minio_source" "http://127.0.0.1:$port" "$data_dir" "$pid"

	wait_port 127.0.0.1 "$port" "${IPAM_S3_START_TIMEOUT:-60}" "$pid" "$logfile"

	deadline=$((SECONDS + 60))

	until curl -fsS -o /dev/null -m 5 "http://127.0.0.1:$port/minio/health/live"; do
		((SECONDS < deadline)) || die "MinIO did not report healthy within 60s (log: $logfile)"
		sleep 0.5
	done

	curl -fsS -m 15 -o /dev/null \
		--aws-sigv4 "aws:amz:us-east-1:s3" --user "$access_key:$secret_key" \
		-X PUT "http://127.0.0.1:$port/$bucket" ||
		die "could not create bucket $bucket"

	log "minio ($minio_source) started on 127.0.0.1:$port (pid $pid, data $data_dir, bucket $bucket); selection pending"
}

if locate_minio; then
	attempt "located: $minio_source"
	start_minio
else
	attempt "no MinIO binary available"
	write_state "no MinIO binary available" "" "" null
fi

if ! (cd "$repo_root" && bounded 900 go run ./scripts/emulators/probe \
	-select-s3 -state-dir "$STATE_DIR" -out "$selection"); then
	die "the S3 selection probe did not complete"
fi

jq --slurpfile sel "$selection" '. + ($sel[0] | {kind, source, selected, failed_probes, attempts})' \
	"$STATE_DIR/s3.json" >"$STATE_DIR/s3.json.tmp"
mv "$STATE_DIR/s3.json.tmp" "$STATE_DIR/s3.json"

if [[ "$(jq -r .selected "$STATE_DIR/s3.json")" == true ]]; then
	log "selected S3 emulator: $(jq -r '.kind + " (" + .source + ")"' "$STATE_DIR/s3.json")"
else
	note="no S3 emulator passed every conditional-write probe"

	if [[ "$(jq -r .pid "$STATE_DIR/s3.json")" != null ]]; then
		note="MinIO started but NOT selected: failed conditional-delete probes"
	fi

	log "WARNING: $note;" \
		"failed probes: $(jq -r '.failed_probes | join(", ")' "$STATE_DIR/s3.json");" \
		"attempts: $(jq -r '[.attempts[] | .kind + "=" + (.failed_probes | join("+"))] | join(", ")' "$STATE_DIR/s3.json");" \
		"S3 integration runs only against real AWS (IPAM_TEST_S3_BUCKET). Detail: $STATE_DIR/s3.json"
fi
