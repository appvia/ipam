# ADR-0002: Publish explicitly verified S3 handles

**Status**: Proposed for G3; direct-construction migration needs approval
**Trace**: AC-1, AC-5, AC-6, AC-7, AC-10, AC-11, AC-14.

## Context

`storage/s3/s3.go` documents `New` as offline and currently returns a directly
usable `Store`. `open.go` uses it for `OpenStorage`; `client.go` exposes a
context-free `New(storage.Storage)` that cannot return an opening error. The
spec preserves construction without network calls while requiring verification
before application operations, on every new backend open. Verification only
in URL-based opening would leave direct `s3.New` plus `ipam.New` as a bypass.
(AC-10, AC-14.)

## Decision

Keep `s3.New`'s signature/offline role but return an unopened handle. Add
`s3.Open(ctx, cfg)` and `(*Store).Open(ctx)`, returning a distinct verified
handle on success and nil on failure. Public storage and inspection operations
on unopened handles fail `s3.ErrNotOpen` without network calls; verifier-only
private raw methods are not exported. Each explicit open verifies a new
candidate, never shares readiness or memoizes acceptance. Normal verified
operations contain no opening check. Root URL openers use `s3.Open` once.
(AC-5, AC-6, AC-7, AC-10, AC-11, AC-14.)

## Consequences

The backend-neutral interface and root opener signatures stay stable, and
client construction remains offline. Direct S3 constructor consumers must
migrate to an explicit open before reads/writes; this behaviour change must be
called out and tested, not hidden behind lazy first-operation verification.
Existing backend wire tests must retain their assertions while adding valid
opening setup or testing private primitives from same-package tests.
(AC-1, AC-10, AC-14.)

Read-only callers and doctor now need scratch read/write privileges at open.
Doctor's independent probe is not changed, but its no-write help must distinguish
opening activity. Security review must approve this authorisation impact.
(AC-10, AC-11; Constitution I.)

Networked construction violates AC-14; lazy verification violates the opening
boundary; URL-only checks leave a direct-construction bypass; global or
same-store result caching violates fresh checks per open. These alternatives
are rejected. This design deliberately exposes the migration rather than
changing the approved spec to fit existing APIs. (AC-6, AC-10, AC-14.)
