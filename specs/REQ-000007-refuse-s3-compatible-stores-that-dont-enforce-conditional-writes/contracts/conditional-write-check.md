# Contract: isolated conditional PUT verification

Scope: every S3 opening, regardless of endpoint selection. All keys below are
relative to the existing configured prefix. No `pools/`, `reservations/` or
unrelated existing key is a probe target. (AC-2, AC-3, AC-4, AC-11.)

## Preconditions

The proposed isolation mechanism assumes cooperating opening clients exclusively
use the reserved `.ipam-checks/v1/` namespace, a cryptographically secure nonce
source and honest reads. These are not authentication or a proof against a
malicious storage operator. A namespace absence read cannot close the race on a
store that ignores conditions: AC-4's absolute guarantee is unresolved at G3.
Do not implement this as though probabilistic isolation were an approved spec
amendment. (AC-4, AC-5; NFR-2.)

Select a random key as described in `../data-model.md`, GET it, and proceed only
on a definitive missing-object result. A missing bucket, denied read, generic
unclassified HTTP 404, malformed response or missing ETag is not absence proof.
Use S3 `NoSuchKey` or unambiguous object-absence response semantics, not just
the broad current `mapError` mapping. A pre-existing key causes regeneration,
never mutation or cleanup. At most three candidates; no bucket listing.
(AC-4, AC-5, AC-7.)

## Verification sequence

Payload A/B/C share the attempt's owner nonce but contain different phase
markers. Readbacks must match the complete expected bytes and ETag, with a
4-KiB read limit. Normal successful operations require nonempty ETags.

| Step | Request | Required observation | Failure handling and trace |
| --- | --- | --- | --- |
| 1 | PUT A, `If-None-Match: *` | Success and version VA; GET equals A/VA. | Failure is inconclusive; never declare unsupported simply because the positive control failed. AC-1, AC-7. |
| 2 | PUT B at same key, `If-None-Match: *` | Definitive HTTP 412; subsequent GET still equals A/VA. | Definitive success means unsupported creation; all other responses/readback changes are inconclusive. AC-2. |
| 3 | PUT B, `If-Match: VA` | Success and version VB different from VA; GET equals B/VB. | Failure, missing/unchanged ETag or mismatched readback is inconclusive. Positive control avoids accepting reject-all stores. AC-1, AC-3, AC-7. |
| 4 | PUT C, `If-Match: VA` (now stale) | Definitive HTTP 412; subsequent GET still equals B/VB. | Definitive success means unsupported version-conditional replacement; all other responses/readback changes are inconclusive. AC-3. |
| 5 | Check caller context before publishing verified handle | Both negative checks verified, positive controls succeeded and caller not cancelled. | No app operation is dispatched before publication. AC-6, AC-8, AC-10. |

Per attempt there are exactly **four logical verification PUTs**
(create A, rejected create B, accepted replace B, rejected replace C), plus
absence/readback GETs and cleanup. SDK retries can add physical requests; this
is still one opening check. No operation on an already-open handle repeats the
sequence. (AC-6; NFR-1.)

The rejection predicate must require an actual HTTP 412 AND no
`storage.ErrUnknownOutcome` in the attempt history, not merely
`errors.Is(err, storage.ErrPreconditionFailed)`. Existing S3 `mapError` also
maps 409 to that sentinel. SDK retries after a possibly-applied attempt can
produce a later 412 answering the client's own write; those remain inconclusive
under `writeOutcome` semantics. Reuse that recorder; inspect status only inside
the verifier, never echo raw errors. Do not change generic storage mapping.
(AC-2, AC-3, AC-7, AC-9.)

Access-propagation retry opt-in and SDK bounded retries retain existing
semantics from `storage/context.go` and `storage/s3/s3.go`. Do not add application
retry loops that hide uncertainty or rerun the full check after failure. Caller
context bounds verification; cancellation stops further verification writes.
(AC-7, AC-8.)

## Cleanup on every exit

If the initial write could have landed, attempt cleanup even when its response
was lost or the last version is unknown. Use a separate context derived with
`context.WithoutCancel`, capped at five seconds. Read only the chosen candidate
key. If absent, finish; if it matches one of this attempt's exact A/B/C payloads,
take its current nonempty ETag and issue `DeleteIfMatch`. If ownership cannot be
established, do not delete; warn that temporary data may remain. This is
ownership defense in depth, not proof of atomic exclusion on a broken store.
(AC-4, AC-8, AC-12, AC-13.)

Never delete collision candidates, never use unconditional DELETE, never scan,
and never delete an object whose ownership marker differs. Treat deletion
failure as incomplete cleanup. For an unknown outcome a bounded absence GET may
establish logical removal; otherwise warn. A foreign replacement or failed
ownership read warns rather than escalating to broader deletion. Cleanup does
not certify conditional DELETE support, recover prior attempts, purge retained
versions or guarantee removal after process death. (AC-4, AC-12, AC-13; scope.)

## Integrity and response assertions

Tests must snapshot every pre-existing object's bytes AND version, force
collisions, overlap conforming opens with barriers, and independently disable
each conditional header. Include deny-all, 409-only, missing-ETag, oversized
readback, unknown-outcome and response-message injection cases. Count zero pool
or allocation writes before successful opening. Do not present these tests as a
proof against all adversarial writers; AC-4 resolution remains a prerequisite.
(AC-1 through AC-14; NFR-2, NFR-3, NFR-4.)
