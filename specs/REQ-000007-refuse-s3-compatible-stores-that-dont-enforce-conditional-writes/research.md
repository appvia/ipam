# Research: S3 opening verification

**Status**: Technical investigation complete; owner-dependent AC-4 assumption
unresolved. This is the Spec Kit Phase 0 record, not implementation approval.

The redraft uses the available Spec Kit v1.2.5 skill, its vendored plan reference
and the Appvia plan template. Research is limited to the approved `spec.md`,
`clarifications.md`, `.specify/memory/constitution.md` and the checked-out product
source. The registry lists only `appvia/ipam` as this product's code repository.
No live store, credentials or external processor is used for this investigation.

## R1: Stack and dependency choice

**Decision:** Propose the existing exact-pinned Go 1.27.3 toolchain and AWS SDK
for Go v2; record the explicit choice in
[ADR-0001](decisions/ADR-0001-stack.md). Add no runtime dependency.

**Rationale:** `go.mod`, `mise.toml`, `storage/s3/s3.go` and `open.go` show the
library and CLI already share this SDK-backed integration. `Makefile` and
`.github/workflows/test.yml` provide build, test, race and lint commands. No Go
stack skill is listed for the product, so the plan names these commands.

**Alternatives considered:** A subprocess probe, new service or different
language would introduce deployment and credential surfaces without improving
the required in-process opening guarantee. (AC-1, AC-6, AC-10, AC-14;
Constitution I, II, III.)

## R2: Construction versus opening

**Decision:** Preserve offline `s3.New`; propose explicit opening that returns a
distinct verified handle. Unopened public operations fail without requests.
Use the same path for reference and custom endpoints.

**Rationale:** `storage/s3/s3.go` promises offline construction. `client.go`'s
`New(storage.Storage)` has no context/error return, while `open.go` centralizes
URL opening for CLI and automation. A readiness check at the S3 boundary covers
direct constructors without introducing lazy verification. The author's
recorded boundary clarification is in `clarifications.md`.

**Alternatives considered:** Networked construction conflicts with AC-14;
first-operation verification moves the boundary; URL-only checks leave direct
construction unchecked; shared success caching violates fresh checks per open.
The direct-construction migration is explicit, not a compatibility claim.
(AC-5, AC-6, AC-10, AC-11, AC-14;
[ADR-0002](decisions/ADR-0002-opening-boundary.md).)

## R3: Conditional PUT evidence

**Decision:** Use positive create/replace controls and rejected duplicate/stale
writes against one synthetic per-open key, with bounded readbacks. Negative
evidence requires definitive HTTP 412 and no uncertain-write history.

**Rationale:** `storage/s3/s3.go`'s `writeOutcome` tracks possibly applied SDK
attempts, while `mapError` groups multiple statuses under a storage sentinel.
The verifier must not equate that sentinel with proof of enforcement.
`storage/storage.go` requires opaque version round-tripping; no ETag parsing or
invented stale version is needed. The wire contract remains
[conditional-write-check.md](contracts/conditional-write-check.md).

**Alternatives considered:** Negative-only checks can accept reject-all stores;
endpoint allowlists cannot prove behavior; a later 412 after a possibly applied
request can answer the client's own write. None establishes the required
evidence. (AC-1, AC-2, AC-3, AC-7, AC-11;
[ADR-0003](decisions/ADR-0003-isolated-protocol.md).)

## R4: Existing-object integrity remains unresolved

**Decision:** Keep the isolated random namespace proposal conditional on owner
resolution of AC-4/NFR-2. Do not reinterpret the approved spec or claim that
randomness proves absolute integrity.

**Rationale:** No trusted conditional primitive exists before the check. A
pre-write absence GET and a later PUT are not atomic on a non-enforcing store.
An ownership GET followed by conditional cleanup has the same limitation if
the store also ignores delete conditions. `doc.go` confirms that application
correctness relies on storage CAS, not an independent lock or transaction.

**Alternatives considered:** Existing-document probes, a shared fixed key and
unconditional cleanup expose existing objects. A reserved namespace plus
256-bit names reduces ordinary collision risk but does not close an arbitrary
writer race. A trusted isolation primitive or an approved requirement
clarification would need an owner decision, not an agent assumption.

**Required disposition:** The owner must reconcile the spec's zero-change
guarantee with a precisely bounded storage/writer trust model or approve an
alternative. Until then, the clarification gate is not passed, and the proposed
architecture must not be sliced as implementation-ready. No answer is inferred
from skill availability or approval of temporary objects. (AC-4, AC-5; NFR-2;
Constitution I; threat T3.)

## R5: Cleanup and diagnostics

**Decision:** Keep sanitized unsupported/inconclusive opening errors separate
from cleanup warnings. Attempt ownership-aware cleanup with a five-second
cancellation-detached bound; never make cleanup failure alone refuse a verified
open. See [ADR-0004](decisions/ADR-0004-diagnostics-and-cleanup.md).

**Rationale:** `pool.go` already uses bounded detached compensation;
`storage/context.go` provides a per-call context precedent. `cmd/ipam/main.go`
injects stderr and openers and classifies runtime errors separately from usage.
The spec's AC-12/AC-13 and recorded author clarification require independent
warning delivery. `storage/s3/s3.go` retains raw SDK causes today, so new opening
diagnostics need a safe classification boundary rather than raw error wrapping.

**Alternatives considered:** A cleanup error that replaces success violates
AC-12; suppressing a warning violates AC-12/AC-13; exposing raw SDK responses
risks AC-9. A scanner, crash recovery or broader deletion is outside scope.
(AC-7, AC-8, AC-9, AC-12, AC-13; Constitution I, VI.)

## R6: Permissions, scope and residency

**Decision:** Require G3 security review for scratch access and explicit migration
documentation, including previously read-only callers. Reuse the configured
store and existing authentication chain; do not select a region or add a new
processor. Deployment residency remains an owner confirmation/exception item.

**Rationale:** All S3 URL openers share `open.go`, including the opener used by
`cmd/ipam/doctor.go`. Verification necessarily performs temporary writes even
when the requested command only reads. `spec.md` explicitly excludes changing
doctor's separate probe and region enforcement. Constitution I requires high
risk for authorisation changes; Constitution V is not waived by a scope answer.

**Alternatives considered:** Bucket-wide permission grants, exempting readers,
bypass controls and region enforcement are not justified by this Requirement.
No provider patch is claimed for a repository absent from this product's
registry entry. (AC-7, AC-10, AC-11, AC-12, AC-14; scope constraints.)

## R7: Deterministic acceptance evidence

**Decision:** Reuse stateful fake HTTP server and no-delay retry patterns in
`storage/s3/s3_test.go` and `storage/s3/outcome_test.go`, plus the existing CLI
harness. Map every AC to named tests in `plan.md`; provide a runnable future
validation guide in [quickstart.md](quickstart.md).

**Rationale:** Independent ignored-header cases, deterministic concurrent
barriers, object bytes/version snapshots and synthetic diagnostic markers test
the required outcomes without live service dependence. A missing proposed test
must not be treated as passing merely because `go test -run` exits successfully.

**Alternatives considered:** Live AWS/vendor certification, fixed sleeps and
request-count-only assertions do not satisfy the deterministic semantic evidence
required here. Tests cannot resolve the AC-4 trust assumption. (AC-1 through
AC-14; NFR-1 through NFR-4; Constitution II, VI.)
