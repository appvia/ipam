# scripts/emulators/

## OVERVIEW
Docker-free Azurite + MinIO lifecycle for `make test-integration`, plus a Go probe that proves each emulator honours the conditional-write semantics the backends rely on. Distinct domain: bash process management + emulator selection (score ~8).

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Start Azurite | `start-azurite.sh` | `npx azurite@$IPAM_AZURITE_VERSION` (default 3.37.0) on 127.0.0.1; prints the dev connection string |
| Start/select S3 | `start-s3.sh` | MinIO (PATH, cache, brew, dl.min.io, `go install` of final commit) then `probe -select-s3` |
| Stop both | `stop.sh` | TERM, then KILL after 10s; exits 1 if a recorded process or port survives |
| Shared helpers | `lib.sh` | source it, never execute; `STATE_DIR`, `wait_port`, `bounded`, `record_launch`, `live_recorded` |
| Stop-logic tests | `selftest.sh` | ghost pid, reused-pid decoy, ignored SIGTERM, idle stop, exited launch |
| Conditional-write probe | `probe/` (`package main`) | `checks.go`: stale If-Match PUT/DELETE, If-None-Match on existing, If-Match on missing, concurrent PUT |

## STATE
- `STATE_DIR = $IPAM_EMULATOR_STATE_DIR` or `${TMPDIR%/}/ipam-emulators`; holds `azurite.json`, `s3.json`, `*.pid`, `*.pgid`, `*.procs`, logs, cached `bin/`.
- The probe reads `azurite.json`/`s3.json` from `-state-dir` and writes its report to `-out` (default `.omo/evidence/emulators.json`).
- `s3.json` `kind: "none"` / `selected: false` = no S3 emulator passed; S3 integration then runs only against real AWS (`IPAM_TEST_S3_BUCKET`). `start-s3.sh` still exits 0 and prints a WARNING.
- Ports/env: `IPAM_AZURITE_PORT` (10000), `IPAM_S3_PORT` (9000), `IPAM_S3_CONSOLE_PORT` (9001), `IPAM_S3_BUCKET_NAME` (`ipam-test`), `IPAM_MINIO_GO_VERSION`, `IPAM_AZURITE_START_TIMEOUT` (180), `IPAM_S3_START_TIMEOUT` (60).
- Probe exit: 0 all selected emulators pass, 1 a probe failed, 2 the report could not be written.

## USAGE
```bash
bash scripts/emulators/start-azurite.sh   # stdout: connection string -> export AZURE_STORAGE_CONNECTION_STRING
bash scripts/emulators/start-s3.sh        # then set IPAM_TEST_S3_ENDPOINT=http://127.0.0.1:9000 (+ IPAM_TEST_S3_SKIP_CONDITIONAL_DELETE=1)
go run ./scripts/emulators/probe          # run from repo root
make test-integration
bash scripts/emulators/stop.sh
bash scripts/emulators/selftest.sh        # uses free ports from 20000 and a temp state dir
```

## CONVENTIONS
- Launch under `set -m` so the process group id equals the pid; `record_launch` stores pgid + command lines, re-recorded once the port listens (captures the npx node child).
- Every wait is bounded (`wait_port` fails fast if the pid exits; `bounded SECONDS CMD`); no unbounded sleeps.
- Each start uses a fresh `mktemp -d` data dir; `stop.sh` removes data only on a clean stop.
- Known limitation (2026-10): no tested S3 emulator (MinIO final commit, gofakes3 v1.2.0) honours If-Match on DELETE.

## ANTI-PATTERNS
- NEVER signal a pid the state dir did not record, or a recorded pid whose command line changed (pid reuse); report it instead.
- NEVER kill by port or process name; `stop.sh` refuses when a port is open with nothing recorded.
- NEVER use Docker here; scripts must run on a bare CI runner (needs `jq`, `curl`, `npx`, `go`).
- The Azurite account key is the public well-known dev key; never add real credentials to these scripts.
