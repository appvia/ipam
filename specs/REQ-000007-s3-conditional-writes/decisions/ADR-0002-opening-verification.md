# ADR-0002: Per-Store proof with fail-closed mutations and read-capable failure

**Status**: Proposed for G3
**Traces**: AC-1 through AC-8, AC-13 through AC-15, AC-18; NFR-1, NFR-2

## Context

`s3.New` currently does no network I/O; root `OpenStorage` delegates to it and
`Open` wraps its result. A check only in the CLI or root opener can be bypassed
by typed constructors. Failing the constructor on probe denial breaks the
approved read-only journey. A cache keyed by endpoint cannot account for new
openings or changed endpoint behavior/permissions.

## Decision

Run the five-step synthetic positive/negative control sequence synchronously
inside default `s3.New`, for reference and custom stores alike. Store one
immutable result and gate all public S3 mutations before transport. Probe
failure returns a read-capable Store with an explicit typed report, not a
configuration error. Add an optional report interface following
`storage.Inspector` and a forwarding Client accessor.

Provide explicit read-only S3 construction, reachable by a root read-only
opener, that performs no probe or cleanup writes and can never become writable.
No bypass enables unverified writes. Keep configuration/URL validation intact.
Probe operations are private and owner-key-restricted; ordinary operation retry
and error mapping remain unchanged. Single-attempt proof requests avoid
ambiguous retry evidence; one elapsed second bounds the added sequence and
cleanup. The report distinguishes failed enforcement from unavailable evidence.

Alternatives: constructor failure prevents authorised reads; lazy checking on
first mutation contradicts opening-time verification and complicates concurrent
first use; per-write checks violate NFR-2; endpoint caches violate AC-8/AC-18.

## Consequences

Library consumers retain existing opening signatures but must inspect reports
for diagnostics; mutation errors cannot be ignored as success. CLI consumes
reports automatically. Existing policies without reserved-prefix write access
will intentionally lose writes while keeping reads. A successful check is
point-in-time evidence, not conditional-DELETE certification or continuous
protection against a later endpoint change.
