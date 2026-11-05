# Contract: explicit S3 opening and diagnostics

This is the proposed library/CLI contract, not implemented API documentation.
It extends `storage/s3/s3.go` and `open.go` without changing the backend-neutral
`storage.Storage` interface. (AC-1, AC-10, AC-14.)

## Public entry points

```go
// Existing signature, construction only; zero network requests.
func New(ctx context.Context, cfg Config) (*Store, error)

// New: construct and verify before returning a usable store.
func Open(ctx context.Context, cfg Config) (*Store, error)

// New: a fresh verified handle; source readiness is unchanged.
func (s *Store) Open(ctx context.Context) (*Store, error)
```

Every explicit `Open` performs its own verification, including when invoked on
an already-verified handle. New client operations on a returned verified handle
cause zero additional checks. Failure returns a nil handle and an error. No
partial candidate escapes; no cached acceptance across instances, endpoints,
identities or prefixes. (AC-5, AC-6, AC-7, AC-14.)

`Get`, `List`, `CreateIfAbsent`, `PutIfMatch`, `DeleteIfMatch` and `Inspect` on
an unopened `Store` return `s3.ErrNotOpen` without network requests. Direct
`ipam.New(configuredStore)` remains offline but cannot issue S3 operations until
the caller supplies the verified handle instead. This is intentionally a
behaviour-breaking direct-construction migration, not an optional safety mode.
(AC-10, AC-14.)

Root `ipam.OpenStorage(ctx, rawURL)` selects `s3.Open` for S3, retaining its
signature, URL guards and non-S3 behaviour. `ipam.Open` delegates to it once,
then `ipam.New` does not verify again. `s3.New` constructor/configuration failures
keep the existing argument-error classification; runtime opening verification
failures do not become `ipam.ErrInvalidArgument`. Both reference and overridden
endpoints, including SDK-resolved endpoints, verify identically. (AC-1, AC-7,
AC-10, AC-11, AC-14.)

## Failure classification

Expose a typed `s3.OpenError` with safe enum fields `Kind`, `Condition`, `Phase`.
`Kind` is `unsupported` or `inconclusive`; `Condition` is `create`, `replace`
or empty; `Phase` comes from the fixed protocol phases. `errors.Is` supports
new `s3.ErrConditionalWritesUnsupported` and `s3.ErrVerificationIncomplete`.
`ErrNotOpen` is separate and is not evidence of endpoint nonconformance.
(AC-2, AC-3, AC-7, AC-14.)

| Observation | Result | Safe text example |
| --- | --- | --- |
| Conditional create knowingly succeeds on owned existing key | Unsupported, create | `s3: opening refused: conditional creation is not enforced` |
| Stale conditional replacement knowingly succeeds | Unsupported, replace | `s3: opening refused: version-conditional replacement is not enforced` |
| Denied, transport failure, 409, unreadable/mismatched readback, missing ETag, uncertain outcome, exhausted candidates or entropy failure | Inconclusive | `s3: opening verification could not complete during replace-b (access denied)` |
| Cancelled/deadline before verification completes or publication | Inconclusive with context cause | `s3: opening verification could not complete (context canceled)` |

Only definitively successful forbidden writes justify unsupported. A successful
HTTP response with unusable/missing ETag is inconclusive, though it still refuses
opening. Do not interpret 409 or arbitrary 4xx responses as proven condition
enforcement. (AC-2, AC-3, AC-7.)

Safe classified causes retain `errors.Is` for `context.Canceled`,
`context.DeadlineExceeded`, `storage.ErrAccessDenied`,
`storage.ErrUnknownOutcome` when applicable. Do not return or unwrap raw
SDK/transport/configuration error messages from the new verification path;
extract only these allowlisted identities and fixed reasons. Do not wrap an
expected negative-probe `ErrPreconditionFailed` as the application operation's
conflict. Raw established storage-operation error contracts remain unchanged.
(AC-7, AC-8, AC-9.)

## Warnings

Introduce a small backend-neutral context facility alongside the precedent in
`storage/context.go`:

```go
type Warning struct {
    Code    string
    Message string
}
func WithWarningHandler(ctx context.Context, handler func(Warning)) context.Context
```

The only new code is `s3_open_cleanup_incomplete`; its message is fixed:
`s3: temporary opening-check data may remain`. No key, bucket, endpoint, payload,
SDK text or credential appears. Invoke a supplied handler synchronously once
per attempt when cleanup cannot establish removal; otherwise call the standard
library `slog` warning logger. A nil/absent handler uses the default rather than
silently discarding the warning. No process-global mutable handler is added.
(AC-9, AC-12, AC-13.)

Warnings are independent of `(store, error)`: failed cleanup after verified
checks returns a usable store and nil error; failed cleanup after refused
verification returns nil and the original safe verification error. An absent
object during cleanup is already removed and needs no warning. Foreign ownership,
denied reads/deletes or uncertain cleanup outcomes produce a warning. A
conclusive absence read after an uncertain delete counts as removal. (AC-12,
AC-13.)

## CLI and infrastructure automation

`runCLI` installs a per-run handler on the execution context and prints
`ipam: warning: s3: temporary opening-check data may remain` to its injected
stderr writer. This applies to normal openers and the doctor storage opener;
do not alter the injected opener signatures. JSON/table stdout stays unchanged.
Cleanup-only warnings preserve exit 0 for an otherwise-successful command.
Opening verification failure is runtime exit 1; valid URL validation failures
retain exit 2. Doctor fails at opening with exit 1 rather than inventing a
doctor-report check or changing its own write probe. (AC-7, AC-9, AC-10, AC-12,
AC-13; diagnostic-probe exclusion.)

Infrastructure-automation callers use `ipam.Open`/`OpenStorage` or explicit
`s3.Open`; a context handler can turn warnings into consumer diagnostics. Default
logging means legacy consumers are still notified. No provider change or release
is claimed here. The new library contract and its nil-handle refusal are tested
in this repository. (AC-9, AC-10, AC-12, AC-13.)
