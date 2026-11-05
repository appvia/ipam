# Technical Design: Verify S3 conditional writes before opening

**Spec**: `specs/REQ-000007-refuse-s3-compatible-stores-that-dont-enforce-conditional-writes/spec.md`
**Created**: 2026-11-05
**Status**: Draft for G3; integrity assumption requires owner clarification
**Proposed risk**: high

## Summary

Introduce an explicit S3 opening boundary that verifies both conditional PUT
behaviours using synthetic, per-open scratch data before returning a usable
store. Keep construction offline, fail closed on incomplete verification, and
deliver cleanup warnings separately from the opening result. Both default and
custom endpoints take the same path. No product implementation is included in
this design. The approved spec remains unchanged. (AC-1 through AC-14.)

**G3 blocker:** AC-4/NFR-2 require zero changes to pre-existing objects even when
the server ignores conditional writes. Random naming plus an absence read
cannot prove that absolute guarantee: another writer can create that key between
the read and the first PUT. The proposed design requires exclusive use of its
reserved scratch namespace by cooperating clients, cryptographic entropy, and
an honest object-read service. The owner must explicitly reconcile that assumption
with AC-4 before implementation is approved; this document does not silently
relax the spec. Existing-object collision tests are necessary but do not prove
the absence of that race. See ADR-0003 and threat T3.

## Technical context

**Languages and versions**: Go 1.27.3; golangci-lint 2.14.0, as pinned in
`mise.toml`. Reuse AWS SDK for Go v2 core v1.47.5, S3 v1.114.6,
config v1.33.12 and smithy-go v1.28.7 from `go.mod`/`go.sum`. No new dependencies.

**Stack decision**: [ADR-0001](decisions/ADR-0001-stack.md). There is no Go stack
skill listed for this product. Use `make build`, `make test`, `make lint`;
`ac-test-mapping` supplies the implementation evidence convention.

**Repositories touched**: only `appvia/ipam`. Its registry entry lists no other
code repository. Infrastructure-automation coverage uses the exported Go API;
provider integration/release coordination is a consumer obligation, not a
change to an unlisted repository.

**Storage**: the existing operator-configured S3 bucket and prefix, plus temporary
synthetic objects in that same store. No database or processor is added. Region
comes from the existing configuration; its actual deployment value is not known
from this Requirement. The owner must confirm UK/EU residency at G3 or record an
explicit constitutional exception. No regional selection/check is introduced.

**Testing**: Go testing, race detector, stateful fake S3 via the existing
`net/http/httptest` pattern in `storage/s3/s3_test.go`; existing storage contract
tests in `storage/storagetest/contract.go`. No live service is required for AC
proof. Existing optional emulator CI is not evidence that every vendor works.

**Constraints**: NFR-1: one check per successful open, zero per subsequent
operation; NFR-2: no pre-existing modifications, subject to the unresolved
integrity assumption above; NFR-3: zero credential disclosure; NFR-4: only
synthetic fixtures. No bypass flag, vendor certification, conditional-delete
certification, continuous rechecking, region enforcement or doctor-probe rewrite.

### Evidence and research decisions

| Source path | Observed pattern and design consequence | Trace |
| --- | --- | --- |
| `doc.go`, `pool.go`, `allocation.go` | Application correctness depends on CAS on pool/reservations documents; never probe them. | AC-2, AC-3, AC-4, AC-10 |
| `storage/s3/s3.go` | `New` is documented offline; ETags are opaque; `put`, `writeOutcome` and `callOptions` already implement conditional requests, uncertain outcomes and bounded SDK retry policy. Reuse these semantics in private probe primitives. | AC-7, AC-8, AC-14 |
| `open.go` | `Open` delegates to `OpenStorage`; the S3 branch currently calls `s3.New`. Wire verification here, without weakening URL validation. Do not wrap runtime verification errors as `ErrInvalidArgument`. | AC-9, AC-10, AC-11 |
| `client.go` | `New(storage.Storage)` cannot return an error and has no context. Keep it offline; enforce readiness in the S3 handle rather than introducing lazy network calls in client operations. | AC-6, AC-10, AC-14 |
| `storage/storage.go`, `storage/inspect.go` | Backend-neutral storage interface plus optional capabilities; leave `Storage` unchanged and keep the new opening API S3-specific. | AC-1, AC-14 |
| `cmd/ipam/main.go`, `cmd/ipam/commands.go` | Injectable openers, stderr diagnostics and stable exit codes. Route opening warnings to stderr and keep verification failures as runtime exit 1. | AC-7, AC-9, AC-10, AC-12, AC-13 |
| `cmd/ipam/doctor.go` | Doctor uses `OpenStorage`, then its existing independent probe. Opening now checks first; do not reuse or alter its reservations-document probe. | AC-10; diagnostic-probe exclusion |
| `storage/s3/s3_test.go`, `storage/s3/outcome_test.go`, `storage/s3/export_test.go` | Fake HTTP servers and no-delay SDK retry helpers are existing test seams. Preserve retry/outcome regression assertions with explicit opening setup. | AC-2, AC-3, AC-7, AC-8 |
| `README.md`, `specs/BASE-0012-s3-backend/spec.md` | Public construction and error contracts need migration documentation; no silent change to baseline specifications in implementation. | AC-9, AC-14 |
| `.github/workflows/test.yml`, `Makefile` | Existing build/test/lint pins and cross-platform tests are sufficient; no CI workflow redesign. | Constitution II, III |

Alternatives rejected: endpoint allowlists cannot prove observed behaviour
(AC-11); negative-only probes can falsely accept stores that reject all
conditional requests (AC-1); probing existing reservations violates AC-4;
lazy first-operation verification violates the explicit opening boundary
(AC-14); a process-global cache violates AC-6. New languages/services add no
benefit for this library-level change (Constitution III).

### Spec Kit redraft and planning status

This redraft addresses the review requesting use of the now-available skill.
The workflow's `spec-kit` skill was loaded before planning; its plan reference
and the product's matching `.github/skills/spec-kit/references/vendor/speckit-plan.md`
were read. `.github/skills/spec-kit/SOURCE.md` records Spec Kit v1.2.5 at commit
`4572a67fe0e72a6837e2d7bb0fda875bc41482e5`. The plan retains the structure of
`.specify/presets/appvia/templates/plan-template.md`. No installation or remote
skill retrieval is needed; the previous missing-skill blocker no longer applies.

The workflow supplies the existing spec directory and redraft branch. The
checkout has neither `.specify/scripts/bash/setup-plan.sh` nor
`.specify/extensions.yml`, so no setup script or extension hook is executed.
No scaffolding is installed outside `specs/`. The skill's Phase 0 decision record
is [research.md](research.md); its Phase 1 validation guide is
[quickstart.md](quickstart.md), alongside the existing data model, contracts and
ADRs. The architecture and its independent threat review remain separate.

Code inspection resolves the technical choices, but cannot resolve an owner's
acceptance of the AC-4 trust assumption. Phase 0 therefore remains blocked for
implementation approval; the Phase 1 artifacts are conditional proposals for
review, not evidence that the Spec Kit clarification gate has passed. The
approved spec's stale Draft/awaiting-G2 wording is preserved unchanged; this run
relies on the workflow's approved-spec input, not a local status amendment.

## Constitution check

| Principle | How the design complies | Exception (needs approver sign-off) |
| --- | --- | --- |
| I. Security baseline | Same SDK identity and endpoint; validate all probe responses; safe diagnostics; scratch-only permissions. | High-risk G3 security review required for expanded scratch access, including previously read-only callers. AC-4 assumption unresolved. |
| II. Test standards | Named proposed tests cover every AC; deterministic barriers, fake responses, synthetic credentials; full existing CI retained. | Tests are planned, not implemented or passing evidence. |
| III. Technology choice | ADR-0001 selects existing exact-pinned Go/SDK stack and explicit build/test/lint commands; the available Spec Kit v1.2.5 skill guides this redraft. | No stack exception. |
| IV. Accessibility | No visual interface; errors/warnings are readable text, separate from machine-readable stdout. CLI warning assertions included. | No new WCAG-applicable UI. |
| V. Data residency | Reuse configured store only, with no external processor or region selection. | Actual deployment region unspecified; owner confirmation or explicit exception required before G3. |
| VI. Data handling | Small synthetic probe payloads; no application reads, client identifiers or credential output. | None. |

Post-design result: not ready for unconditional G3 approval. Integrity and
deployment-residency questions remain explicit, not waived. Skill availability
is no longer a blocker; it does not resolve those owner/security decisions.

## Components

| Component | Repo | Responsibility | Acceptance criteria |
| --- | --- | --- | --- |
| S3 opening boundary | `appvia/ipam` | Add `s3.Open` and `(*Store).Open`; configured handles fail closed until a verified handle is returned. | AC-1, AC-6, AC-10, AC-14 |
| Private S3 verifier | `appvia/ipam` | Isolate a scratch key, exercise successful and rejected conditional PUTs, check readbacks and definitive outcomes. | AC-2, AC-3, AC-4, AC-5, AC-7, AC-11 |
| Cleanup and diagnostics | `appvia/ipam` | Bounded ownership-aware cleanup, sanitized typed errors and independent warnings. | AC-8, AC-9, AC-12, AC-13 |
| Root URL openers | `appvia/ipam` | Use verified S3 open for CLI and automation API; other backends unchanged. | AC-1, AC-10, AC-11 |
| CLI warning adapter | `appvia/ipam` | Deliver warnings to injected stderr without changing stdout or exit-code mapping. | AC-7, AC-9, AC-12, AC-13 |

### Opening and verification flow

1. Parse/validate configuration using existing guards. `s3.New` returns an
   offline configured handle. Its public `Storage` operations and `Inspect`
   return a safe `s3.ErrNotOpen` before any request; wrapping it with `ipam.New`
   cannot bypass verification. This intentionally changes direct-construction
   usage, but not the constructor signature or no-network promise. (AC-10, AC-14.)
2. `(*Store).Open(ctx)` creates a separate candidate handle from immutable
   configuration, runs one check and returns a verified handle or nil plus an
   error. It never mutates the source handle's readiness. Each call, including
   one on a verified handle, creates and checks a new candidate. `s3.Open(ctx,
   cfg)` is `New` followed by this opening operation. No successful check or
   failure is cached across opens. (AC-5, AC-6, AC-14.)
3. Generate 32 cryptographically random bytes for the per-open key beneath
   `<configured-prefix>/.ipam-checks/v1/<64-lowercase-hex>.json`. Read for
   absence before the first write; an existing key is never written/deleted.
   Try at most three candidates; entropy failure, denied reads or exhausted
   candidates are inconclusive failures. Never list or scan the bucket.
   The AC-4 race limitation is the G3 blocker above. (AC-4, AC-5, AC-7.)
4. Execute the precise protocol in
   [the conditional-write contract](contracts/conditional-write-check.md).
   Private raw methods bypass readiness only inside the verifier; public
   callers get no raw handle or bypass option. (AC-2, AC-3, AC-10.)
5. Always attempt bounded cleanup for a candidate that might have been written.
   Read back ownership, then conditionally delete only that exact scratch key.
   Cleanup never touches a collision candidate or a foreign payload. Use a
   cancellation-detached context capped at five seconds, as already used for
   compensation in `pool.go`; a cancelled open may take up to that additional
   bound to return. Cleanup warnings never replace the primary result. (AC-4,
   AC-8, AC-12, AC-13.)
6. Publish only after both checks and their readbacks succeed and the caller
   context remains live at the publication boundary. A cleanup failure alone
   does not block publication. Normal operations on that handle never recheck.
   (AC-1, AC-6, AC-8, AC-12.)

No global registry, lease, local lock or shared scratch key is needed. The SDK
client may be shared because it already supports concurrent calls; copy any
per-call option slices to preserve `slices.Clip` isolation. (AC-5; existing CAS
correctness constraint.)

## Contracts

- [Opening API](contracts/opening-api.md): library signatures, readiness,
  error classifications, warning delivery and CLI consequences.
- [Conditional-write check](contracts/conditional-write-check.md): wire
  sequence, ownership checks, outcome classification and cleanup.

## Data model

See [data-model.md](data-model.md). Existing pool/reservations schemas and
allocation behaviour remain unchanged. (AC-1, AC-4.)

## Decisions

- [ADR-0001-stack](decisions/ADR-0001-stack.md): existing pinned Go/AWS SDK.
- [ADR-0002-opening-boundary](decisions/ADR-0002-opening-boundary.md): explicit
  verified handles, not networked construction or lazy operation checks.
- [ADR-0003-isolated-protocol](decisions/ADR-0003-isolated-protocol.md): isolated
  scratch protocol and explicit limit on absolute integrity guarantees.
- [ADR-0004-diagnostics-and-cleanup](decisions/ADR-0004-diagnostics-and-cleanup.md):
  sanitized errors, independent warnings and bounded cleanup.

## Implementation boundaries and sequencing

Proposed future slices, each limited to `appvia/ipam` and sized M or smaller:

1. Resolve the AC-4 assumption, API migration and scratch permissions with the
   owner/security reviewer before implementation. This is not permission to
   edit `spec.md` in the present design PR. (AC-4, AC-10, AC-14.)
2. Implement the internal protocol and focused fake-store regression tests;
   write failing regressions first if delivered as a Bug. (AC-2 through AC-5,
   AC-7 through AC-9.)
3. Add explicit public opening/readiness and diagnostics contracts, wire root
   openers and CLI adapter, preserve raw wire/outcome coverage and add the
   full opening integration matrix. (AC-1, AC-6, AC-10 through AC-14.)
4. Update `README.md`, Go API comments and CLI/doctor help where opening now
   creates temporary objects. Keep doctor's own probe unchanged. Consumer
   release notes describe read-only permission impact and direct `New` migration.
   (AC-10, AC-12, AC-14; diagnostic-probe exclusion.)

Expected implementation surfaces: `storage/s3/`, root `open.go` and opener
tests, a small warning context helper in `storage/`, CLI context wiring/tests,
and directly related documentation. **Must NOT have:** pool/allocation algorithm
changes, document-schema changes, provider code, workflow/settings changes,
credential-chain changes, new endpoint rules, opt-outs, vendor lists, automatic
orphan scanning, doctor write-probe changes or specification rewrites.

## Verification plan

All names below are proposed automated tests, not existing evidence. Put the
AC id in each test's description. Use the existing `test (ubuntu-latest)` job
for race evidence, macOS/Windows matrix for portability, and `lint` for static
checks. Count logical verifier invocations separately from SDK requests/retries.

| AC | Proposed named test and assertion |
| --- | --- |
| AC-1 | `TestOpenConformingS3PreservesOperations`: valid pool creation/allocation through a verified handle has unchanged results. |
| AC-2 | `TestOpenRefusesIgnoredIfNoneMatch`: only create conditions disabled; nil handle, creation-specific unsupported error, zero app writes. |
| AC-3 | `TestOpenRefusesIgnoredIfMatch`: only replacement conditions disabled; nil handle, replacement-specific unsupported error, zero app writes. |
| AC-4 | `TestOpenPreservesExistingObjects`: snapshot bytes AND versions for pools, reservations, unrelated keys and forced scratch collision; check success and every failure path. The unsupported arbitrary-writer race remains blocked, not claimed proven. |
| AC-5 | `TestConcurrentOpensUseIsolatedScratchKeys`: deterministic request barriers overlap opens; both succeed and cannot touch each other's keys. |
| AC-6 | `TestOpenCheckFrequency`: each successful explicit open has exactly 1 protocol invocation, many subsequent operations have 0, including concurrent operations; another open has 1. |
| AC-7 | `TestOpenInconclusiveFailures`: access denied, transport error, 409, malformed/missing ETag, readback mismatch and uncertain-write retry histories yield inconclusive, never unsupported or success. |
| AC-8 | `TestOpenCancellationAndDeadline`: cancel at each protocol phase with channels; preserve `errors.Is` for context errors; no usable handle/app writes; cleanup bounded. |
| AC-9 | `TestOpeningDiagnosticsNeverDiscloseCredentials`: synthetic secret markers in URLs, SDK errors/response bodies and signing failures absent from error text, chain formatting, CLI stderr/stdout and default warning logs. |
| AC-10 | `TestCLIRefusesBeforeApplicationWrite` and `TestAutomationOpenRefusesBeforeApplicationWrite`: both condition violations across CLI and exported library path; `ipam.New(s3.New(...))` cannot bypass readiness. |
| AC-11 | `TestOpenChecksDefaultAndCustomEndpoints`: inject a local HTTP transport at the SDK test seam to simulate default routing and explicit endpoint routing; no real service requests. |
| AC-12 | `TestOpenCleanupWarningAllowsVerifiedUse`: denied DELETE, uncertain DELETE and ownership mismatch warn exactly once and keep verified open usable; CLI stdout remains valid. |
| AC-13 | `TestOpenFailureAndCleanupWarningCoexist`: each primary failure plus cleanup failure returns nil handle, retains failure classification and emits a separate warning. |
| AC-14 | `TestConstructionOfflineAndExplicitOpenVerified`: zero requests for `New`/unopened operations, fresh verification for its `Open`, never publish a failed candidate. |

Additional protocol regressions: `TestProbeRejectsFalsePreconditions` covers
reject-all, 409 masquerading as a mapped precondition and 412-after-ambiguous-
attempt; `TestProbeCleanupOwnershipAndUnknownOutcome` covers lost create/PUT
responses and refuses deleting foreign payloads; `TestProbeResponseSizeBound`
covers oversized data. Synthetic seeded collisions exercise bounded regeneration;
fake entropy failure makes zero writes. Keep existing storage contract/outcome
tests rather than dropping assertions that opening setup changes.

See [quickstart.md](quickstart.md) for implementation validation prerequisites,
commands and expected outcomes. Its test-inventory check prevents a Go selector
that matches no proposed tests from being mistaken for acceptance evidence.
No credentials or emulator are necessary for the acceptance matrix. Do not
claim AWS/vendor certification from these tests. This documentation-only PR
does not claim implementation tests passed.

## Rollout

No data migration and no safety bypass flag. Release only after G3 decisions
are settled. Document the source-compatible but behaviour-breaking migration
from `s3.New` to `s3.Open`, or `configured.Open`, before first storage use.
Unopened handles now fail explicitly. `ipam.Open`/`OpenStorage` signatures stay
unchanged; network I/O during opening is intentional. Read-only identities now
need narrowly scoped scratch GET/PUT, and DELETE for clean removal; inability to
read/write scratch refuses opening, while cleanup denial only warns. This is
an authorisation change requiring security review, not a suggestion to grant
bucket-wide write access. (AC-7, AC-10, AC-12, AC-14.)

All S3-backed commands, including doctor and read commands, pass this boundary.
Doctor may fail before its report and its existing probe remains unchanged;
opening failure exits 1, not 6 or 2. Update help's no-write wording to distinguish
the new opening scratch activity. Consumers must surface warnings; no provider
patch is claimed in this repo. Rollback reintroduces the known unsafe-open risk;
prefer stopping use of a refused endpoint over downgrading safety. (AC-9, AC-10,
AC-12; explicit scope exclusion.)

## Risks

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Absolute AC-4 cannot be established against arbitrary concurrent writers on a non-enforcing store. | Low for random collisions; material under adversarial namespace access | High | G3 blocker; document isolation/trust assumption and obtain owner resolution, never claim random keys alone prove safety. |
| Scratch access changes least-privilege/read-only deployment expectations. | High | High | Namespace-scoped policies, migration notes, explicit security review; no broad grants. |
| Errors or warnings disclose SDK/endpoint-controlled data. | Medium | High | Fixed safe messages and classified-only causes, marker tests, no request/response logging. |
| Stale cleanup state deletes another actor's data. | Low with cooperating isolated clients | High | Ownership readback, exact-key conditional delete, no scans/unconditional fallback; residual assumption remains. |
| Residue, retained bucket versions or a five-second cleanup delay. | Medium | Medium | One warning, small synthetic objects, bounded cleanup, document that delete markers do not erase retained versions. |
| A store changes behaviour after opening or fabricates successful probes. | Low | High | Explicit trust/residual risk; operator contract validation remains required; continuous checking out of scope. |

The threat review is recorded independently in [threat-model.md](threat-model.md)
and does not change this architecture.
