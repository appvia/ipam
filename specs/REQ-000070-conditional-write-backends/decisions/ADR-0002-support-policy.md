# ADR-0002: Centralize pure host classification and explicit opening consent

**Status**: Proposed for G3
**Trace**: AC-1..AC-12, AC-15..AC-18, AC-21, AC-23..AC-28;
FR-1 (answered Q2), FR-7/FR-8; constitution I/II/V.

## Context

`open.go` already validates URLs into `openTarget`, then constructs storage;
`Open` currently delegates to fixed-arity `OpenStorage` and applies
`client.go`'s function-valued client options. `storage/s3/s3.go` sends the
required conditional headers, but its server may ignore them. `doc.go` relies
on these conditions for concurrent allocation correctness.

The approved spec requires default refusal, library visibility without consent,
explicit opt-ins for both URL openers, identical support classification across
CLI/library paths, no network calls at open time and unchanged validation.
`storage/context.go` already implements explicit opt-ins with private typed
context keys. Replacing the existing public `Option` type would break custom
options and slice expansion; a unified new option interface is not justified.

## Decision

Expose `BackendClassification`, `ClassifyBackend(rawURL)` and the sentinel
`ErrUnverifiedBackend`. Route classification through the existing validator
and one private target classifier. For S3 override endpoints, match lowercase
`Hostname()` with the two approved dot-bounded AWS suffixes, not substrings or
provider labels. Never learn, cache or mutate the verified set after use.

Keep both opener signatures and the existing client option type/order. Add
`WithAllowUnverifiedBackend(ctx context.Context) context.Context` as an explicit
opening option for either entry point, reusing the typed-context pattern.
`OpenStorage` reads the private marker after validation and classification and
refuses before any constructor without consent. `Open` still delegates to it
and applies client options once after successful construction. Preserve the
parent context and narrowly scope the derived context to the intended opening.
Do not add global consent, new client fields or changes to operation contexts.

CLI ordinary paths use the same policy and carry effective consent into their
opener seam. Record warning state per invocation, never per open. Library
diagnostics are static and library opening is silent. All support references
use `https://github.com/appvia/ipam#supported-backends`.

Direct backend construction and `New(store, ...)` remain caller-managed under
the approved scope. The [Requirement author's Q2 answer](https://github.com/appvia/ipam/issues/70#issuecomment-6742245024)
assigns Unverified to the existing test-only container-form Azure path.
Distinguish it by the empty `azure.AccountURL` produced by `parseAzblob`, not
by inspecting connection-string configuration. Apply ordinary refusal and
explicit consent plus the same doctor matrix. Emulator tests explicitly opt
in; construction still enforces existing loopback and connection-string guards.
Verified account-form Azure behavior and the verified support set are unchanged.

## Alternatives considered

- Runtime probes would violate AC-17/NFR-1 and revive the rejected scratch-object
  and cleanup approach. Do not use them.
- A per-backend duplicated allowlist risks library/CLI/doctor policy drift
  (AC-18, FR-8).
- Checking raw URL substrings could accept suffix lookalikes or provider words
  in paths (AC-25).
- Replacing client `Option` with an open-option interface breaks existing
  configuration callbacks and `[]Option` consumers unnecessarily (FR-7).
- Adding client/storage option types would either require separate APIs or
  change existing callback timing to discover consent before opening. The
  repository's existing context marker avoids both (AC-7, AC-27).
- Applying client options twice to extract consent could repeat user callbacks
  and must not be used (FR-7 compatibility).
- Constructing first and refusing later can let configuration failures mask the
  required refusal category and perform local setup before permission to open
  (AC-5/NFR-2).

## Consequences

Both URL entry points fail closed with a matchable category and no live checks.
Consent restores access, never verification. One explicit context-scoped option
works with both openers without signature changes, public-type conversion or
custom callback ordering changes. Existing direct calls and fixed-arity
function assignments still compile. Context reuse can unintentionally spread
consent to another opening, so examples derive it inline and tests assert the
parent remains unmarked. Existing configuration and verified-backend operations
stay covered by regression tests.

The emulator form has a defined classification without becoming a supported
production endpoint. Successful-opening fixtures need explicit consent;
constructor-rejection tests also opt in so they still exercise missing or
forbidden configuration rather than stopping at the policy gate. Classification
remains network/configuration-free, and consent never bypasses authentication.

The broad AWS host policy, HTTP grammar and SDK default-resolution assumptions
remain limited by the approved spec. They are not endpoint authentication or
CAS proofs, and residual risks must be stated rather than silently tightening
the support contract.
