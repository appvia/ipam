# Technical Design: Conditional-write protection on open

**Spec**: `specs/REQ-000007-s3-conditional-writes/spec.md`
**Created**: 2026-11-05
**Status**: Draft for G3; the two interpretation questions below block implementation
**Proposed risk**: high

## Summary

Every S3 store constructor performs one bounded, isolated verification sequence
before publishing the store. A positive result enables its three mutation
methods; every other result leaves those methods fail-closed while reads remain
available. Evidence belongs to that opening, not an endpoint cache. Doctor uses
the same evidence, offers a genuinely non-mutating diagnostic mode, and reports
residue independently of verification. No product code or approved spec changes
are part of this design PR. Traces: AC-1 through AC-18; NFR-1 through NFR-4.

## Technical context

**Languages and versions**: Go 1.27.3; golangci-lint 2.14.0 (`mise.toml`).
Retain AWS SDK S3 v1.114.6, SDK core v1.47.5, config v1.33.12, Smithy v1.28.7
and Cobra v1.10.2 (`go.mod`, `go.sum`). No new dependencies.
**Stack decision**: [ADR-0001](decisions/ADR-0001-stack.md).
**Repositories touched by future implementation**: `appvia/ipam` only; the
registry lists it as the sole code repository for product `ipam`.
**Storage**: existing configured S3 bucket and prefix; no region is selected and
no bucket settings, policies or lifecycle rules are installed.
**Testing**: Go tests and race detector with scripted HTTP fakes; existing
emulator integration suite; separately controlled reference-store qualification.
No matching Go stack skill is installed; use `make build`, `make test` and
`make lint`. Use the installed `ac-test-mapping` skill when implementing.
**Constraints**: one sequence per opening, at most 1 second added verification
latency on the reference store, no user-object changes, synthetic data only.

## Evidence from the code

| Path / pattern | Design implication | Trace |
| --- | --- | --- |
| `storage/s3/s3.go`: `New`, `Config`, `objectKey` | Put the check at the common typed constructor, retaining SDK authentication, configured prefix and custom endpoint behavior. | AC-1, AC-4, AC-8; FR-7 |
| `open.go`: `OpenStorage`, `Open`, strict URL parser | URL callers and typed callers must reach the same gate; no new URL bypass or parser relaxation. | AC-6, AC-8; FR-3 |
| `client.go`: `New`, `Close` | A Client wrapping an S3 Store cannot bypass the Store gate; forward diagnostic evidence through Client without rerunning it. | AC-6, AC-7 |
| `storage/storage.go`, `storage/inspect.go` | Keep `Storage` unchanged; use a separate optional reporting interface, following `Inspector`. | AC-5, AC-11; FR-7 |
| `storage/s3/s3.go`: `put`, `DeleteIfMatch`, `writeOutcome`, `mapError` | Gate all public mutations before dispatch; keep raw probe operations private and preserve ordinary write uncertainty handling. A mapped precondition error alone is not probe evidence because it includes HTTP 409. | AC-2, AC-3, AC-6, AC-13, AC-14 |
| `cmd/ipam/doctor.go`: `doctorCommand`, `diagnose`, `probeWrite` | Current doctor has no true no-write flag; `--expect-no-write` is a pool-grant assertion and still sends a PUT. Do not equate it with a non-mutating mode. | AC-15; FR-6 |
| `cmd/ipam/doctor.go`: `checkResult`, `doctorReport`, `finish`; `cmd/ipam/main.go`: `exitCode` | Add diagnostic rows using existing PASS/WARN/FAIL aggregation; retain exit codes 0, 1, 2 and 6. | AC-11, AC-14, AC-17; FR-7 |
| `storage/s3/s3_test.go`, `outcome_test.go`, `concurrent_test.go` | Extend scripted request capture and channel-controlled concurrency, not sleeps or real-service calls in unit tests. | AC-4, AC-7, AC-13, AC-14, AC-16 |
| `doc.go`: ownership-only compensation, bounded cancellation-detached cleanup | Cleanup must use positive ownership, not prefix membership or object age. | AC-9, AC-10, AC-16, AC-17 |
| `storage/s3/integration_test.go`, `storage/storagetest/contract.go` | Preserve existing backend contract and the explicit distinction between emulator PUT evidence and conditional-DELETE support. | AC-1; FR-7 |
| `README.md`: backend constructors, doctor, permission tables, recovery | Update these surfaces together in implementation; current no-network and no-create/delete statements need scoped S3 amendments. | AC-5, AC-12, AC-15; FR-7 |

No existing ADRs or feature contracts were found in this checkout. Baselines
`BASE-0012-s3-backend/spec.md` and `BASE-0015-cli-doctor/spec.md` describe the
current behavior rather than new design authority.

## Constitution check

Basis: `.specify/memory/constitution.md`, version 0.2.0-draft. The spec's stale
"Draft" header is not edited; the target issue carries `gate:spec-approved`.

| Principle | How the design complies | Exception (needs approver sign-off) |
| --- | --- | --- |
| I. Security baseline | Preserve SDK credentials and URL guards; private probe capability; prefix-scoped additional permissions; sanitised reports; exact existing dependency pins. Classify the write-authorisation boundary change as high risk and require security-managers review at G3. | None proposed. |
| II. Test standards | Named tests cover every AC below, with fake transport, injected deadlines and channel barriers. A Bug implementation first commits the failing regression. | Reference-store latency evidence is an operator qualification, not a network-dependent CI test; G3 must accept this evidence split. |
| III. Technology choice | ADR-0001 records stack alternatives; exact toolchain, checksum file and PR test/lint CI already exist. Implementation explicitly runs a build too. | None. |
| IV. Accessibility | Preserve text/JSON alternatives; status is never colour-only. New flags, help and actionable warnings are tested in both formats. No web interface is introduced. | None. |
| V. Data handling | Random synthetic check payloads only; no pool bytes, client identifiers, endpoint values or credentials in check reports or examples. | None. |

Post-design review: no intended constitutional exception, but G3 is **not ready**
until the interpretation questions and latency evidence policy are approved.

## Components

| Component | Repo | Responsibility | Acceptance criteria |
| --- | --- | --- | --- |
| S3 opening verifier | appvia/ipam | One synchronous sequence on default `s3.New`; positive and negative controls; one shared elapsed budget. | AC-1 through AC-3, AC-7, AC-8, AC-13, AC-14, AC-18 |
| Store mutation gate | appvia/ipam | Deny create/replace/delete before raw I/O unless this opening is verified; read-only mode can never become writable. | AC-2 through AC-6, AC-15 |
| Private probe and ownership cleanup | appvia/ipam | Synthetic unique key, exact-key cleanup and recoverable ownership records; no prefix sweep. | AC-4, AC-9, AC-10, AC-16, AC-17 |
| Optional verification report | appvia/ipam | Immutable result on Store and forwarding accessor on Client, including independent cleanup errors. | AC-5, AC-6, AC-11, AC-14, AC-17 |
| Doctor and operator guidance | appvia/ipam | No-write opening, shared evidence, residue inventory, lifecycle advice, permission migration. | AC-5, AC-11, AC-12, AC-15, AC-17 |

### Opening and dispatch

1. Validate configuration and load the existing SDK configuration as today.
   Configuration failures remain constructor errors with existing identity.
2. Select default verification or explicit read-only mode before any storage I/O.
   Add `s3.Config.ReadOnly` and `ipam.OpenStorageReadOnly(ctx, rawURL)`; the latter
   shares the existing parser and selects read-only S3 construction. It does not
   add URL parameters. On non-S3 backends it retains normal construction;
   doctor itself suppresses all mutation probes in no-write mode.
3. Default S3 construction uses a private, fresh owner key and the sequence in
   [the contract](contracts/verification.md). Finish or fail closed before
   returning the Store; do not launch background verification or retry on use.
4. Verification failures are data on a usable read-capable Store, not
   `ErrInvalidArgument` constructor failures. Add the optional
   `storage.VerificationReporter` interface and `VerificationReport` value;
   expose `Client.Verification() (storage.VerificationReport, bool)`. Reporting
   does no I/O and returns a copy. Existing open signatures remain compatible.
5. Guard every public S3 mutation, including calls targeting the reserved
   namespace. Only private, exact-owner probe helpers may bypass the guard.
   A failed public write carries `storage.ErrVerificationRequired`, with an
   additional `storage.ErrConditionalWriteProtection` for demonstrated failure.
   Preserve typed underlying causes without rendering their unsafe text.
6. CLI ordinary commands inspect the report once and print an actionable,
   sanitised warning for unavailable verification or incomplete cleanup.
   Writes return their explicit gated error (exit 1), reads still run normally.
   Doctor renders report rows instead of duplicating stderr warnings.

Traces: AC-1 through AC-8, AC-13 through AC-15, AC-17, AC-18; FR-7; constitution I, V.

### Timing and recovery

Start a single elapsed verification budget of 1 second after unchanged SDK
configuration loading. Spend at most 900 ms on proof and previous owned cleanup,
reserving the remainder for this sequence's cleanup. Each probe request uses
one transport attempt, not SDK retries or access-propagation retry opt-in;
otherwise a later rejection can mask an earlier applied request. Clip all
deadlines to the parent's deadline. After caller cancellation, own-key cleanup
may detach from cancellation, but never exceed the original absolute 1-second
budget. When no time remains, record pending cleanup rather than extend opening.
Normal user operations retain their existing retry behavior.

Process-local pending cleanup records retain only exact keys, scope/client
handles and terminal ownership state. A later default opening may claim a
terminal record for the same scope and attempt cleanup using its recorded
client; an active record is never eligible. No verification result is reused.
No-write openings neither create objects nor drain this queue. Process death
loses the ownership proof: list such residue, do not infer abandonment from age.
See ADR-0003 and question DQ-2 before implementing this bounded interpretation.

Traces: AC-7 through AC-10, AC-14 through AC-17; NFR-1 through NFR-3.

## Contracts

- [contracts/verification.md](contracts/verification.md): proof, opening modes,
  mutation errors, report shape and cleanup ownership.
- [contracts/diagnostics.md](contracts/diagnostics.md): doctor flags, report rows,
  inventory completeness and safe operator guidance.

## Data model

See [data-model.md](data-model.md). Existing pool/reservations document versions,
incarnations, allocation policy and CAS retry semantics are unchanged.

## Decisions

- [ADR-0001-stack](decisions/ADR-0001-stack.md): keep the existing pinned Go stack.
- [ADR-0002-opening-verification](decisions/ADR-0002-opening-verification.md):
  store-local evidence, guarded mutations and explicit read-only construction.
- [ADR-0003-probe-isolation](decisions/ADR-0003-probe-isolation.md): unique probe
  keys, positive ownership for cleanup and lifecycle treatment of unknown orphans.
- [ADR-0004-diagnostics](decisions/ADR-0004-diagnostics.md): distinguish endpoint
  proof from per-pool grants, and introduce a truly non-mutating doctor mode.

## Validation and AC traceability

These are **planned test names**, not claims that tests already exist. Test files
belong in `storage/s3/`, root opening tests, and `cmd/ipam/` respectively.

| AC | Named automated evidence to add | Required observation |
| --- | --- | --- |
| AC-1 | `TestS3VerifiedOpeningAllowsMutations` | Valid create/replace and guarded pool/allocation operations succeed. |
| AC-2 | `TestS3IgnoredCreateConditionBlocksWrites` | Fake accepts duplicate create; no user PUT/DELETE reaches transport. |
| AC-3 | `TestS3IgnoredMatchConditionBlocksWrites` | Fake accepts wrong match; all user mutations blocked. |
| AC-4 | `TestS3ProbePreservesAllUserObjects` | Snapshot bytes, versions and existence including unrelated keys before/after unsafe probe. |
| AC-5 | `TestOpenUnverifiedS3AllowsReads` | Probe denied, constructor usable, authorised Get/List succeed. |
| AC-6 | `TestS3UnverifiedGateCoversEveryMutation` | Table of create/replace/delete, including reserved keys and malformed versions, emits zero user mutation requests. |
| AC-7 | `TestS3OpeningVerifiesExactlyOnce` | Multiple Clients and concurrent operations on one Store never re-probe. |
| AC-8 | `TestS3NewOpeningNeverReusesEvidence` | Each typed and URL opening gets a distinct sequence. |
| AC-9 | `TestS3ProbeRemovesOwnLiveObject` | Successful cleanup leaves no live own object; history remains scoped. |
| AC-10 | `TestS3LaterOpeningCleansTerminalOwnedResidue` | Later opening claims a known-terminal record, not another active owner; subject to DQ-2. |
| AC-11 | `TestDoctorReportsVisibleProbeResidue` | Live, historical and delete-marker inventory; denied/truncated inventory never says empty. |
| AC-12 | `TestS3VerificationGuidanceCoversLifecycle` | Documentation/help fixture names reserved prefix and explains noncurrent versions and markers. |
| AC-13 | `TestS3RejectedPositiveControlIsUnverified` | Always-403, always-412, 409 and malformed/missing-ETag cases cannot verify. |
| AC-14 | `TestS3InterruptedProbeFailsClosed` | Inject cancellation, lost response, timeout and server failure; reason retained, gate blocks. |
| AC-15 | `TestDoctorNoWriteSendsOnlyReads` | Real opener path with fake S3, both output formats and pre-existing pending cleanup: zero PUT/DELETE, unknown evidence. |
| AC-16 | `TestS3ConcurrentOpeningsHaveIsolatedOwners` | Channel-interleaved independent openings; disjoint keys, cleanup and evidence under race detector. |
| AC-17 | `TestS3CleanupFailureIsReported` | Denied/uncertain delete remains pending; API and doctor expose incomplete cleanup. |
| AC-18 | `TestS3ReopeningDetectsChangedStoreBehavior` | Same endpoint first enforcing, then ignoring each condition; second opening blocked. |

NFR-1: add `TestS3VerificationSharesOneDeadline` with injected time/deadline and
transport control; assert the 1-second total and cleanup reserve, not a sleep
threshold. Reference-store qualification must additionally measure constructor
added elapsed time (proof plus cleanup) and reject any observed opening above
1 second; include cold and reused transport cases, at least 100 openings each,
with a zero-probe construction control. A timeout is a safe but **unverified**
opening, not evidence that the reference store remains usable. Unit timing is
not a proxy for AWS latency. No real-service test is run by this design stage.

NFR-2: sequence/request counters in AC-7/AC-8 tests. NFR-3: full object/version
snapshots in AC-4/AC-16 tests. NFR-4: synthetic secret-canary tests
`TestVerificationReportsNeverEchoBackendSecrets` for errors, table, JSON and
warnings. Add compatibility tests for direct `s3.New`, URL `OpenStorage`, `Open`,
`ipam.New(store)`, existing per-operation unknown-outcome behavior, and all other
backends. [quickstart.md](quickstart.md) supplies the implementation validation
commands; none of these implementation tests are fabricated as present here.

## Rollout

1. Approve the questions below, ADRs and threat mitigations at G3; no slicing or
   implementation until the semantic gaps are settled by the spec owner.
2. Implement regression tests before the mutation gate; extend fakes to answer
   opening probes without disabling existing header/error/unknown-outcome tests.
3. Add proof, cleanup and report wiring; then doctor and documentation together.
   Update `README.md` constructor, access tables, diagnostic behavior, recovery
   and lifecycle guidance. Product documentation must acknowledge the changed
   S3 doctor behavior instead of retaining the old universal no-mutation claim.
4. Operators grant GetObject/PutObject/DeleteObject only on the reserved subtree
   plus existing pool grants. Residue inventory uses existing ListBucket and
   optional ListBucketVersions; no bucket administration or DeleteObjectVersion
   permission is required. Do not broaden grants automatically.
5. Treat loss of write capability with old policies as an intentional safety
   change and document it in release notes. No data migration or unsafe feature
   flag. Roll back only to a reviewed safe release; keep read-only diagnosis
   available while correcting permissions or endpoint behavior.

Traces: AC-1, AC-5, AC-6, AC-11, AC-12, AC-15; FR-7; constitution I through III.

## Risks

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Unsafe endpoint gets false assurance | medium | high | Positive controls, exact 412 evidence, single attempts, opening-local state. |
| Existing narrowly scoped identity loses writes | high | medium | Publish minimum probe-prefix grants and preserve authorised reads. |
| Crash residue mistaken for abandoned active work | medium | high | Never reclaim without terminal ownership; lifecycle and explicit unknown inventory. |
| One-second budget insufficient on cold reference-store opens | medium | high | Qualify actual latency before release; fail closed, do not hide timeouts as success. |
| Probe endpoint or credentials change after verification | medium | high | New opening rechecks; no claim of continuous certification or delete verification. |

See [threat-model.md](threat-model.md) for assets, boundaries and residual risk.

## Spec interpretation questions and gate status

- **DQ-1 (AC-15, FR-6)**: The spec assumes an existing explicit no-write mode,
  but `doctor.go` exposes only grant assertions. Proposed resolution: introduce
  `doctor --no-write`, retain `--expect-no-write` as an assertion, and report
  unavailable assertions as WARN without inferring pool denial from probe-prefix
  denial. Confirm the flag and compatibility semantics before implementation.
- **DQ-2 (AC-10, FR-5)**: The spec does not define how a later process identifies
  "its own recoverable leftovers" after losing ownership state. Proposed safe
  boundary: automatic recovery of positively owned terminal records within a
  surviving process; crash/foreign orphans are inventoried and left to lifecycle
  or operator recovery. If cross-process automatic recovery is required, the
  spec needs an explicit ownership/liveness contract; prefix, timestamps and
  successful conditional PUT alone do not provide it on a non-enforcing store.

The approved spec is unchanged. These are not new accepted requirements or
permission to weaken AC-10. Under Spec Kit's unresolved-clarification gate this
plan is a reviewable proposal, **not an implementation-ready design**.
