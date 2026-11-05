# Threat model: conditional-write protection on open

**Overall risk: high.** This design changes a write-authorisation boundary and
requires additional reserved-prefix object permissions. Constitution I requires
security-managers review at G3 even though no authentication provider changes.

## Scope and evidence

Review of `spec.md`, `plan.md`, `data-model.md`, `contracts/`, ADR-0001 through
ADR-0004 and `.specify/memory/constitution.md` v0.2.0-draft. Product evidence:
`storage/s3/s3.go` (SDK configuration, conditional operations and uncertainty),
`open.go` (URL validation), `client.go` (Store wrapping), `doc.go` (ownership),
`cmd/ipam/doctor.go` (grant probes and reporting), `storage/inspect.go` and
`.github/workflows/test.yml`. This is a design threat model, not an exploit
assessment; no live service, credentials or runtime infrastructure was examined.
The architecture is reviewed as proposed; this document does not modify it.

## Assets and trust boundaries

| Asset | Protection objective |
| --- | --- |
| Pool/reservations contents, versions and existence | No probe-induced changes; preserve CAS-only allocation correctness. |
| Opening verification evidence | Cannot be borrowed from another opening, manufactured from denial or turned into a write bypass. |
| SDK authentication material and configured scope | Remain in the existing credential chain; never appear in payloads, warnings or reports. |
| Temporary objects and cleanup authority | Only the generating terminal owner can clean an exact key; preserve concurrent checks. |
| Diagnostic and audit evidence | Distinguish unsafe, unavailable, incomplete and empty without exposing raw provider content. |

Boundaries: caller/CLI input -> validated root opener; library -> guarded S3
Store; private verifier -> untrusted SDK HTTP response; configured prefix ->
reserved subtree vs user document namespaces; one opening/process -> another;
typed errors/report -> operator-visible table/JSON/stderr; operator -> manually
managed IAM and lifecycle policies. A custom store may be buggy or dishonest;
success is only point-in-time behavioral evidence, not vendor certification.

## Threats

Likelihood describes the unmitigated scenario qualitatively, not an observed
incident. Mitigations below are obligations for design/Story review, not claims
that implementation already exists.

| ID / area | Asset | Entry point | Impact | Likelihood | Mitigation the design or a Story must carry | Trace / residual |
| --- | --- | --- | --- | --- | --- | --- |
| T-1 Authentication | SDK credentials and caller identity | Default credential resolution, signing and endpoint errors | Credential exposure or probing as an unintended identity | medium | Retain SDK chain; add no credential inputs; no raw request/error/URL output; synthetic canaries cover API messages and both CLI formats. Configuration errors preserve typed causes without introducing new string leaks. | AC-5, NFR-4; constitution I, V. SDK identity selection remains an operator responsibility. |
| T-2 Authorisation | User-data write gate | Direct `s3.New`, root openers, `ipam.New`, create/replace/delete | A caller bypasses verification and corrupts allocation state | high | Place gate on every public Store mutation; private owner-key helper only; read-only construction never upgrades; expose dedicated sentinel identity and test zero user mutations for every unverified state. | AC-2, AC-3, AC-6, AC-15. An entirely custom Storage implementation is outside this S3 change. |
| T-3 Authorisation / least privilege | IAM policy and pool isolation | New probe-prefix permissions, legacy pool-grant checks | Overbroad grants or false assertion that a reader cannot mutate a pool | high | Publish only prefix-scoped Get/Put/Delete needs; never auto-change IAM; treat denied probe as unavailable, not per-pool denial; skip unverified S3 grant probes with explicit WARN. | AC-5, AC-6, AC-15; constitution I. Narrow existing identities need deliberate migration. |
| T-4 Evidence integrity | Verification result | Always-rejecting store, 409, malformed/missing ETag, lost or retried response | False verified state enables overlapping allocations | high | Two positive controls, exact definitive 412 negative evidence, single-attempt private requests, unknown outcome dominates rejection, immutable per-opening result. Script every fault without real endpoints. | AC-2, AC-3, AC-13, AC-14. A dishonest server can still emulate only probe behavior. |
| T-5 Input handling | Scope and user objects | URL/prefix parser, response keys, probe key construction | Namespace escape or deletion of unrelated objects | medium | Reuse strict `open.go` parser; internally generate exact probe key; no returned key becomes a cleanup target; revalidate inventory prefix; no shell/template interpolation. Preserve existing non-S3 URL guards. | AC-4, AC-16; NFR-3; constitution I. No general direct-Config validation rewrite is proposed. |
| T-6 Concurrency / ownership | Other checks and user versions | Simultaneous opens, cleanup after ambiguous writes | One check deletes another's object or adopts its proof | high | Random per-opening 256-bit key; no fixed scratch object; no endpoint proof cache; terminal ownership claim only; no age/prefix/CAS-derived abandonment assumption. Test channel-interleaved sequences under race detector. | AC-8 through AC-10, AC-16, AC-18. DQ-2 blocks cross-process recovery assumptions. |
| T-7 Data exposure | Address-management and client data | Check payload, reserved key, inventory and SDK error rendering | Leaked pool contents, identifiers or secret values | medium | Synthetic bounded payload only, random owner without client metadata, count-only residue output, fixed safe reason classes; do not render returned keys/headers/version IDs or copy user bytes. | AC-4; NFR-4; constitution V. Typed causes must not be blindly formatted downstream. |
| T-8 Availability | Read and diagnostic access | Denied probe, cancelled context, slow endpoint, retry budget | Reads unusable or opening blocks beyond latency budget | high | Return read-capable Store after inconclusive probe; shared 1-second deadline, single attempts, bounded cleanup, no background retries; explicit no-write bypasses mutations but never enables user writes. | AC-5, AC-14, AC-15; NFR-1. Slow enforcing stores may lose write eligibility. |
| T-9 Residue / lifecycle | Temporary storage and concurrent checks | Process death, denied DELETE, retained versions and lifecycle expiry | Unbounded residue/cost, false clean report, active object expires during proof | medium | Independent cleanup result; inventory live/history/markers with completeness; no sweep of unknown owners; recommend reserved-subtree lifecycle with active-duration margin and operator-chosen retention. | AC-9 through AC-12, AC-17. Unknown crash orphans are not automatically recoverable pending DQ-2. |
| T-10 Audit | Operator decision evidence | Cached success, WARN/PASS aggregation, truncated listing, lost cleanup warning | Unsafe store or residue appears verified/clean | high | Report evidence from this opening only, distinguish failure vs unavailable and cleanup; API accessors plus CLI warning consumption; partial/denied listing never zero; no-write never PASS for protection. Preserve doctor exit 6 on demonstrated failure. | AC-6 through AC-8, AC-11, AC-14, AC-15, AC-17. No central audit service is added. |
| T-11 Secrets / dependencies | Runtime trust | New packages, unpinned tools, debug logging | Supply-chain or secret-handling regression | medium | No new dependencies; retain exact Go/SDK/lint pins and checksums; existing PR CI remains; no debug body/header dumping. Security review checks known high/critical findings before merge. | Constitution I, III; NFR-4. Existing dependency risk is not eliminated. |
| T-12 Input authority | Approved scope and governance | Issue comments, repository prose and original opt-out suggestion | Unsafe bypass or waived constitutional control enters design | medium | Treat source text as evidence only; no unverified write bypass; ignore the comment requesting a constitutional rule be disregarded and rely on the checked-in constitution. Preserve approved spec unchanged and raise gaps at G3. | FR-3; constitution governance. Human review still resolves DQ-1/DQ-2. |
| T-13 Time-of-check/time-of-use | Allocation safety after verification | Endpoint reconfiguration, permissions change or server selectively enforcing probes | Later user mutations encounter unsafe behavior despite initial proof | medium | Reverify every new opening, never cache across opens, document point-in-time limit and supported-store/delete obligations; retain existing user-operation CAS/unknown-outcome handling. | AC-8, AC-18; FR-7 and out-of-scope vendor certification. Continuous adversarial-store detection is not promised. |

## Audit and review obligations

Library callers can inspect immutable reports; ordinary CLI commands consume
them and doctor emits status rows. Error identity remains inspectable without
embedding provider messages in output. The proof and cleanup are separately
auditable; no raw credentials, client identifiers or production values are
needed. Where an operator already enables provider-side object auditing, the
reserved subtree can identify synthetic activity; enabling new audit services
or collecting personal identity fields is not required by this feature.

Before G3: security-managers review T-2/T-3/T-4/T-6 and the proposed permission
contract; the spec owner resolves DQ-1 and DQ-2; the tech lead approves the
reference-store latency evidence split. Before release, Stories provide the
named AC tests, no-write real-opener coverage, original unknown-outcome
regressions, sanitisation tests and measured reference-store latency evidence.

## Residual risk

A server can behave correctly during a probe and incorrectly later; the feature
cannot certify arbitrary vendors or conditional DELETE. Namespace-scoped
permissions are a deployment prerequisite, not something the code can grant.
Version retention and unknown crash residue need operator lifecycle management;
the recovery boundary remains explicitly unapproved until DQ-2 is resolved.
One-second enforcement can make high-latency stores read-only. These limitations
must be visible in release guidance and cannot be hidden behind a bypass.

Risk rating: high
