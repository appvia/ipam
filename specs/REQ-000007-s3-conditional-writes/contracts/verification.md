# Contract: S3 opening proof and write eligibility

Status: proposed; DQ-2 in `../plan.md` must be settled at G3.
Consumers: typed S3 callers, `OpenStorage`, `Open`, `ipam.New` and doctor.
Sources: `storage/s3/s3.go`, `open.go`, `storage/storage.go`, `client.go`.

## Construction and read-only access

- `s3.New(ctx, Config)` defaults to verification on Amazon S3 and all configured
  custom endpoints, including SDK-selected endpoints. Do not filter by host.
- Add `Config.ReadOnly bool`; true suppresses **all** verification writes and
  pending cleanup, and permanently blocks public S3 mutations on that Store.
- Add `ipam.OpenStorageReadOnly(ctx, rawURL)`, sharing validation with
  `OpenStorage`; use read-only S3 construction. No URL/env write bypass exists.
- Existing invalid-configuration failures still return an error. Probe failures
  return a usable Store plus explicit report state, not constructor failure.
  Reads and inspection still enforce the service's existing permissions.
- The optional `storage.VerificationReporter` report and Client forwarding
  accessor are read-only and never rerun the probe. Calling code must inspect
  cleanup warnings; CLI does so automatically.

Traces: AC-1, AC-5 through AC-8, AC-14, AC-15, AC-18; FR-7.

## Default sequence

For fresh private key K, initial bytes A and distinct replacement bytes B:

| Step | Request | Evidence / result |
| --- | --- | --- |
| 1 | PUT K, A, `If-None-Match: *` | Success and nonempty ETag V1 are required; otherwise unavailable. A definitive collision does not confer ownership. |
| 2 | PUT K, distinct negative-control bytes, `If-None-Match: *` | Exactly HTTP 412 PreconditionFailed is expected. Success demonstrates failed protection. Other responses are inconclusive. |
| 3 | PUT K, distinct negative-control bytes, `If-Match: W` | W is a valid quoted, nonempty synthetic tag different from V1. Exactly HTTP 412 is expected. Success demonstrates failed protection. |
| 4 | PUT K, B, `If-Match: V1` | Success and nonempty ETag V2 establish that valid replacement is permitted; rejection is unavailable, not verified. |
| 5 | DELETE K, `If-Match: V2` | Cleanup, not an additional capability proof. Record its independent success/failure. |

The happy path has five small requests, within the author's estimate. Extra
requests needed for uncertain own-key cleanup or prior terminal records share
the same elapsed budget; the spec does not impose a request-count ceiling.
Use private helpers that cannot address any other key. Do not invoke gated
public mutations recursively. Do not change global `mapError`: HTTP 409 maps to
`ErrPreconditionFailed` for normal operations but is **not** negative-control
proof here. Inspect the exact HTTP/API result, and require a definitive first
attempt; any unknown outcome, malformed response or missing ETag is unavailable.
All probe requests use a single attempt. Negative rejection without both
positive controls can never produce `verified`.

On early termination, cleanup may read **only K**, recover its latest ETag and
delete K under that ETag. Never send an empty version, sweep a prefix or delete
a key discovered from an untrusted listing. Own-key read failure, deletion
denial or ambiguity records incomplete cleanup. A cleanup 404 proves no live
object remains; a definitive successful DELETE completes live cleanup, not
historical-version cleanup. Conditional deletion enforcement remains a separate
supported-store requirement, not something this proof certifies.

Traces: AC-1 through AC-4, AC-9, AC-13, AC-14, AC-17; NFR-1, NFR-3.

## Public mutations

Before any user-data request, `CreateIfAbsent`, `PutIfMatch` and `DeleteIfMatch`
must require verified evidence on **this Store**. This applies to deletion as
well as PUT. Guard before request construction or per-operation validation, so
an unverified Store cannot leak a different condition error as write eligibility.
After verification, preserve existing argument checks, header semantics,
unknown-outcome classification and retries. Public mutations of the reserved
namespace are always rejected with an explicit reserved-key error; no caller
can use that namespace as an unverified write bypass.

| Opening state | Mutation error / safe message |
| --- | --- |
| verified | Existing mutation semantics; not a substitute for pool permissions. |
| failed | `ErrVerificationRequired` and `ErrConditionalWriteProtection`; "conditional-write protection failed: create condition ignored" or "match condition ignored". |
| unavailable | `ErrVerificationRequired`, with typed underlying cause; "verification required: denied", "deadline", "cancelled", "transport" or "invalid response". |
| skipped_read_only | `ErrVerificationRequired`; "verification required: opening is read-only". |

Do not wrap these as `ErrInvalidArgument`, `ErrConflict` or a successful no-op.
Client error wrapping must preserve identity through `errors.Is`. Cleanup failure
after a complete proof leaves `verified` plus `cleanup=pending`; it must remain
visible in reports and CLI warnings. It does not falsely classify enforcement
as failed or claim cleanup completed. Traces: AC-2, AC-3, AC-5, AC-6, AC-17.

## Ownership and subsequent openings

Every opening generates a different K and performs its own proof. Sharing a
Client's Store shares that Store's evidence; constructing another Store does
not. Only process-local, positively owned **terminal** records are eligible for
later automatic cleanup. Active or unrecognised keys are never eligible, even
if old. The claim is atomic locally; no distributed locks, leases, ownership
by timestamps or endpoint-wide cache are introduced. A pending cleanup record
cannot grant verification to another Store. See ADR-0003 for the unresolved
cross-process interpretation. Traces: AC-7, AC-8, AC-10, AC-16, AC-18.
