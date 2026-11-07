# Data model: Backend support policy

**Source**: [spec.md](spec.md), AC-1..AC-28 and the no-document-change constraint.
**Design**: [plan.md](plan.md), [backend contract](contracts/backend-opening.md).

## Ephemeral entities

| Entity | Fields | Invariants and lifecycle | Trace |
| --- | --- | --- | --- |
| Validated backend target | Existing private `openTarget`: scheme and parsed S3/Azure/local configuration | Produced only by `parseOpenURL`; keep URL validation and secret-free error handling unchanged. Configuration stays private and is never returned as classification metadata. | AC-1..AC-4, AC-23, AC-24 |
| `BackendClassification` | Public string enum: `BackendVerified = "verified"`, `BackendUnverified = "unverified"` | A policy result for a valid FR-1 selection, independent of consent, connectivity, credentials or prior operations. Invalid input returns an error and the empty result, not verified. | AC-8, AC-18, AC-23, AC-27 |
| Opening consent | Private typed context key carrying boolean true in `WithAllowUnverifiedBackend(ctx)`'s derived context | Absent marker means false. Applies to either URL opener receiving that context; preserve parent cancellation/deadline/values and do not mutate the parent. Scope the derived context to the intended opening, never globally. Not a store capability, configuration record or user identity. | AC-5, AC-7 |
| CLI effective consent | Boolean plus flag-supplied indication | Explicit flag true/false wins; otherwise parse injected environment setting. Malformed setting is a redacted usage error, not consent. | AC-9..AC-11, AC-26 |
| Invocation warning state | Private `warnedUnverified` boolean on `cli` | False at `runCLI` start; true only after successfully writing the one support warning; never global or persisted. Warning write errors stop with an error rather than running silently. | AC-12, NFR-3 |
| Doctor execution policy | Classification, operator consent, derived `probeEnabled` | Verified enables existing probes; unverified enables them only with operator consent. Internal diagnostic construction consent must never replace operator consent. | AC-13..AC-16, AC-28 |
| Doctor classification row | Existing `checkResult` fields `check`, `status`, `detail`; no `pool` or `stats` | Check name `backend-classification`. Static detail identifies verified/unverified; statuses follow the contract. No backend URL or private configuration. | AC-13..AC-15, AC-24 |
| Doctor skip row | Existing `checkResult`; `check = "write-probe"`, `status = "WARN"`, optional existing pool ID | Explicit static skipped reason. A pool row uses only the existing diagnostic pool ID, never bucket/prefix/endpoint; no fake successful probe. | AC-28 |

The public enum's empty value is not a policy decision. A future enum expansion
requires contract review; consumers must not treat an unknown value as verified.
The existing loopback-only Azure form is the scope clarification in the plan,
not an additional value introduced by this design.

## State transitions

1. Raw selection -> existing validation -> parsed target or `ErrInvalidArgument`.
2. Scoped parsed target -> deterministic verified/unverified classification.
3. Ordinary open: verified or explicit consent -> existing constructor -> usable
   store/client or existing constructor error; unverified without consent ->
   `ErrUnverifiedBackend`, no constructor and no usable return value.
4. CLI allowed unverified opening -> one warning -> requested operation; no
   consent -> refusal before the operation. Classification never transitions.
5. Doctor: classification plus operator consent -> PASS/WARN/FAIL; available
   read-only checks -> probe or explicit skip rows -> existing aggregation.

No transition reclassifies a store after a successful request, warning,
operator setting or diagnostic result (AC-18).

## Unchanged persisted model

`pools/<id>.json` and `reservations/<id>.json` retain their existing schema
versions, incarnations and lifecycle documented in `doc.go` and the baseline
document contract. `storage.Version` remains an opaque token, and all CAS
operations remain unchanged. No classification, consent, diagnostic state,
provider identity or support-policy cache is written to a pool, reservation,
scratch object or configuration file. There is no data migration (scope,
AC-17, NFR-1).
