#!/usr/bin/env bash
# Copyright 2026 Appvia Ltd.
# SPDX-License-Identifier: Apache-2.0

# Starts the Azurite blob emulator in the background on a fresh data directory,
# waits for the port, and prints the well-known development connection string.
# Stop it with scripts/emulators/stop.sh.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
. "$script_dir/lib.sh"

port="${IPAM_AZURITE_PORT:-10000}"
# The npm package is "azurite"; it ships the "azurite-blob" binary.
version="${IPAM_AZURITE_VERSION:-3.37.0}"
logfile="$STATE_DIR/azurite.log"

if port_open 127.0.0.1 "$port"; then
	die "port $port is already in use; run scripts/emulators/stop.sh or free it"
fi

# tmp_root is assigned in the sourced lib.sh.
# shellcheck disable=SC2154
data_dir="$(mktemp -d "$tmp_root/ipam-azurite.XXXXXX")"

# set -m gives the launch its own process group, which stop.sh relies on.
set -m
nohup npx --yes --package "azurite@$version" azurite-blob \
	--silent --skipApiVersionCheck \
	--location "$data_dir" \
	--blobHost 127.0.0.1 --blobPort "$port" \
	</dev/null >"$logfile" 2>&1 &
pid=$!
set +m

echo "$pid" >"$STATE_DIR/azurite.pid"
echo "$data_dir" >"$STATE_DIR/azurite.data"

record_launch azurite "$pid"

# The first run downloads the package, so allow a generous bounded wait.
wait_port 127.0.0.1 "$port" "${IPAM_AZURITE_START_TIMEOUT:-180}" "$pid" "$logfile"

# Re-record once listening so the node child that npx spawns is captured too.
record_launch azurite "$pid"

conn="DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==;BlobEndpoint=http://127.0.0.1:$port/devstoreaccount1;"

jq -n --arg version "$version" --arg conn "$conn" --arg data "$data_dir" \
	--argjson pid "$pid" --arg source "npx --yes --package azurite@$version azurite-blob" \
	'{version: $version, source: $source, connection_string: $conn, data_dir: $data, pid: $pid}' \
	>"$STATE_DIR/azurite.json"

log "azurite $version listening on 127.0.0.1:$port (pid $pid, data $data_dir)"
printf '%s\n' "$conn"
