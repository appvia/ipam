# Technical Design: Explicit consent for unverified backends

**Spec**: `specs/REQ-000070-conditional-write-backends/spec.md`
**Created**: 2026-11-07
**Status**: Draft for G3; Q2 answered and incorporated
**Proposed risk**: medium

## Summary

Classify validated backend selections in the root Go library, refuse unverified
backends by default, and expose explicit opening consent independently from
classification. The CLI uses the same policy and emits one invocation-scoped
warning. Doctor reports the policy result before running available diagnostics;
without consent it executes read-only checks and never enters its write-probe
path. Storage conditions, credentials and document formats remain unchanged.
This covers AC-1 through AC-28 without network activity during opening. The
author's Q2 answer assigns Unverified to the existing test-only container-form
Azure path without changing its loopback/authentication restrictions.

## Technical context

**Languages and versions**: Go 1.27.3 and golangci-lint 2.14.0, pinned in
`mise.toml`; Go is also pinned in `go.mod`. Retain Cobra v1.10.2 and the exact
AWS/Azure SDK versions in `go.mod` with `go.sum`. No dependencies are added.

**Stack decision**: [ADR-0001-stack](decisions/ADR-0001-stack.md). Existing
`github.com/appvia/ipam` library and Cobra CLI; no new service or platform.
The checked-out `.github/skills/` contains `ac-test-mapping` but no Go stack
skill. Commands are `make build`, `make test`, and `make lint`; the existing
`.github/workflows/test.yml` runs tests on Linux/macOS/Windows, Linux race
tests, lint, and emulator integration tests. Agents must not apply infrastructure.

**Repositories touched**: `appvia/ipam` only. The registry lists it as the sole
code repository for product `ipam`. The separate Terraform provider is a later
consumer, not a repository to change in this Requirement.

**Storage**: Existing S3/Azure objects, local files and memory; no new store,
region selection, schema, migration, cache or persisted consent record.

**Testing**: Go unit tests with fake openers, recording stores and synthetic
configuration; existing storage contract and CLI regression suites. New
classification tests need no external service. Later CI emulator tests exercise
existing integration behavior, not proof that an emulator is safe.

**Constraints**: NFR-1 through NFR-7; FR-7 validation and no-network opening;
out-of-scope runtime probes, ordinary read-only mode, provider changes,
credential changes, document changes and doctor probe repair.

## Constitution check

The input constitution is version `0.2.0-draft`, explicitly not ratified; this
design nevertheless applies its rules as required by this stage. Rechecked
after architecture and threat modelling.

| Principle | How the design complies | Exception (needs approver sign-off) |
| --- | --- | --- |
| I. Security baseline | Validate before policy; keep existing credentials, permissions and exact dependency pins; use static redacted diagnostics; no new authentication, authorisation, external integration or personal data. See AC-17, AC-23, AC-24 and the threat model. | None; G3 must review the existing host-policy limitations, not infer an endpoint safety proof. |
| II. Test standards | Named proposed tests cover every AC below; count requests, writes and warning lines; inject openers and synthetic environment getters; retain all regression tests. NFR-5. | None. Proposed tests are not represented as executed or existing evidence. |
| III. Technology choice | Stack ADR, existing exact toolchain pins, `go.mod`/`go.sum` and PR CI; explicit build/test/lint commands above. | None. |
| IV. Accessibility | Textual PASS/WARN/FAIL and skip reasons in both formats; readable headings, links and flag help; no colour-only status. NFR-6. | None; implementation review must assess applicable WCAG 2.2 AA criteria. |
| V. Data handling | Only synthetic fixtures; no private URL, bucket, prefix or credentials in new diagnostics or consent records. AC-24, NFR-4/NFR-5. | None. |

## Existing code and research

Phase 0 research used the checked-out product source, not registry stack hints
as a technology decision. No existing ADR files were found in the target.

| Evidence | Pattern relied on and design consequence | Trace |
| --- | --- | --- |
| `open.go`: `parseOpenURL`, `parseS3`, `validateEndpoint`, `openTarget` | Reuse validated structured targets; endpoint classification is not a second permissive parser. Keep redacted `invalidURL` errors. | AC-1..AC-4, AC-23..AC-25 |
| `open.go`: `Open`, `OpenStorage`, `openAzure`; `client.go`: `Option`, `New`; `storage/context.go`: `WithAccessPropagationRetry` | Reuse the existing typed-context opt-in pattern for opening consent; preserve both opener signatures and client-option evaluation order. | AC-5, AC-7, AC-8, AC-27 |
| `storage/s3/s3.go`: `Config`, `New`, conditional operation methods | Constructor is network-free; conditions already exist and remain unchanged. Update endpoint reference documentation only in later implementation. | AC-17, AC-20, AC-21 |
| `open.go`: `parseAzblob`; `storage/azblob/azblob.go`: `New`, `newClients`, `parseAzuriteConnectionString`, `requireLoopbackURL`; `open_test.go` | Nonempty parsed `azure.AccountURL` distinguishes account form from container form. Classify the latter Unverified without reading connection-string configuration; preserve constructor loopback/authentication guards. | FR-1/Q2; AC-3, AC-5, AC-7, AC-8, AC-17, AC-23 |
| `doc.go`, `storage/storage.go`, `storage/storagetest/contract.go` | CAS is the correctness boundary. A support label is not enforcement; retain full provider qualification including conditional delete. | AC-18, AC-19, AC-20 |
| `cmd/ipam/commands.go`: `cli`, `backendURL`, `client`, `leaf` | Centralize consent resolution with backend selection; all pool/allocation commands flow through the same gate. Keep help/version backend-free. | AC-6, AC-9..AC-12, AC-26 |
| `cmd/ipam/main.go`: `runCLI`, injectable openers, `exitCode` | Carry effective consent into injected openers; refusal uses existing generic exit 1, not a new usage or conflict mapping. | AC-6, AC-11, AC-27 |
| `cmd/ipam/doctor.go`: `diagnose`, `probeWrite`, `finish`, `printDoctor` | Add an independent classification row and a probe enablement input; preserve FAIL-over-WARN aggregation and textual formats. | AC-13..AC-16, AC-28 |
| `cmd/ipam/doctor_test.go`, `doctor_fragmentation_test.go`, `main_test.go` | Reuse recording stores, fake openers, injected environment and format assertions. | NFR-3, NFR-5, NFR-7 |
| `specs/BASE-0006-error-contract/spec.md`, `BASE-0009-backend-url/spec.md`, `BASE-0012-s3-backend/spec.md`, `BASE-0014-cli/spec.md`, `BASE-0015-cli-doctor/spec.md` | Existing Requirement overlays already record the changed contract. Preserve baseline AC IDs; add later implementation evidence, not another rewrite of approved requirements. | AC-5, AC-17, AC-21, AC-28; issue documentation constraint |

Decisions, rationale and alternatives from this research are recorded in the
three ADRs. Phase 1 outputs are this plan, the data model, the two interface
contracts, the ADRs and the threat model. The validation guide is included below
rather than creating files outside the stage's enumerated output set. No setup
script or extension configuration exists under the target `.specify/`; no
pre/post hooks apply, and no scaffold is run that could alter non-spec files.

## Components

| Component | Repo | Responsibility | Acceptance criteria |
| --- | --- | --- | --- |
| Root support policy | appvia/ipam | Pure `ClassifyBackend` and one internal target classifier in `open.go` or a small adjacent file; no mutable registry or discovery. | AC-1..AC-4, AC-8, AC-17, AC-18, AC-23, AC-25 |
| URL opening gate | appvia/ipam | Shared validation/policy/backend construction path; default refusal and one explicit context-scoped opening option for either opener. | AC-5, AC-7, AC-17, AC-23, AC-27 |
| CLI backend preparation | appvia/ipam | Effective boolean consent, option propagation, invocation-local warning state; no ordinary operation before permission to open. | AC-6, AC-9..AC-12, AC-24, AC-26 |
| Doctor orchestration | appvia/ipam | Classification result and diagnostic-only construction; disable probe calls without consent while retaining readable diagnostics. | AC-13..AC-16, AC-24, AC-28 |
| Operator reference and migration | appvia/ipam | Shared README anchor, endpoint references, library examples and changelog warning about the breaking default. | AC-19..AC-22 |

### Opening and consent

The common order is validate URL, classify the parsed target, resolve opening
consent, enforce ordinary policy, then invoke the existing backend constructor.
No constructor runs for a refused ordinary selection, so missing S3 region or
credential configuration cannot mask the dedicated refusal category. Validated
verified selections retain constructor error chains and operation behavior.

Both opening signatures remain unchanged. Add the explicit opening option
`WithAllowUnverifiedBackend(ctx context.Context) context.Context`, following
`storage/context.go`'s existing typed-context opt-in pattern. The derived context
carries a private boolean marker; `OpenStorage` reads it only after validation
and classification. `Open` delegates with the same context and still applies
client options once, after successful storage construction, exactly as today.
No client field, new option interface, function-signature migration or partial
client is needed. Use a narrowly scoped derived opening context; do not store it
globally or reuse it for unrelated backend selections. The parent context's
cancellation, deadline and values are preserved. The library prints no warning.
`ClassifyBackend` neither constructs a store nor reads credential configuration.
Direct `s3.New`, other backend constructors and `ipam.New(store, ...)` remain
caller-managed and outside URL-gate enforcement; document that boundary.

### CLI and diagnostic-only access

Resolve consent only when a backend is needed. A Cobra flag's `Changed` bit
decides precedence, so explicit false wins over environment true. Parse the
environment boolean only if the flag was not supplied; empty means false, and
malformed values produce a static usage error without echoing the value.

Use one CLI preparation helper for selected URL, classification and effective
consent. Pass the derived consent context to the existing injected openers;
do not rely on production openers to enforce policy in fake tests.
An invocation-local `warnedUnverified` bit prevents repeated support warnings.
Reset it for each `runCLI`; unrelated existing warnings remain independent.

Doctor classifies before ordinary opening. It deliberately calls the storage
opener with a derived internal consent context for diagnostic construction,
even when the operator has not consented. Do not use that derived context for `diagnose` or
ordinary commands. Keep the operator's effective consent separate:
the row is still FAIL, no support opt-in warning implies consent, and a
`probeEnabled` input derived from classification plus operator consent is false.
Do not expose a new public read-only opening mode. This narrow CLI exception
does not run ordinary commands or change classification.

Classification FAIL is not the existing backend reachability FAIL. Add it to
the report without triggering `diagnose`'s reachability short-circuit. If actual
construction fails after a valid classification, doctor retains the policy row,
adds a redacted backend FAIL, renders the report and returns exit 6; invalid URLs
still return usage exit 2 before diagnostic construction. Verified constructor
errors retain their existing handling. Other readable diagnostics continue to
follow existing availability rules, not succeed by fallback.

Without consent, never call `probeWrite`: report a WARN skip row for each
discovered or expectation-named pool, plus one global skip row if no pool is
available. Read-only protection and fragmentation checks can still run. No new
status enum is added and no skip is a PASS. With consent or a verified backend,
existing write-probe bytes, version, semantics and ordering remain unchanged.
Update changed doctor help to state the existing probe risk truthfully; do not
repair the probe or claim it cannot change data on a non-enforcing store.

## Contracts

- [contracts/backend-opening.md](contracts/backend-opening.md): library types,
  host matching, opening order, errors, opt-in and compatibility boundary.
- [contracts/cli-doctor.md](contracts/cli-doctor.md): CLI precedence, warning
  content/streams, doctor row schema, skip behavior and exit results.

## Data model

See [data-model.md](data-model.md). Classification and consent are ephemeral,
separate values; existing pool/reservation documents and versions are untouched.

## Decisions

- [ADR-0001-stack](decisions/ADR-0001-stack.md): retain the exact-pinned Go/Cobra
  stack, with no new runtime or dependency.
- [ADR-0002-support-policy](decisions/ADR-0002-support-policy.md): one pure host
  policy and explicit fail-closed opening options, not endpoint probing.
- [ADR-0003-doctor-consent](decisions/ADR-0003-doctor-consent.md): isolate doctor's
  diagnostic construction from operator consent and gate the probe call path.

## Validation and AC-to-test map

These are proposed automated test names for later implementation, not executed
tests. Each grouped row requires a subtest for every listed criterion and both
covered library openers or CLI selectors where relevant.

| Acceptance criteria | Named test and measurable assertion |
| --- | --- |
| AC-1 | `TestBackendClassificationDefaultS3`: default endpoint verified through classifier, both openers and CLI. |
| AC-2 | `TestBackendClassificationAWSSuffixes`: both suffixes, case folding and ports; table-driven parity across entry points. |
| AC-3 | `TestVerifiedBackendCompatibility`: Azure account with fake credential, local and memory construction/behavior; existing prerequisites preserved. |
| AC-4 | `TestUnverifiedClassificationIndependentOfLocation`: fictional custom host, prefixes, buckets and path styles do not change classification. |
| AC-5 | `TestOpenRefusesUnverifiedBackend`: nil store/client, `errors.Is(ErrUnverifiedBackend)` and zero constructor calls, including missing region. |
| AC-6 | `TestCLIRefusesUnverifiedBackend`: both selectors, exit 1, static opt-in/link guidance, zero requested operations. |
| AC-7 | `TestOpenAllowsExplicitUnverifiedConsent`: both openers accept the derived consent context with synthetic prerequisites; classification remains unverified. |
| AC-8 | `TestClassifyBackendWithoutOpening`: both outcomes with no consent, no constructor or credential access. |
| AC-9 | `TestCLIUnverifiedFlagConsent`: absent/false environment, flag true, successful fake operation and exactly one warning. |
| AC-10 | `TestCLIUnverifiedEnvironmentConsent`: injected environment true, flag absent, exactly one warning. |
| AC-11 | `TestCLIExplicitFalseOverridesEnvironment`: true environment, false flag, refusal and zero operations. |
| AC-12 | `TestCLIUnverifiedWarningOncePerInvocation`: repeated opens and JSON; one support-risk line, complete condition guidance, stdout still decodes; separate invocations reset state. |
| AC-13 | `TestDoctorVerifiedClassification`: PASS row in table/JSON, independent aggregation. |
| AC-14 | `TestDoctorAllowedUnverifiedClassification`: WARN row in table/JSON, result warn and exit 0 with otherwise healthy fake checks. |
| AC-15 | `TestDoctorRefusedUnverifiedClassification`: FAIL row in table/JSON, result fail and exit 6. |
| AC-16 | `TestDoctorRefusalKeepsReadOnlyChecks`: assert backend/protection/fragmentation calls and rows despite policy FAIL. |
| AC-17 | `TestClassificationAndOpenHaveNoIO`: counting constructor transports and recording stores, zero requests and diagnostic writes for every valid scoped class/consent/open combination; direct constructors remain network-free. |
| AC-18 | `TestClassificationNeverLearnsOrPromotes`: repeated calls/opens and toggled consent yield the same unverified result. |
| AC-19 | `TestSupportedBackendsDocumentation`: README contains exactly the stated support policy, CLI/library consent and full-contract recommendation. |
| AC-20 | `TestSupportedBackendsSafetyGuidance`: static assertions for all three conditions, corruption risk, reference service and both doctor limitations. |
| AC-21 | `TestBackendSupportLinks`: table, endpoint doc comment, refusal and warning use the identical README anchor. |
| AC-22 | `TestUnverifiedBackendMigrationDocumentation`: changelog states breaking default and all three consent forms without a safety guarantee. |
| AC-23 | `TestConsentNeverBypassesURLValidation`: retain the BASE-0009 rejection corpus; invalid-argument identity with every consent variant and zero constructors. |
| AC-24 | `TestBackendPolicyDiagnosticsAreRedacted`: synthetic markers in valid/rejected fixtures never appear in refusal, warning or doctor policy rows; static link remains. |
| AC-25 | `TestBackendClassificationRejectsSuffixLookalikes`: both deceptive suffixes, hostname substring, path-only match, bare suffix and IP literal are unverified. |
| AC-26 | `TestCLIConsentDefaultsClosed`: absent/empty/false setting refuses; malformed environment yields redacted usage failure. |
| AC-27 | `TestVerifiedBackendErrorsPreserved`: constructor causes and operation failures keep existing identity/text and are not described as proof of safety. |
| AC-28 | `TestDoctorUnverifiedWithoutConsentNeverWrites`: table/JSON, discovered/expectation-only/no pools, both expectation flags; exactly zero create/put/delete calls, explicit WARN skip rows, readable checks continue, overall fail/exit 6. |

Also add `TestOpenOptionsAppliedOnce` for unchanged configuration ordering/retention,
`TestOpeningConsentContextPreservesParent` for cancellation/deadline/values and
unmodified parent consent, and
`TestDoctorConsentRetainsExistingProbe` for the existing allowed probe call
sequence. Retain `TestOpenRejectsInvalidURLs`, `TestOpenS3BuildsStoreWithoutRequests`,
`TestOpenAzureAccountFormBuildsWithoutNetwork`, `TestExitCodes`,
`TestBackendSecretsAreNeverEchoed`, the doctor probe-wire tests, and all lifecycle
and storage-contract regression tests. Update custom-endpoint fixtures with
explicit consent only where successful opening is intended, never weaken a
precondition or remove a safety test.

Add `TestAzureContainerFormUnverifiedPolicy` for FR-1/Q2, AC-5, AC-7, AC-8,
AC-15..AC-17, AC-23 and AC-28: classify fictional `azblob://sample-container/demo`
without configuration or I/O; both URL openers refuse without consent before
construction and allow existing emulator construction with explicit consent.
Table/JSON doctor cases retain FAIL/exit 6 and zero writes without consent,
and WARN with consent when no other check fails. Preserve verified account-form
behavior (AC-3/AC-27). Adapt existing container-form success fixtures to opt in;
also supply consent to constructor-rejection tests so missing/non-loopback or
invalid connection strings still reach and prove the existing guards. Keep URL
rejection tests with and without consent. Use only synthetic configuration and
fake transports; existing emulator CI remains test-only, not qualification.

### Validation guide for implementation

Prerequisites: pinned Go/linter installed, synthetic fixtures and fake transports;
no production credentials or services. From the product checkout:

```sh
go test . ./cmd/ipam -run 'Test(BackendClassification|AzureContainerForm|UnverifiedClassification|VerifiedBackend|OpenRefuses|OpenAllows|OpenOptions|OpeningConsent|ClassifyBackend|CLIUnverified|CLIRefuses|CLIExplicitFalse|CLIConsent|DoctorVerified|DoctorAllowed|DoctorRefused|DoctorRefusal|DoctorUnverified|DoctorConsent|Classification|ConsentNever|BackendPolicy|BackendSupport|SupportedBackends|UnverifiedBackendMigration)' -count=1
```

Expected: every new test runs, the assertions above pass, and diagnostics use
synthetic markers only. Confirm test discovery explicitly; an empty selector
is not verification. Then run `make build`, `make test`, `make lint` and the
repository's existing CI integration jobs. Do not run live endpoint probes as
part of classification verification. The later implementation PR must map all
28 ACs to its actual named tests and report applicable accessibility checks.

## Rollout

Ship classifier, both opt-ins, CLI gating, doctor reporting and migration docs
together; no interval may introduce refusal without guidance or leave doctor
probe gating unwired. Default refusal is an intentional pre-1.0 breaking change
for unverified custom endpoints (AC-22). Explicit consent restores access, not
safety. Do not modify an operator's environment automatically. There is no
data migration or cloud apply. Reverting the release restores the old permissive
behavior and its corruption risk; prefer an informed explicit opt-in over a
silent rollback. Provider configuration is deferred; document library entry
points so a later provider Story can expose matching consent (FR-5 scope).

## Resolved specification boundary: Q2

The [Requirement author's numbered Q2 answer](https://github.com/appvia/ipam/issues/70#issuecomment-6742245024)
assigns Unverified to `azblob://<container>/<prefix>`. It is recorded in
[clarifications.md](clarifications.md) and minimally incorporated into FR-1;
no AC IDs changed. The parsed target's empty `azure.AccountURL` identifies this
existing form without consulting credentials, environment or a live endpoint.
It follows the existing unverified refusal, consent and doctor contracts,
including zero probes without operator consent. Emulator tests explicitly opt
in; consent never skips constructor loopback or connection-string checks.
Account-form Azure remains verified. No supported provider, URL grammar,
authentication behavior or runtime emulator detection is added (FR-1/FR-7 and
the emulator scope constraint). Q2 is resolved; G3 approval is still human-owned.

## Risks

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| URL classification is mistaken for enforcement or endpoint authenticity | Medium | High | Static support-policy wording, no learned verification, full storage-contract guidance; AC-18..AC-20. |
| Doctor diagnostic consent leaks into ordinary access or probe enablement | Low | High | Separate operator-consent/probe values and recording-store assertions; AC-6, AC-28. |
| A consented context is reused for an unrelated opening | Low | High | Narrow derived-context scope, unchanged parent, no global state; assert consent isolation and CLI precedence. AC-7, AC-11. |
| Existing emulator form is silently promoted or its restrictions bypassed | Low | Medium | Apply the answered Q2 Unverified policy; explicit emulator-test consent, retained constructor/URL rejection tests and unchanged loopback guards. FR-1; AC-5, AC-7, AC-23. |
| Breaking refusal interrupts custom-endpoint automation | High | Medium | Atomic docs/options release and changelog migration; AC-6, AC-19, AC-22. |

See [threat-model.md](threat-model.md) for assets, boundaries, security threats,
mitigations and residual risk.
