# Threat model: S3 opening verification

**Overall risk: high.** The design changes the scratch permissions required by
read-only callers, relies on storage atomicity for allocation safety, and cannot
establish the spec's absolute existing-data guarantee against arbitrary writers
on a non-enforcing store. Constitution I requires security review for the
authorisation change. This is a review of the proposed architecture, not a
change to it, and does not approve G3.

## Scope and evidence

Review inputs: `spec.md`, `plan.md`, `data-model.md`, both `contracts/` files,
ADR-0001 through ADR-0004 and `.specify/memory/constitution.md`. Code evidence:
`open.go` (URL/opening boundary), `client.go` (offline client constructor),
`storage/s3/s3.go` (SDK requests, opaque ETags, retries and uncertain outcomes),
`storage/context.go` (per-call retry opt-in), `doc.go` (CAS integrity),
`cmd/ipam/main.go`, `cmd/ipam/commands.go` and `cmd/ipam/doctor.go` (diagnostics
and diagnostic opener). No exploit execution or third-party requests were used.

Out of scope: vendor certification, conditional-delete proof, continuous
reverification, doctor-probe remediation, new authentication schemes,
deployment region selection and orphan recovery. These exclusions do not waive
confidentiality or existing-data integrity. (Spec scope; AC-4, AC-9.)

## Assets

| Asset | Protection goal | Trace |
| --- | --- | --- |
| Pool/reservations documents and allocation versions | No probe mutations; CAS remains the correctness mechanism. | AC-2, AC-3, AC-4, AC-10 |
| Verified handle and capability result | No usable handle after incomplete/failed checks; no constructor bypass. | AC-6, AC-7, AC-8, AC-14 |
| Existing unrelated objects and other checks | No cross-attempt or pre-existing-object modification/deletion. | AC-4, AC-5 |
| Credentials, request signing and configuration | No exposure in errors, warnings, examples or stored payloads. | AC-9; NFR-3, NFR-4 |
| Operator identity permissions and audit diagnostics | Least-privilege scratch access; truthful failure/warning evidence. | AC-7, AC-12, AC-13; Constitution I |
| Availability and bounded resources | Prompt cancellation; bounded readbacks/cleanup; no extra per-operation checks. | AC-6, AC-8 |

## Actors, flows and trust boundaries

1. Operator/automation provides untrusted configuration to validated `open.go`
   parsing. SDK identity resolution remains the existing default credential
   chain. The nonce is isolation, never an identity credential. (AC-9, AC-14.)
2. CLI/root library opens a configured S3 handle. The in-process configured-to-
   verified boundary must not be bypassed by direct construction or private
   probe methods. (AC-10, AC-14.)
3. SDK-signed requests cross the network to the configured S3-compatible
   endpoint. The endpoint, response bodies, ETags, HTTP statuses and SDK error
   text are untrusted; a successful probe is not vendor certification.
   (AC-2, AC-3, AC-7, AC-9, AC-11.)
4. Scratch operations share a bucket/prefix with existing application data.
   Reserved key naming separates data, but IAM is the authorisation boundary;
   names alone are not access control. Other writers and the storage operator
   may have stronger permissions. (AC-4, AC-5.)
5. Safe diagnostics cross into stderr/logs and automation diagnostics. Payloads
   and raw SDK errors must not cross this boundary. (AC-9, AC-12, AC-13.)

No new processor is selected. Existing bucket processing region is deployment-
configured and unspecified by the Requirement; owner confirmation of UK/EU
compliance or an exception is still required. (Constitution V; scope.)

## Threat register

Likelihood describes the pre-mitigation scenario for this design, not a claim
that the repository currently has a verified exploitable vulnerability.

| ID / category | Asset and entry point | Impact | Likelihood | Mitigation carried by design or future Story | Trace |
| --- | --- | --- | --- | --- | --- |
| T1 Authentication | SDK identity and configured endpoint; invalid, expired or unavailable credentials during any probe request | False claim of nonconformance or accidental acceptance; secret-bearing diagnostics | Medium | Leave SDK identity selection unchanged; fail inconclusive on authentication/communication errors; never treat denial as unsupported; no new credential fields or token logging. | AC-7, AC-9, AC-11 |
| T2 Authorisation | Deployment IAM policy for `.ipam-checks/v1/`; read-only callers asked to enable opening checks | Broad write grants could expose pools or other namespaces; previously valid read-only opens fail | High | G3 security review; scope GET/PUT/DELETE to the existing prefix's scratch namespace, retain original application permissions; no bucket-admin or bucket-wide write grant. Denied verification refuses; denied cleanup warns. Migration documentation required. | AC-7, AC-10, AC-12, AC-14; Constitution I |
| T3 Integrity and namespace race | Existing data; absence GET followed by initial PUT on a store ignoring `If-None-Match` | Another writer's newly created object can be overwritten; violates absolute AC-4 | Low for random collision, medium for hostile shared access | 256-bit names, reserved namespace, bounded collision regeneration and ownership checks reduce exposure but cannot atomically prove absence. Explicit G3 blocker: owner must reconcile trusted exclusive namespace assumptions or require an approved alternative before implementation. | AC-4, AC-5; NFR-2 |
| T4 False capability evidence | Verified handle; negative PUT responses and SDK retries | Unsafe backend accepted, permitting conflicting allocations | Medium | Successful positive controls and readbacks; require definitive HTTP 412 with no unknown-outcome history; 409, generic mapped sentinels, missing ETags and malformed results remain inconclusive. Separate tests for each ignored header and reject-all stores. | AC-1, AC-2, AC-3, AC-7 |
| T5 Readiness bypass | Application documents; `s3.New` wrapped directly with `ipam.New`, or verifier-only primitives exposed | App writes reach an unchecked store | Medium | Unopened public methods return `ErrNotOpen` with zero requests; only explicit open publishes a distinct verified handle; keep raw probe methods private; test CLI, URL API and direct-construction paths. | AC-6, AC-10, AC-14 |
| T6 Cleanup integrity | Other objects/checks; stale ETag, uncertain write result or foreign payload at cleanup key | Wrong object removed, verification error hidden or residue silently retained | Medium | Exact-key ownership readback and conditional delete, never unconditional fallback; detached five-second budget; warn if ownership/removal unknown; preserve original failure. Arbitrary-writer race remains covered by T3, not declared solved. | AC-4, AC-5, AC-8, AC-12, AC-13 |
| T7 Data exposure and secrets | Credentials/configuration; reflected SDK errors, endpoint-controlled XML, transport errors, warning logs | Credentials or sensitive endpoint/application identifiers appear in caller output or telemetry | Medium | Fixed text, safe enum fields and allowlisted causes only; do not unwrap raw SDK exceptions from new opening errors; no request/response/body logs; default logger and CLI handler use same safe warning. Test synthetic markers and chain formatting. | AC-9; NFR-3, NFR-4 |
| T8 Input handling and resource exhaustion | Process resources and target keys; oversized bodies, invalid ETags, malformed responses, entropy failures | Memory exhaustion, wrong-target access or false opening result | Medium | Bounded 4-KiB readbacks and 1-KiB payloads; retain URL guards; internally generated fixed-format key; no shell/evaluation; treat missing or unusable versions as inconclusive; entropy failure makes zero writes. | AC-4, AC-7, AC-9 |
| T9 Availability and cancellation | CLI/automation; stalled service, denied access propagation, repeated opens | Hung requests, extra storage costs, orphan objects after interruption | Medium | Respect caller context in verification; reuse bounded SDK retries, no full-protocol retry loop; one check per explicit open, none per operation; five-second cleanup bound. Crash residue remains explicit. | AC-6, AC-7, AC-8, AC-12, AC-13 |
| T10 Audit and warning suppression | Operator understanding; cleanup failure on a successful open or failure-plus-cleanup scenario | Operators mistake refusal for success or never learn residue may remain | Medium | Stable typed failure reasons and warning code; synchronous per-context handler or default logger; CLI stderr separate from JSON stdout; exactly one cleanup warning per attempt, no warning-as-error substitution. No new audit service or sensitive object identifiers. | AC-7, AC-9, AC-12, AC-13 |
| T11 Post-check drift or malicious store | Allocation integrity; endpoint selectively honours probes or later changes semantics | Successful opening is mistaken for lasting proof and later CAS becomes unsafe | Low | Clearly document point-in-time conditional-PUT evidence only; retain operator storage-contract validation responsibility; no certification or unsupported continuous-check claim. An actively dishonest store is outside this verifier's security guarantee. | AC-6, AC-11; stated exclusions |
| T12 Data retention/residency | Synthetic scratch content; versioned store, cleanup denial or process death | Retained versions/residue, unknown processing jurisdiction | Medium | No personal/application data in probes; cleanup warning and documented retention limits; no implicit region exemption; owner confirms deployment region/exception. No lifecycle-policy change or cleanup facility added. | AC-4, AC-9, AC-12, AC-13; Constitution V, VI |

## Required security evidence and review disposition

The implementation must supply the plan's named AC tests, especially independent
header violations, 409/unknown-outcome histories, zero-request unopened handles,
bytes-and-versions snapshots, forced collisions, concurrent barrier-controlled
opens, secret-marker rejection, warning-only success and simultaneous failure
plus warning. These are planned mitigations, not evidence already collected.
(AC-1 through AC-14.)

G3 requires security-managers review of the permission impact and owner resolution
of T3/absolute AC-4, plus region confirmation or a recorded constitutional
exception. The available Spec Kit v1.2.5 skill was loaded for this redraft;
its availability removes the earlier tooling blocker, not these security or
integrity conditions. See `research.md` for the decision record and
`quickstart.md` for planned evidence. Do not widen IAM, modify the approved spec,
approve gates or amend the design automatically as a consequence of this threat
review.

## Residual risk

No request sequence can compel an arbitrary non-enforcing or dishonest server
to preserve other writers' data. The proposed isolation reduces ordinary
collision risk but is not a substitute for an approved trust assumption.
Point-in-time checks cannot guarantee future storage behaviour; DELETE support,
crash cleanup and physical erasure of noncurrent versions remain unproven.
Scratch privileges are an intentional deployment-authorisation change, not a
transparent upgrade. The design is reviewable but not implementation-approved
until those G3 conditions are addressed.

Final risk rating: high
