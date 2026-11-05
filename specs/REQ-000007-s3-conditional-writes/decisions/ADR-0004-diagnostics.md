# ADR-0004: Separate no-write diagnosis, endpoint proof and pool permissions

**Status**: Proposed; DQ-1 requires spec-owner resolution before implementation
**Traces**: AC-5, AC-6, AC-11, AC-12, AC-15, AC-17; NFR-4; FR-7

## Context

`cmd/ipam/doctor.go` currently sends a wrong-version PUT of existing reservation
bytes for every pool. `--expect-no-write` asserts a pool's grants; it does not
prevent requests. The approved spec assumes an explicit no-write mode and
requires zero mutations in it. Starting opening probes before parsing doctor
mode would violate that requirement. Permission on a synthetic prefix also
does not establish pool-specific write access.

## Decision

Propose `doctor --no-write`, wired before opening through the read-only opener,
to suppress all verification, cleanup and per-pool mutation probes. Retain the
existing expectation flags as distinct assertions; mark untested assertions
WARN rather than inventing a denial. Default S3 doctor consumes the opening's
proof and runs legacy per-pool grant probes only if that opening verified.
No second endpoint sequence is allowed. Other backends retain existing default
behavior; explicit no-write suppresses their mutation probes too.

Add global capability, cleanup and residue rows using existing report and exit
aggregation. Inventory live objects and versions/markers read-only; partial or
denied visibility must not look empty. Document scoped minimum permissions,
historical residue and lifecycle guidance. Do not render raw provider errors.

Alternatives: reinterpret `--expect-no-write` as a global mode, breaking mixed
pool expectations; treat synthetic-prefix denial as a pool denial, producing
false grant assurance; keep unconditional legacy probes on unverified S3,
allowing the diagnostic itself to alter user-object versions.

## Consequences

Default S3 doctor now creates/removes synthetic objects via opening, a deliberate
scoped departure from old help and baseline descriptions. True no-write mode
is explicit and testable. Unverified identities may receive inconclusive pool
grant assertions rather than previous PASS results. The spec owner must confirm
this compatibility boundary at G3; this PR does not silently alter the spec.
