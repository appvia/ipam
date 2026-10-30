# storage/

## OVERVIEW
Backend contract (`Storage`, sentinels, `Inspector`) plus four implementations and the reusable contract suite. Earned its file: ~40 files, 5 subpackages, every client write goes through it (score ~16).

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Interface | `storage.go` | `Version` is opaque: round-trip verbatim, never parse/compare |
| Sentinels | `errors.go` | `ErrNotFound`, `ErrPreconditionFailed`, `ErrAccessDenied`, `ErrUnknownOutcome` |
| Data-protection report | `inspect.go` | `Info{Versioning,SoftDelete,HierarchicalNamespace}` as `TriState`; read-only |
| Access-denied retry opt-in | `context.go` | `WithAccessPropagationRetry(ctx)` (fresh IAM/RBAC grants) |
| Contract suite | `storagetest/contract.go` | `Run(t, newStore)` / `RunWithOptions(..., Options{SkipConditionalDelete})` |
| In-memory | `memory/` | tests + `mem://`; counter versions |
| Local dir | `local/` | sha256-hex versions, `<dir>/.lock` flock, temp+fsync+rename, `os.Root` |
| S3 | `s3/` | ETag versions, `If-None-Match: *` / `If-Match`; SDK default credential chain |
| Azure Blob | `azblob/` | ETag conditions, no leases; `auth.go` = aztfauth with `ARM_*` env |

## CONTRACT (every backend MUST)
- `CreateIfAbsent` on an existing key -> `ErrPreconditionFailed`; `PutIfMatch`/`DeleteIfMatch` with a stale version -> `ErrPreconditionFailed`; missing key -> `ErrNotFound`.
- `List(prefix)` returns sorted keys with the backend prefix stripped.
- Return `ErrUnknownOutcome` whenever any attempt of a conditional write MAY have been applied (timeout, reset after send, 5xx), even if the final answer was 412/409: it may answer our own earlier write (`writeOutcome.mapError` in s3, `Store.write` in azblob). Wrap: `fmt.Errorf("%w: %w", storage.ErrUnknownOutcome, err)`.
- Never return `ErrUnknownOutcome` for reads or for errors raised before the request was sent.
- Implement `Inspector` (all four do); a property the caller may not read is `Unknown`, not an error.

## NEW BACKEND CHECKLIST
1. Implement `Storage` + `Inspector`.
2. `_test.go`: `storagetest.Run(t, func(t *testing.T) storage.Storage { ... })`; subtests are parallel and reuse fixed keys, so return an ISOLATED store (own prefix/container/dir) per call.
3. Outcome tests: inject failures after send and assert `ErrUnknownOutcome`, and assert definitive 412 stays `ErrPreconditionFailed` without it (see `s3/outcome_test.go`).
4. Real-service tests go in `integration_test.go` with `//go:build integration`, skipping when env is unset.
5. Wire the scheme in root `open.go` with strict parsing.

## INTEGRATION ENV
- azblob: `AZURE_STORAGE_CONNECTION_STRING` (from `scripts/emulators/start-azurite.sh`).
- s3: `IPAM_TEST_S3_ENDPOINT`, `IPAM_TEST_S3_BUCKET` (default `ipam-test` with endpoint), `IPAM_TEST_S3_REGION`, `IPAM_TEST_S3_PATH_STYLE`, `IPAM_TEST_S3_SKIP_CONDITIONAL_DELETE=1` (emulators ignore If-Match on DELETE; only real AWS proves it).
- `s3/s3_test.go` leaves caller env alone when `IPAM_TEST_S3_*` is set.

## ANTI-PATTERNS
- NEVER accept shared keys/SAS as config: azblob shared-key auth comes only from `AZURE_STORAGE_CONNECTION_STRING` AND only for a loopback endpoint (localhost, 127.0.0.0/8, ::1).
- NEVER support Azure hierarchical-namespace (ADLS Gen2) accounts; renames escape path-based role conditions.
- NEVER point `local` at NFS/SMB/FUSE/cloud-synced dirs; never delete `.lock` while in use. Lock impl is per-OS (`lock_unix.go`, `lock_windows.go`; CI tests windows).
- NEVER retry 412/409/404 as access propagation; only `AccessDenied` is retryable, and only under `WithAccessPropagationRetry` (s3: 10 attempts, 30s max backoff).
- NEVER put credentials, keys or full connection strings in error text.
- `SkipConditionalDelete` is for known-broken emulators only; real backends run every case.
- Doctor/probe behaviour lives in `cmd/ipam/doctor.go` + `Inspect`; do not encode probe-specific versions here.
