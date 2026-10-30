# PROJECT KNOWLEDGE BASE

**Generated:** 2026-10-30
**Commit:** none (no commits yet; tree untracked)
**Branch:** main

## OVERVIEW
Go IPAM client library (`github.com/appvia/ipam`, module root = client entrypoint) that keeps CIDR pools as JSON documents in object storage and coordinates writers only through conditional writes (CAS). Ships the cobra CLI `ipam`. Go 1.27.3 / golangci-lint 2.14.0 pinned in `mise.toml`.

## STRUCTURE
```
ipam/
├── *.go              # package ipam: Client, pools, allocations, Open URL parser
├── internal/alloc    # pure in-memory CIDR math (Next/explicit, netipx); no I/O
├── internal/document # v1 JSON codec, envelope + schema_version check, testdata/*_v1.json
├── storage/          # Storage interface + memory/local/s3/azblob backends + storagetest contract
├── cmd/ipam/         # CLI (cobra), exit codes, `ipam doctor`
└── scripts/emulators # Azurite/MinIO start/stop + Go probe for integration runs
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Client options, retry/backoff, per-pool mutex | `client.go` | defaults: 8 attempts, 10ms base, 250ms cap |
| Pool create/delete/update, incarnations, residue | `pool.go`, `doc.go` | `doc.go` is the design contract; read it first |
| Allocate/Release/update allocation | `allocation.go` | CAS on `reservations/<id>.json` is the linearization point |
| Backend URL grammar | `open.go` (`OpenStorage`, `parseOpenURL`) | security boundary, see ANTI-PATTERNS |
| Sentinels, `OpError{Op,Pool,ID,Reason,Err}` | `errors.go` | Reasons: `contention`, `pool_incomplete`, `pool_exists`, ... |
| ID/CIDR canonicalisation | `canonical.go`, `validate.go` | pool ID `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`; alloc IDs UUIDv7 |
| Doc encode/decode | `document.go` -> `internal/document` | |
| Runnable examples | `example_test.go` | `go test -run Example .` |

## CODE MAP
Centrality unmeasured (gopls not used); roles from source.

| Symbol | Type | Location | Role |
|--------|------|----------|------|
| `Client` / `New` / `Option` | struct/func | `client.go` | entrypoint over a `storage.Storage` |
| `Open` / `OpenStorage` | func | `open.go` | URL -> backend (-> Client) |
| `WithMaxAttempts` | Option | `client.go` | `0` = retry until ctx ends; negative rejected |
| `ErrNotFound/ErrConflict/ErrPoolExhausted/ErrInvalidCIDR/ErrInvalidArgument/ErrUnsupportedSchema` | var | `errors.go` | public sentinels; CLI maps them to exit codes |
| `storage.Storage` | interface | `storage/storage.go` | Get / CreateIfAbsent / PutIfMatch / DeleteIfMatch / List |
| `storagetest.Run` | func | `storage/storagetest/contract.go` | backend contract suite |

## CONVENTIONS
- Two docs per pool: `pools/<id>.json` + `reservations/<id>.json`, same random 16-byte hex `incarnation`; usable only when both exist with equal incarnations. Mismatch on every attempt -> `pool_incomplete` (wraps `ErrNotFound`).
- Child pool create order: parent carve-out (`pool:<child>:<incarnation>`) -> child reservations -> child pool doc. `CreatePool` only ever `CreateIfAbsent`s and never adopts a doc it did not write; `DeletePool` clears all residue shapes; it never scans for abandoned carve-outs.
- Retry loop: read version -> mutate -> `PutIfMatch`/`DeleteIfMatch`; `ErrPreconditionFailed` -> re-read + full-jitter backoff (`waitRetry`); exhaustion -> `contentionError`.
- `storage.ErrUnknownOutcome` (request may have landed): treat like a conflict but FIRST re-read and check whether our own write landed (allocation ID / incarnation / metadata equality). Never treat it as success or as a plain failure.
- In-process per-pool mutex (`poolLock`) is only a contention optimisation; correctness must hold with `withoutPoolLocks()` (model tests run both ways).
- Long-lived callers (Terraform provider, services): `WithMaxAttempts(0)` + `context.WithTimeout`; default 8 attempts can surface transient `pool_incomplete` on concurrent same-spec creates.
- Documents carry `schema_version: 1`; any other value -> `ErrUnsupportedSchema`, missing -> invalid document. A new schema needs a new codec branch + `internal/document/testdata/*_v2.json`, never a silent reinterpretation.
- Every file starts with the `Copyright 2026 Appvia Ltd.` / `SPDX-License-Identifier: Apache-2.0` header (goheader lint). `wsl_v5` is on: blank lines around blocks/returns.
- Errors wrap with `%w` and keep sentinel identity; `errors.Join` for multi-cause.

## ANTI-PATTERNS (THIS PROJECT)
- NEVER weaken `open.go` guards: user-info, fragments, control chars, `//`/`.`/`..` segments, unknown query keys, repeated params, azblob query (SAS) and ports, non-http(s) S3 endpoints, IP-literal buckets are all rejected on purpose. Tests in `open_test.go`/`open_message_test.go` pin them.
- NEVER echo the raw URL, connection string, keys or tokens in error text (`argumentError`, `invalidURL` give reasons only). Credentials come from env, never the URL.
- NEVER add locks/leases as a correctness mechanism; storage CAS is the only one.
- NEVER let `DeletePool`/`Release` remove a document carrying another incarnation (`deleteOwnDocument`).
- Do not release a `pool:<child>:<incarnation>` carve-out while a `CreatePool` for that child may still run.

## COMMANDS
```bash
make build              # go build -ldflags "-X main.version=$(VERSION)" -o ipam ./cmd/ipam
make test               # go test ./... -count=1 -race -timeout 20m
make test-integration   # go test -tags integration ./... -count=1 (needs emulators/env)
make lint               # golangci-lint run + scripts/license-headers.sh --check
make license-headers    # add missing Copyright/SPDX headers to tracked files (.go .sh .tf .yml .yaml .toml Makefile .gitignore)
make test-scripts       # scenario tests for scripts/license-headers.sh
go test -run Example .
GATE_SEEDS=200 go test -run 'TestLifecycleStressMemory' -race .   # memory stress seeds (default 20; GATE_OFFSET shifts)
GATE_LOCAL_SEEDS=10 go test -run TestLifecycleStressLocal -race . # local seeds (default 3; GATE_LOCAL_START)
GATE_CREATE_ITERATIONS=1000 go test -run TestCreatePoolRaceLocalLoop .
```

## NOTES
- CI (`.github/workflows/test.yml`): `go test ./...` on ubuntu, macos and windows (`-race` on ubuntu only via `make test`); a separate ubuntu `lint` job runs golangci-lint v2.14.0 and the license header check; emulators + probe + `make test-integration` on ubuntu. Actions are SHA-pinned (`pinact run -u`).
- Headers: `scripts/license-headers.sh` adds/checks the Apache-2.0 header on non-Go files too (CI step `License headers`); golangci `goheader` enforces Go files. `testdata/`, `docs/` and `templates/` are skipped.
- Cross-process tests re-exec the test binary via `IPAM_CROSS_PROCESS_WORKER`/`IPAM_CROSS_PROCESS_DIRECTORY` (root) and `IPAM_LOCAL_CROSS_PROCESS_DIR`/`IPAM_LOCAL_FRESH_DIR` (storage/local); do not set them by hand.
- `.omo/` and `.codegraph/` are gitignored tooling state; the probe writes `.omo/evidence/emulators.json`.
- `client_attempt*_test.go`, `client_gate_regression_test.go` are regression suites for past races; keep them when refactoring.
