# ADR-0004: Separate sanitized refusal from cleanup warnings

**Status**: Proposed for G3
**Trace**: AC-7, AC-8, AC-9, AC-12, AC-13; Constitution I, VI.

## Context

The spec requires cleanup failure to warn without overriding success or the
original verification failure. Existing `Open` APIs return `(value, error)`
and have no warning return. `storage/context.go` is a precedent for per-call
context behaviour; `cmd/ipam/main.go` injects stderr and openers. Existing S3
errors may preserve raw SDK text, which is not safe to reflect from the new
verification path. `pool.go` already uses bounded cancellation-detached
compensation. (AC-8, AC-9, AC-12, AC-13.)

## Decision

Return typed opening errors with fixed unsupported/inconclusive kinds and
allowlisted causes; do not retain raw error strings or SDK errors in the new
opening error chain. Preserve context and storage sentinel identities where
they are safe and meaningful. Do not globally change existing operation error
contracts. (AC-7, AC-8, AC-9.)

Add a context warning handler with a safe fixed warning shape and default
standard-library `slog` delivery when no handler is provided. CLI installs a
handler writing to its injected stderr, including the doctor opener. Cleanup
issues emit at most one warning per opening attempt, separate from the primary
return value. (AC-9, AC-12, AC-13.)

Cleanup has a five-second cancellation-detached deadline, reads ownership and
the latest opaque version, and conditionally deletes only its exact candidate.
Unknown write outcomes trigger an ownership read for cleanup, never acceptance.
No unconditional deletion, scanner or process-crash guarantee is added.
(AC-4, AC-7, AC-8, AC-12, AC-13.)

## Consequences

Legacy callers can receive warnings without a signature break; integrations may
adapt them to consumer-native diagnostics without global mutable logging
configuration. Fixed messages sacrifice detailed SDK diagnostics to guarantee
credential confidentiality. A caller that is cancelled can wait up to five
additional seconds for cleanup. (AC-8, AC-9, AC-12.)

Returning a cleanup error after verification would incorrectly refuse a valid
open; returning a non-nil handle alongside an error invites ignored failures;
dropping warnings is not permitted. These alternatives are rejected. Residual
synthetic data and noncurrent versions may persist and are explicitly warned
or documented, not silently treated as physical erasure. (AC-12, AC-13.)
