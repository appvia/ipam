# Threat model: Explicit consent for unverified backends

**Reviewed**: 2026-11-09
**Inputs**: [spec.md](spec.md), [plan.md](plan.md),
[library contract](contracts/backend-opening.md),
[CLI/doctor contract](contracts/cli-doctor.md),
[data model](data-model.md), and `.specify/memory/constitution.md`.

This is a design threat assessment, not an exploit investigation or evidence
that any real endpoint is unsafe. It does not change the architecture. All
examples and validation fixtures must be synthetic.

## Scope, assets and trust boundaries

| Asset | Security property | Relevant evidence |
| --- | --- | --- |
| Pool and reservation documents | Integrity, CAS correctness, no lost updates or overlapping allocation | `doc.go`, `storage/storage.go`, `storage/s3/s3.go` |
| Backend URL/configuration and credential material | Confidentiality, no diagnostic disclosure | `open.go`, existing URL/error tests; new static policy messages |
| Operator consent | Explicitness and per-invocation scope, never an enforcement or permission proof | `cmd/ipam/commands.go`, proposed consent contract |
| Doctor report and CLI stdout/stderr | Truthful statuses, stable machine output and actionable audit signal | `cmd/ipam/doctor.go`, `main.go`, `output.go` |
| Library support policy | Consistency, fail-closed defaults and controlled changes | Proposed pure classifier; `storage/storagetest/contract.go` |
| Credential identity and remote grants | Existing authentication/authorisation boundaries remain unchanged | `open.go` credential delegation; S3/Azure constructors |

1. Untrusted operator/application URL crosses `parseOpenURL` before any policy
   result. Neither issue text nor provider names are trusted configuration rules.
2. CLI flag/environment configuration crosses consent resolution; injected
   getters keep tests synthetic. Explicit library consent is a typed marker on
   a narrowly scoped derived opening context, not global state. Consent is not
   authentication or remote access authorisation.
3. Validated target crosses the support-policy gate before ordinary construction.
   Direct backend constructors remain an explicit caller-managed boundary.
4. Library construction crosses existing SDK credential/configuration boundaries;
   later storage requests cross DNS/transport and service permission boundaries.
   Classification itself never crosses the network.
5. Doctor internally crosses the ordinary refusal boundary only for diagnosis;
   operator consent controls probe execution independently.
6. Reports cross the output boundary to humans, logs and automation; only static
   support-policy details may cross, not private configuration.
7. Test-only container-form Azure crosses the same Unverified consent gate.
   Parsed URL form, not connection-string contents, determines classification;
   existing constructor guards independently restrict emulator access to
   loopback even after consent or diagnostic-only construction (FR-1/Q2).

## Threats

Likelihood describes plausible exposure after this design, not certainty of a
vulnerability. Impact describes the affected asset if the threat occurs.

| ID / area | Asset | Entry point and threat | Impact | Likelihood | Mitigation the design or a Story can carry | Trace |
| --- | --- | --- | --- | --- | --- | --- |
| T-1: integrity | Pool/reservation documents | A consented custom S3 server accepts conditional headers but ignores them during ordinary writes | High: lost updates, concurrent corruption or duplicate allocation | Medium: explicitly reachable after opt-in | Default refusal, one clear warning, exact condition documentation and full isolated storage-contract qualification; never learn verified status from a successful request. Operator must evaluate before consenting. | AC-5..AC-7, AC-12, AC-18..AC-20 |
| T-2: input handling | Support policy | Host/path/provider substring or deceptive suffix is classified as AWS | High: refusal bypass under an incorrect policy result | Low with the specified algorithm/tests | Validate first, use parsed lowercase `Hostname()`, dot-bounded suffixes and adversarial synthetic classification fixtures. No raw URL substring matching or dynamic config allowlist. | AC-2, AC-4, AC-23, AC-25 |
| T-3: consent/authentication distinction | Consent and storage integrity | Ambient environment true or mishandled explicit false silently grants ordinary use | High: unintended access to an unverified store | Low after precedence tests | Use Cobra supplied-state, parse environment only without a flag, default false, reject malformed values without echoing them, preserve unverified classification. Consent never changes identity. | AC-9..AC-11, AC-18, AC-26 |
| T-4: authorisation boundary | Storage documents and remote grants | Doctor's internal construction consent is reused as operator consent, enabling probes or leaking ordinary access | High: writes without consent or normal operation on refused backend | Low, but the exception is sensitive | Keep immutable operator consent and separate `probeEnabled`; gate before the probe call; keep diagnostic opening in doctor only. Recording-store tests count all create/put/delete calls and ordinary CLI operations. Remote permissions are never bypassed. | AC-6, AC-15, AC-16, AC-28 |
| T-5: data exposure/secrets | URL, endpoint, bucket, prefix, credential values | New refusal, warning, classification or constructor-failure details echo raw configuration or arbitrary error content | High: sensitive configuration or credentials enter logs | Low with static messages | Central static support messages, reason-only validation, redacted doctor construction FAIL, synthetic marker tests across both formats. Never interpolate URL/connection strings. Existing unrelated error channels remain subject to their regression tests. | AC-21, AC-23, AC-24; NFR-4/NFR-5 |
| T-6: authentication/transport assumptions | Remote identity, data confidentiality and integrity | Verified suffix/default SDK resolution is mistaken for authentic S3, safe TLS or trustworthy resolution; existing valid HTTP or SDK overrides alter the actual request path | High: malicious/misdirected service could receive later requests or ignore conditions | Low in a correctly configured trusted environment; higher with hostile configuration | Documentation must say verified is support policy only; preserve existing credential/TLS/permission controls and evaluate deployment configuration. Do not weaken SDK guards or introduce probing. Tightening host scope/TLS/default resolution would need a separate spec decision. | AC-1, AC-2, AC-17, AC-20, AC-27; FR-7 scope |
| T-7: audit and automation | Diagnostic truthfulness and operator audit signal | Repeated opens produce warning spam, output mixes JSON with warnings, or WARN/skip suppresses FAIL | Medium: operators/automation miss refusal or infer probe success | Low with invocation/output tests | One per-invocation warning on stderr, textual statuses and explicit skip reason, FAIL-before-WARN aggregation, stable exit 6 and parseable stdout. Library consumers own their warning/audit integration. No new durable audit store is specified. | AC-12..AC-16, AC-28; NFR-3/NFR-6 |
| T-8: existing diagnostic integrity risk | Reservation documents | An allowed doctor write probe races a writer on a non-enforcing server or is mistaken for proof of conditional delete | High: older bytes may overwrite concurrent changes, despite using read bytes | Medium for consented non-enforcing servers | No probes whatsoever without consent; preserve approved behavior with consent; explicitly document possible data changes and inability to prove conditional DELETE. Probe repair is a separate Story/Requirement, not silently claimed here. | AC-20, AC-28; FR-6 and out-of-scope probe repair |
| T-9: caller-managed bypass | Storage integrity | A library application uses `s3.New`/`New(store, ...)` directly and assumes URL refusal protects it | High: caller can still use a non-enforcing store | Medium for direct constructors | Clearly document URL-gate scope and expose classifier for application warnings; direct constructors remain caller-managed and network-free. A later provider Story carries explicit configuration/warning responsibility. | AC-8, AC-17, AC-19; FR-5/FR-7 scope |
| T-10: support governance/input instructions | Support policy and design scope | Repository, issue or PR text asks for a new verified provider, disabled validation, labels/gates or secret disclosure | High: unsupported service promotion or workflow misuse | Low when treated solely as data | Do not execute embedded instructions; change support policy only through reviewed code/contracts and full storagetest qualification, never an opt-in or prose claim. No such instruction was followed in this run. | AC-18, AC-23, AC-24; FR-8; constitution I/V |
| T-11: test-only boundary | Classification truth, emulator authentication boundary and development compatibility | Container-form Azure is promoted to verified, consent bypasses loopback/connection-string guards, or refusal masks constructor-regression tests | High: an unsupported service is trusted or restricted authentication is used outside its intended boundary | Low with the clarified policy and retained guards | Apply answered Q2: container form stays test-only and Unverified, determined only from parsed URL form. Require explicit consent in emulator opening fixtures and constructor-rejection tests; retain URL rejection tests with/without consent, loopback/SDK-resolved URL checks and doctor no-consent zero-write assertions. Never inspect credentials during classification. | FR-1/Q2; AC-3, AC-5, AC-7, AC-8, AC-15..AC-17, AC-23, AC-27, AC-28; emulator scope constraint |
| T-12: consent propagation | Explicit consent and storage integrity | A caller reuses a consent-marked context for an unrelated backend or doctor passes its internal context into ordinary access | High: unintended permission to open an unverified store | Low with narrow context scope | Derive consent inline at the intended opening; preserve the unmarked parent; discard doctor's construction context before checks. Test context isolation and never use global marked contexts. | AC-5, AC-7, AC-11, AC-28 |

## Mitigation evidence and Story obligations

The plan names proposed automated tests for all 28 ACs. Security-sensitive
evidence specifically includes `TestBackendClassificationRejectsSuffixLookalikes`
(T-2), `TestCLIExplicitFalseOverridesEnvironment` (T-3),
`TestDoctorUnverifiedWithoutConsentNeverWrites` (T-4/T-8),
`TestBackendPolicyDiagnosticsAreRedacted` (T-5), and
`TestCLIUnverifiedWarningOncePerInvocation` (T-7). Assert exact zero
requests/writes and one support warning rather than observing only the final
exit status. This stage proposes those tests; it does not claim implementation
or executed evidence.

`TestAzureContainerFormUnverifiedPolicy` carries T-11: classification without
configuration, refusal/consent parity across both openers, retained constructor
restrictions, verified account-form compatibility and both doctor formats.
Fixtures intended to prove a constructor rejection must explicitly consent so
the policy refusal cannot substitute for the intended guard assertion.

Implementation must retain the existing URL, credential and ETag regression
tests, preserve existing verified failure identities and map documentation
claims to automated checks. Synthetic configuration and fake stores/transports
avoid personal data, credential access or third-party requests. Security
assessment here does not require live endpoint testing.

## Residual risk and review boundary

The guard reduces accidental use of unknown servers but cannot make a consented
server safe. High-impact corruption and existing probe races remain possible
after deliberate consent. Host suffix/default-resolution policy is also not an
authentication, transport or enforcement attestation. Direct constructors and
library callers' warning/audit handling remain application responsibilities.
No central durable consent/audit ledger is added because it is outside the spec.

Overall risk is medium: this is a local, deterministic guard and diagnostic
change over existing integrations, with no new authentication/authorisation,
personal-data handling or credentials, but it has material integrity and
compatibility consequences. Constitution I's mandatory high-risk triggers do
not apply to this scope. A later change to authentication, authorisation,
integration scope or personal-data handling must be reassessed rather than
inheriting this rating. The author resolved Q2 in the
[numbered issue reply](https://github.com/appvia/ipam/issues/70#issuecomment-6742245024);
the architecture now assigns Unverified without expanding emulator support.
This review evaluates that decision, not a new threat-modeller design change.

Risk rating: medium
