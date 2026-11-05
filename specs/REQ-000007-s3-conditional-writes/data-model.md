# Data model: opening verification

## Existing persisted data

No change to `pools/<id>.json`, `reservations/<id>.json`, their schema versions,
incarnations or allocation policy. Never copy their contents into a probe.
Evidence: `doc.go`, `storage/storage.go`, `internal/document/types.go`.
Traces: AC-4, NFR-3, NFR-4.

## Backend opening (in memory)

| Field | Meaning / validation |
| --- | --- |
| mode | `verify` by default, or explicit `read_only`; immutable. |
| scope | Existing SDK client, bucket and normalised configured prefix; never rendered in reports. |
| verification | `pending`, `verified`, `failed`, `unavailable`, or `skipped_read_only`. |
| reason | Fixed safe category: create-condition-ignored, match-condition-ignored, denied, cancelled, deadline, transport, invalid-response, or read-only. |
| proof | Completion flags for two positive writes and both rejected false conditions; never an externally loadable certification token. |
| cleanup | `not_needed`, `complete`, or `pending`, with a separate safe cause. |

Transitions: `pending -> verified | failed | unavailable` exactly once before
the Store escapes `New`. Explicit read-only construction starts and remains
`skipped_read_only`. There is no transition from a returned unverified Store to
verified, no background refresh and no endpoint-wide success cache. A new
opening is a new entity. Mutation allowed iff mode is `verify` and outcome is
`verified`; cleanup status alone neither grants nor revokes proof.
Traces: AC-1 through AC-8, AC-13 through AC-18.

## Check object (temporary S3 data)

Location relative to configured prefix:
`.ipam-checks/v1/<random-256-bit-owner>/probe.json`.
The leading namespace is disjoint from both product document namespaces.
Owner bytes come from `crypto/rand`; randomness failure makes verification
unavailable before any mutation. No host name, account identifier, process ID,
pool name or timestamp is encoded. Owner IDs are unguessable collision
avoidance, not authentication or a distributed liveness proof.

Payloads are bounded synthetic JSON, for example
`{"kind":"ipam-check","format":1,"phase":"initial"}` and a distinct
`phase: replacement`. Negative requests use a third distinct phase. Maximum
encoded probe payload: 256 bytes (design bound serving NFR-1 and NFR-4).
The exact owner/key is constructed internally, never from a listed key or user
supplied path. Reserve the entire subtree for probes; public Store mutation
methods cannot access it even after verification. Reads and lists retain their
existing prefix-relative behavior. Traces: AC-4, AC-9, AC-16; NFR-3, NFR-4.

ETags remain opaque `storage.Version` values. Valid replacement and cleanup
round-trip returned versions verbatim. The false replacement control uses a
syntactically valid quoted synthetic tag selected not to equal the known current
tag; this is a probe-local invalid condition, not parsing ETags or changing the
generic version contract. No persisted user-document version is consulted.
Traces: AC-3, AC-13; FR-7.

## Pending cleanup ownership record (process-local)

Fields: exact generated key, original client/scope handle, last confirmed version
if any, terminal flag and exclusive cleanup-claim state. Register before the
first request, because a lost create response may still leave an object.
An initial create collision means **no ownership** of that existing object:
do not replace or delete it; report verification unavailable.

Only a sequence whose own create succeeded or may have landed owns a cleanup
candidate; never adopt an unrelated key. A completed or cancelled sequence may
publish its candidate as terminal after all its probe writes have stopped.
A later default opening may claim it once. Process-local synchronisation only
protects record ownership; it is not an allocation lock or store correctness
mechanism. Remove the record on confirmed cleanup; preserve pending state on
denial, timeout or unknown outcome. Never retain credential strings in records.
No-write openings do not claim records. Across process death, positive ownership
is lost; no record is reconstructed from listing or age. DQ-2 in `plan.md`
requires approval of this recovery boundary. Traces: AC-9, AC-10, AC-16, AC-17.

## Verification report (read-only value)

`storage.VerificationReport` contains mode, outcome, fixed reason, cleanup state
and sanitised cleanup reason. Its optional typed cause supports `errors.Is`
without being interpolated in CLI strings. `storage.VerificationReporter` adds
`Verification() VerificationReport` without modifying `storage.Storage` or
`storage.Inspector`. Client forwards a copied report with a boolean indicating
support. No report includes bucket, prefix, endpoint, request headers, request
IDs, raw SDK error strings or object contents. Traces: AC-5, AC-6, AC-14, AC-17;
NFR-4; constitution I, V.

## Residue inventory (diagnostic snapshot)

Counts of live objects, historical versions and deletion markers; each category
has `complete`, `unknown` or `partial` visibility. Include a truncation flag and
a fixed error category. Do not render arbitrary returned keys or version IDs.
Inventory is observational, never permission to delete. Missing listing rights
mean unknown, not zero. No new durable database, schema migration or external
integration is introduced. Traces: AC-11, AC-12, AC-17; NFR-4.
