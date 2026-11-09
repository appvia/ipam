# Tasks: Explicit consent for unverified backends

**Spec**: `specs/REQ-000070-conditional-write-backends/spec.md`
**Design**: `specs/REQ-000070-conditional-write-backends/plan.md`
**Requirement risk**: medium

This slice proposes one Feature and five Stories for appvia/ipam#70. The
materialise workflow creates these six issues only after human merge; this
document does not create issues. All work uses the registered `ipam` area,
one repository per Story, medium risk and Paired autonomy. Agent assignments
are reviewer prose because ADR-0004 permits no additional YAML keys.

The existing `.github/workflows/test.yml`, checked through the GitHub API,
runs tests on `pull_request` across Linux, macOS and Windows, with Linux race,
lint and emulator integration jobs. No CI Enabler is needed. No tasks setup
script or extension configuration exists under `.specify/`; no hooks apply.

## Feature: Operators explicitly consent to unverified backend use

Deliver all three approved journeys: safe default selection, deliberate opt-in,
and truthful diagnosis of the support boundary. Every Story is one PR, not a
separate release. Hold release until all five are complete: classification and
consent plumbing land first, then doctor and documentation, and only then
ordinary default refusal. No temporary feature flag, probe or public read-only
mode is introduced. Intermediate plumbing does not claim default enforcement.

```yaml
kind: feature
title: Operators explicitly consent to unverified backend use
parent: REQ-000070
repo: appvia/ipam
area: ipam
size: L
autonomy: Paired
risk: medium
spec_path: specs/REQ-000070-conditional-write-backends/spec.md
what_to_build: Operators and library callers share one URL-based support policy, explicitly consent before ordinary unverified use, receive private-configuration-free guidance, and diagnose refused stores without unconsented write probes.
acceptance:
  - AC-1
  - AC-2
  - AC-3
  - AC-4
  - AC-5
  - AC-6
  - AC-7
  - AC-8
  - AC-9
  - AC-10
  - AC-11
  - AC-12
  - AC-13
  - AC-14
  - AC-15
  - AC-16
  - AC-17
  - AC-18
  - AC-19
  - AC-20
  - AC-21
  - AC-22
  - AC-23
  - AC-24
  - AC-25
  - AC-26
  - AC-27
  - AC-28
constraints:
  - Ship all five Stories together; no default refusal before consent plumbing, doctor probe gating and migration guidance are present.
  - Preserve approved URL grammar, authentication, CAS operations, document formats and direct-constructor behavior; no provider-repository changes or new dependencies.
  - Apply NFR-1 through NFR-7 and constitution I through V using only synthetic fixtures and no requests to real third-party services.
test_expectations:
  - Every AC-1 through AC-28 maps to an actual named automated test in the implementation PRs; final verification exercises all covered entry points.
  - Existing build, full test, lint and emulator CI remain green; emulator success never qualifies a provider as verified.
hints:
  - specs/REQ-000070-conditional-write-backends/plan.md
  - specs/REQ-000070-conditional-write-backends/data-model.md
  - specs/REQ-000070-conditional-write-backends/threat-model.md
depends_on: []
```

### Story: Expose pure backend classification and scoped consent primitives

**Size**: M. **Autonomy**: Paired. **Agent**: Copilot implement, with a human
reviewer. **Spec path**: `specs/REQ-000070-conditional-write-backends/spec.md`.
**Independent test**: classification works without construction, credentials,
consent or network access; the derived context leaves its parent unchanged.

- [ ] T001 [US1] Add classification and consent primitives in `open.go` and `errors.go`, with focused coverage in `open_test.go`; defer opening refusal to T005.

```yaml
kind: story
title: Expose pure backend classification and scoped consent primitives
parent: Operators explicitly consent to unverified backend use
repo: appvia/ipam
area: ipam
size: M
autonomy: Paired
risk: medium
spec_path: specs/REQ-000070-conditional-write-backends/spec.md
what_to_build: Expose BackendClassification, BackendVerified, BackendUnverified and ClassifyBackend using existing validated targets and one private classifier. Add the ErrUnverifiedBackend sentinel and WithAllowUnverifiedBackend context primitive for downstream wiring, without activating default refusal in this PR. Classification is available before opening and never depends on consent or previous use.
acceptance:
  - "AC-1: Default-resolution S3 classifies as verified."
  - "AC-2: Both approved dot-bounded AWS hostname suffixes classify as verified."
  - "AC-3: Account-form Azure, file and memory classify as verified; container-form Azure remains test-only and unverified."
  - "AC-4: A valid custom S3 host remains unverified regardless of bucket, prefix or addressing style."
  - "AC-8: Callers obtain verified or unverified without consent, successful construction or diagnostic-message parsing."
  - "AC-17: Classification performs zero network requests and zero diagnostic writes; opening-path parity is completed in the enforcement Story."
  - "AC-18: Consent and previous successful use never promote an unverified classification."
  - "AC-23: Classification reuses existing validation and returns its invalid-argument category for invalid input, regardless of consent."
  - "AC-25: Substring, deceptive-suffix, path-only, bare-suffix and IP-literal endpoint matches remain unverified."
constraints:
  - Follow contracts/backend-opening.md and decisions/ADR-0002-support-policy.md under this Requirement directory; no second permissive URL parser or mutable policy registry.
  - Match lowercase parsed Hostname() against dot-bounded suffixes; retain valid ports and paths, and do not strip a terminal DNS dot.
  - Distinguish Azure forms using empty versus nonempty parsed azure.AccountURL; classification reads no connection string or credential configuration.
  - Preserve Open, OpenStorage and Option signatures; derive consent with a private typed context key, retaining cancellation, deadline and values without mutating the parent or adding global state.
  - Invalid classification returns an error and empty result, never a success-shaped verified default; library APIs print no warnings.
test_expectations:
  - TestBackendClassificationDefaultS3 and TestBackendClassificationAWSSuffixes cover no override, both suffixes, case folding and endpoint ports.
  - TestVerifiedBackendCompatibility, TestUnverifiedClassificationIndependentOfLocation and TestBackendClassificationRejectsSuffixLookalikes cover every approved class and synthetic deceptive host/path cases.
  - TestClassifyBackendWithoutOpening and the classifier portion of TestClassificationAndOpenHaveNoIO assert zero constructors, credential reads, requests and diagnostic writes.
  - TestClassificationNeverLearnsOrPromotes and TestConsentNeverBypassesURLValidation preserve results under repeated calls and the BASE-0009 rejection corpus.
  - TestOpeningConsentContextPreservesParent and the classification portion of TestAzureContainerFormUnverifiedPolicy assert context isolation and configuration-free Azure classification.
hints:
  - open.go
  - errors.go
  - open_test.go
  - open_message_test.go
  - storage/context.go
  - specs/REQ-000070-conditional-write-backends/contracts/backend-opening.md
depends_on: []
```

### Story: Resolve CLI consent and emit one support-risk warning

**Size**: M. **Autonomy**: Paired. **Agent**: Copilot implement, with a human
reviewer. **Spec path**: `specs/REQ-000070-conditional-write-backends/spec.md`.
**Independent test**: explicit flag precedence reaches injected openers, allowed
unverified invocations warn exactly once on stderr, and JSON stdout stays valid.

- [ ] T002 [US2] Add shared consent preparation, flag and warning state in `cmd/ipam/commands.go` and `cmd/ipam/main.go`, with tests in `cmd/ipam/main_test.go`; defer default refusal to T005.

```yaml
kind: story
title: Resolve CLI consent and emit one support-risk warning
parent: Operators explicitly consent to unverified backend use
repo: appvia/ipam
area: ipam
size: M
autonomy: Paired
risk: medium
spec_path: specs/REQ-000070-conditional-write-backends/spec.md
what_to_build: Add the persistent allow-unverified-backend boolean flag and injected environment consent resolution when a backend is needed. Reuse shared classification, carry effective consent into both injected opener seams, and print one invocation-scoped support-risk warning before an allowed unverified operation. Prepare ordinary and doctor consumers without activating ordinary default refusal until the final enforcement Story.
acceptance:
  - "AC-9: Explicit true flag permits the successful synthetic command with absent or false environment and exactly one warning."
  - "AC-10: Environment true with no supplied flag grants consent and exactly one warning."
  - "AC-12: Multiple openings and structured output produce exactly one support-risk line on stderr while preserving stdout format."
  - "AC-18: CLI uses the shared classifier and consent never promotes an unverified backend."
  - "AC-21: Support warnings use the common Supported backends URL."
  - "AC-23: Consent never bypasses existing URL validation."
  - "AC-24: Warning and consent-parse diagnostics disclose no supplied private-configuration markers."
constraints:
  - Follow contracts/cli-doctor.md; Cobra Changed decides explicit true or false precedence, so a supplied flag prevents parsing even a malformed environment value.
  - Parse nonempty environment consent with strconv.ParseBool only when the flag is absent; absent or empty means false, malformed input is a static usage error with exit 2.
  - Preserve backend flag over environment selection, existing output streams and exit mappings; help and version need no backend or consent parsing.
  - Pass a narrowly scoped derived context to both opener seams; do not mark the original operation context or use global consent.
  - Set invocation warning state only after a successful stderr write; return warning-writer errors before any requested operation and reset state for each runCLI.
  - Use backend-neutral static wording naming conditional creation, replacement and deletion, S3 headers, corruption risk, both CLI opt-ins and https://github.com/appvia/ipam#supported-backends; do not suppress unrelated warnings.
test_expectations:
  - TestCLIUnverifiedFlagConsent and TestCLIUnverifiedEnvironmentConsent cover both backend selectors and count exactly one support-risk line with successful fake operations.
  - TestCLIUnverifiedWarningOncePerInvocation covers repeated opens, independent invocations, JSON decoding, verified silence and warning-writer failure with zero subsequent operations.
  - TestCLIExplicitFalseOverridesEnvironment initially proves false context propagation; TestCLIConsentDefaultsClosed proves absent, empty, false and malformed resolution, with refusal assertions added in the enforcement Story.
  - TestBackendPolicyDiagnosticsAreRedacted and the warning portion of TestBackendSupportLinks check synthetic private markers, complete static risk text and the identical documentation anchor.
  - TestConsentNeverBypassesURLValidation retains invalid-input category and zero opener calls; help and version work with malformed consent settings.
hints:
  - cmd/ipam/commands.go
  - cmd/ipam/main.go
  - cmd/ipam/main_test.go
  - cmd/ipam/output.go
  - specs/REQ-000070-conditional-write-backends/contracts/cli-doctor.md
depends_on:
  - Expose pure backend classification and scoped consent primitives
```

### Story: Diagnose unverified backends without unconsented writes

**Size**: M. **Autonomy**: Paired. **Agent**: Copilot implement, with a human
reviewer. **Spec path**: `specs/REQ-000070-conditional-write-backends/spec.md`.
**Independent test**: table and JSON reports preserve FAIL/exit 6 and available
reads while recording exactly zero write calls without operator consent.

- [ ] T003 [US3] Wire classification and separate probe enablement in `cmd/ipam/doctor.go`, with recording-store coverage in `cmd/ipam/doctor_test.go` and existing wire-test regressions retained.

```yaml
kind: story
title: Diagnose unverified backends without unconsented writes
parent: Operators explicitly consent to unverified backend use
repo: appvia/ipam
area: ipam
size: M
autonomy: Paired
risk: medium
spec_path: specs/REQ-000070-conditional-write-backends/spec.md
what_to_build: Add the first backend-classification doctor row and retain readable diagnostics even when policy reports FAIL. Separate immutable operator consent from doctor-only derived construction consent and pass independent probeEnabled into orchestration. Without consent on an unverified backend, never call a write probe; emit explicit WARN skip rows for the sorted discovered and expectation-named pool union, or one global row when no pool is available. Preserve existing probes when allowed.
acceptance:
  - "AC-13: Verified doctor reports a textual PASS classification in table and JSON without overriding independent checks."
  - "AC-14: Consented unverified doctor reports WARN and remains unverified; otherwise healthy reports return warn and exit 0."
  - "AC-15: Unverified doctor without consent reports FAIL, overall fail and exit 6 in both formats."
  - "AC-16: Policy FAIL alone does not suppress available backend, protection or fragmentation read-only checks."
  - "AC-17: Diagnostic construction adds zero requests or writes; later read-only diagnosis is measured separately."
  - "AC-23: Invalid URLs retain existing usage failures and Azure diagnostic construction retains every existing constructor restriction."
  - "AC-24: Classification and unverified construction-failure details never expose supplied private-configuration markers."
  - "AC-27: Verified construction and operation failure behavior remains unchanged; classification is not connectivity or permission evidence."
  - "AC-28: Unverified no-consent doctor performs zero write operations even with permission expectations, explicitly reports skipped probes, continues available reads and retains FAIL with exit 6."
constraints:
  - Follow contracts/cli-doctor.md and decisions/ADR-0003-doctor-consent.md; use existing checkResult shape, textual status constants and FAIL-before-WARN aggregation.
  - Discard the internally consented construction context immediately after opening; checks use the original context and operator consent alone controls probes and support-warning semantics.
  - Do not expose ordinary read-only access or allow diagnostic consent to reach ordinary commands; classification FAIL is distinct from actual backend reachability FAIL.
  - Return redacted backend FAIL rows and exit 6 for unverified diagnostic construction failures; never fabricate a handle or successful check.
  - NFR-7 requires exactly zero CreateIfAbsent, PutIfMatch and DeleteIfMatch calls without consent, including expect-write, expect-no-write, expectation-only pools and no-pool cases; a skipped probe is WARN, never PASS.
  - Container-form Azure follows the unverified matrix and retains loopback and connection-string guards; account-form Azure stays verified.
  - Do not repair or extend probe semantics; preserve allowed probe bytes, versions and call order. Changed help must acknowledge possible data changes and inability to prove conditional deletion, with accessible non-colour-only text.
test_expectations:
  - TestDoctorVerifiedClassification, TestDoctorAllowedUnverifiedClassification and TestDoctorRefusedUnverifiedClassification assert exact rows, result aggregation and exit statuses in table and JSON.
  - TestDoctorRefusalKeepsReadOnlyChecks proves actual read calls and included results, not just final status; unreadable stores retain real failures.
  - TestDoctorUnverifiedWithoutConsentNeverWrites records every create, put and delete call across discovered, expectation-only and no-pool cases, both expectation flags and both formats; assert exactly zero calls and explicit skip reasons.
  - Doctor subtests in TestAzureContainerFormUnverifiedPolicy and TestBackendPolicyDiagnosticsAreRedacted cover both consent states, constructor failures and synthetic marker non-disclosure.
  - TestDoctorConsentRetainsExistingProbe and existing doctor_wire_test.go coverage prove verified and consented-unverified probe behavior is unchanged; TestExitCodes remains intact.
hints:
  - cmd/ipam/doctor.go
  - cmd/ipam/doctor_test.go
  - cmd/ipam/doctor_fragmentation_test.go
  - cmd/ipam/doctor_wire_test.go
  - specs/REQ-000070-conditional-write-backends/contracts/cli-doctor.md
depends_on:
  - Resolve CLI consent and emit one support-risk warning
```

### Story: Document the support boundary and breaking-change migration

**Size**: S. **Autonomy**: Paired. **Agent**: Copilot implement, with a human
reviewer. **Spec path**: `specs/REQ-000070-conditional-write-backends/spec.md`.
**Independent test**: named static documentation tests check policy, safety,
consent forms, migration guidance and shared support links without any service.

- [ ] T004 [P] [US3] Update `README.md`, `CHANGELOG.md` and the endpoint comment in `storage/s3/s3.go`, adding static documentation checks in `backend_documentation_test.go`.

```yaml
kind: story
title: Document the support boundary and breaking-change migration
parent: Operators explicitly consent to unverified backend use
repo: appvia/ipam
area: ipam
size: S
autonomy: Paired
risk: medium
spec_path: specs/REQ-000070-conditional-write-backends/spec.md
what_to_build: Publish the README Supported backends section as the single support-policy destination, link backend selection and endpoint reference documentation to it, and record the impending breaking default refusal in the changelog with explicit CLI, environment and library migration examples. Explain support classification rather than attestation, operator qualification responsibility and doctor limitations.
acceptance:
  - "AC-19: README matches FR-1 exactly, explains refusal and all consent forms, and recommends the full storage contract suite before accepting another endpoint."
  - "AC-20: Safety guidance explains conditional creation, replacement and deletion, concurrent document corruption, Amazon S3 as reference and only verified S3 service, and both doctor limitations."
  - "AC-21: Backend selection and endpoint reference links reach the common Supported backends anchor; final enforcement verification checks runtime message links too."
  - "AC-22: Changelog identifies the breaking default-refusal change and both CLI opt-ins plus library consent that restore access without proving safety."
constraints:
  - Use https://github.com/appvia/ipam#supported-backends consistently, including the s3.Config.Endpoint doc comment; explain both AWS suffixes and default resolution without promising endpoint authenticity or connectivity.
  - Distinguish verified account-form Azure from unverified test-only container form with unchanged loopback restrictions; local file and memory remain verified.
  - Show narrowly scoped WithAllowUnverifiedBackend contexts for Open and OpenStorage and library classification before opening; direct constructors and New(store) remain caller-managed.
  - Full contract qualification includes conditional deletion; emulator runs that omit it do not qualify a service. Do not execute live qualification as part of this Story.
  - Conditional creation rejects existing objects; replacement and deletion reject version mismatches. Doctor cannot prove conditional deletion and its existing write probe can change data on non-enforcing stores.
  - No changes to approved AC IDs or baseline requirements; existing BASE-0006, BASE-0009, BASE-0012, BASE-0014 and BASE-0015 overlays already record this Requirement, so do not duplicate them. Add evidence only where implementation requires it.
  - Use synthetic examples only and assess applicable WCAG 2.2 AA criteria for headings, links and explanatory text; release remains held for all five Stories.
test_expectations:
  - TestSupportedBackendsDocumentation compares documented classifications, CLI consent and library examples with the approved support policy, including both Azure forms.
  - TestSupportedBackendsSafetyGuidance checks all three conditions, corruption risk, reference target, full-contract recommendation and both doctor limitations.
  - TestUnverifiedBackendMigrationDocumentation checks the breaking-change notice and all three consent forms without a safety guarantee.
  - Documentation portions of TestBackendSupportLinks check the README backend table and endpoint doc comment resolve to the single existing anchor; runtime links are completed in the enforcement Story.
hints:
  - README.md
  - CHANGELOG.md
  - storage/s3/s3.go
  - backend_documentation_test.go (new named static documentation tests)
  - specs/REQ-000070-conditional-write-backends/contracts/backend-opening.md
depends_on:
  - Expose pure backend classification and scoped consent primitives
```

### Story: Enforce explicit consent at every ordinary opening

**Size**: M. **Autonomy**: Paired. **Agent**: Copilot implement, with a human
reviewer. **Spec path**: `specs/REQ-000070-conditional-write-backends/spec.md`.
**Independent test**: both library openers and every ordinary CLI path refuse
before construction or requested operations, while consent and verified paths
retain behavior and doctor keeps its diagnostic-only exception.

- [ ] T005 [US1] Activate shared refusal in `open.go` and `cmd/ipam/commands.go`, complete parity tests in `open_test.go` and `cmd/ipam/main_test.go`, and update intended-success emulator fixtures without weakening guard tests.

```yaml
kind: story
title: Enforce explicit consent at every ordinary opening
parent: Operators explicitly consent to unverified backend use
repo: appvia/ipam
area: ipam
size: M
autonomy: Paired
risk: medium
spec_path: specs/REQ-000070-conditional-write-backends/spec.md
what_to_build: Activate default refusal after existing URL validation and shared classification but before backend construction in OpenStorage, preserving Open delegation and once-only client-option evaluation. Complete ordinary CLI refusal in the shared preparation helper before even an injected fake opener can grant access. Explicit context consent permits existing construction without reclassification. Add static redacted refusal guidance, adapt intended-success custom-endpoint and emulator fixtures to explicit consent, and finish cross-entry-point regression evidence.
acceptance:
  - "AC-1: Default S3 remains verified without opt-in through both library openers and ordinary CLI selection."
  - "AC-2: Both approved AWS suffixes remain verified across all covered entry points."
  - "AC-3: Account-form Azure, file and memory preserve existing opening and operation behavior without opt-in."
  - "AC-4: Custom S3 classification is consistently unverified across library and CLI entry points."
  - "AC-5: Both openers return nil handles and errors.Is matches ErrUnverifiedBackend before any constructor runs without consent."
  - "AC-6: Both backend selectors refuse ordinary unverified commands with exit 1, actionable guidance and zero requested operations."
  - "AC-7: Both URL openers accept explicit context consent with existing prerequisites satisfied and remain classified unverified."
  - "AC-11: Explicit false flag defeats environment true and prevents any ordinary store operation."
  - "AC-17: Every valid class and consent combination adds exactly zero network requests and diagnostic writes during classification and opening; direct constructors remain network-free."
  - "AC-18: Repeated library and CLI use never promotes a new endpoint, with or without consent."
  - "AC-21: Refusal, warning, backend selection table and endpoint reference all use the same Supported backends URL."
  - "AC-23: Every existing URL rejection retains invalid-argument identity and takes precedence over consent and refusal."
  - "AC-24: Refusals, warnings and doctor classification rows never echo synthetic endpoint, bucket, prefix or credential markers."
  - "AC-26: Absent consent and environment false refuse ordinary access; malformed environment remains a redacted usage failure."
  - "AC-27: Verified constructor causes and operation failures retain their existing error identity and handling, without implying connectivity or permission."
constraints:
  - Follow contracts/backend-opening.md and contracts/cli-doctor.md; install no dependencies and preserve Open, OpenStorage, Option and fixed-arity function assignments.
  - ErrUnverifiedBackend must not also match invalid-argument, conflict or storage sentinels. Missing region or credential configuration cannot mask unverified refusal; verified constructors keep existing errors.
  - Open applies client options exactly once after successful storage construction; preserve retry, clock, random, sleeper and ID configuration, close ownership and nil-option behavior.
  - Policy messages are static, name the library option and both CLI opt-ins, describe all conditional-operation requirements and corruption risk, and link https://github.com/appvia/ipam#supported-backends without interpolating configuration.
  - Keep direct backend constructors and New(store) caller-managed, network-free and otherwise unchanged; no ordinary read-only mode, probes, persisted consent or runtime learning.
  - Explicitly consent in successful custom-endpoint and Azure emulator fixtures. Constructor-rejection fixtures must also consent so missing, non-loopback or invalid connection-string guards are genuinely exercised; retain URL rejection tests with and without consent.
  - Doctor-only construction consent must still not enable ordinary access or probes without operator consent; retain all zero-write doctor regressions after activating library refusal.
  - Preserve all lifecycle, storage-contract, URL, credential and exit-code regressions; release only after all five Stories pass and every AC has named automated evidence.
test_expectations:
  - TestOpenRefusesUnverifiedBackend and TestOpenAllowsExplicitUnverifiedConsent cover both openers, custom S3 and container-form Azure, nil returns, errors.Is, zero constructors on refusal and successful consent with synthetic prerequisites.
  - TestCLIRefusesUnverifiedBackend, TestCLIExplicitFalseOverridesEnvironment and TestCLIConsentDefaultsClosed cover both selectors and all ordinary backend-using command paths, asserting zero opener and operation calls on refusal, including fake openers.
  - Complete TestBackendClassificationDefaultS3, TestBackendClassificationAWSSuffixes, TestVerifiedBackendCompatibility and TestUnverifiedClassificationIndependentOfLocation with opener and CLI parity subtests.
  - TestClassificationAndOpenHaveNoIO counts requests and diagnostic writes for every valid class, consent state and opener, including direct-constructor regressions; TestClassificationNeverLearnsOrPromotes retains unchanged results after use.
  - TestOpenOptionsAppliedOnce, TestVerifiedBackendErrorsPreserved and TestOpeningConsentContextPreservesParent prove callback order, retained options, error causes and scoped consent compatibility.
  - Complete TestConsentNeverBypassesURLValidation, TestAzureContainerFormUnverifiedPolicy, TestBackendPolicyDiagnosticsAreRedacted and TestBackendSupportLinks across both openers, CLI and doctor outputs; retain TestBackendSecretsAreNeverEchoed and all existing URL and wire tests.
  - Rerun TestDoctorUnverifiedWithoutConsentNeverWrites and TestDoctorConsentRetainsExistingProbe after gate activation, then make build, make test and make lint; require existing emulator integration CI without running Docker or live services in the agent environment.
hints:
  - open.go
  - errors.go
  - open_test.go
  - open_message_test.go
  - cmd/ipam/commands.go
  - cmd/ipam/main.go
  - cmd/ipam/main_test.go
  - cmd/ipam/doctor_test.go
  - storage/azblob/azblob.go (existing guards remain unchanged)
  - specs/REQ-000070-conditional-write-backends/plan.md
depends_on:
  - Expose pure backend classification and scoped consent primitives
  - Resolve CLI consent and emit one support-risk warning
  - Diagnose unverified backends without unconsented writes
  - Document the support boundary and breaking-change migration
```

## Dependencies and parallel delivery

T001 is the foundation. After it merges, T002 and T004 may run in parallel:
CLI implementation and README/changelog/endpoint-reference work touch distinct
files. T003 follows T002. T005 follows all four, deliberately activating default
refusal last so existing doctor and CLI paths already carry the right context.
Each Story includes its own tests; there is no deferred testing-only Story.

The first reviewable increment is classification without I/O (T001). The
releasable MVP is the whole Feature, not this increment: all three journeys
and migration guidance ship together. Final verification uses the plan's named
test map, verifies test discovery, and records actual evidence rather than
claiming these proposed tests already exist. Applicable WCAG 2.2 AA checks apply
to diagnostic presentation and documentation in their respective Stories.

## Acceptance coverage

Every approved AC is assigned to at least one Story, not just the Feature.
Repeated assignments deliberately distinguish plumbing from final enforcement.

| Acceptance criteria | Story tasks providing evidence |
| --- | --- |
| AC-1, AC-2, AC-3, AC-4 | T001, T005 |
| AC-5, AC-6, AC-7 | T005 |
| AC-8 | T001 |
| AC-9, AC-10, AC-12 | T002 |
| AC-11, AC-26 | T005 |
| AC-13, AC-14, AC-15, AC-16, AC-28 | T003 |
| AC-17 | T001, T003, T005 |
| AC-18 | T001, T002, T005 |
| AC-19, AC-20, AC-22 | T004 |
| AC-21 | T002, T004, T005 |
| AC-23, AC-24 | T001 (AC-23), T002, T003, T005 |
| AC-25 | T001 |
| AC-27 | T003, T005 |
