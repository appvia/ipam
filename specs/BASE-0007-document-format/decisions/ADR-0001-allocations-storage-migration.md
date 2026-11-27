# ADR-0001: Migrate reservations storage to allocations

**Status**: Proposed
**Date**: 2026-11-27
**Scope**: BASE-0007 document format

## Context

Allocation state is currently stored at `reservations/<pool-id>.json` in
documents whose `kind` is `reservations` and whose schema version is 1 or 2.
The stored map already represents allocations, child-pool carve-outs and
cooldown state. The name `reservations` no longer describes that complete
state and must change without allowing old and new clients to update separate
authoritative objects.

Conditional writes are the storage correctness boundary. An object rename is
not atomic on every supported backend, and neither a process lock nor a
backend-specific lease is available. The migration therefore needs an
explicit, resumable protocol whose cutover point is a conditional delete of
the legacy object.

## Decision

Introduce allocation-state schema version 3 as follows. This decision defines
the runtime contract; this Story does not implement the command or codec.

| Property | Legacy source | Version 3 target |
| --- | --- | --- |
| Key | `reservations/<pool-id>.json` | `allocations/<pool-id>.json` |
| `schema_version` | `1` or `2` | `3` |
| `kind` | `reservations` | `allocations` |
| `pool_id` | copied | copied and validated against the key |
| `incarnation` | copied | copied and validated against the pool document |
| `allocations` | copied without semantic changes | copied without semantic changes |
| `cooldown` | absent in version 1; nullable in version 2 | `null` when absent; otherwise copied |
| `released` | absent in version 1; required object in version 2 | `{}` when absent; otherwise copied |

The pool document at `pools/<pool-id>.json` is unchanged. Version 3 applies to
the allocation-state document only. No field is removed from the allocation
state during migration. Unknown fields remain subject to BASE-0007's existing
decode-and-discard rule; unknown fields are not copied as a migration payload.

### Command contract

The migration-capable release exposes `ipam migrate allocations` using the
normal `--backend` / `IPAM_BACKEND` selection and output handling. The command
requires `--confirm-no-legacy-writers` so that the operator explicitly accepts
the quiescence precondition. It is idempotent and safe to rerun against a
partially migrated store.

The command enumerates pool documents and processes each pool independently.
For every pool it first validates the pool document and the incarnation, key,
kind and schema of every allocation-state object it observes. It does not
create a missing pool document or repair arbitrary orphan data.

For an unmigrated pool the algorithm is:

1. Read the legacy document and retain its opaque storage version.
2. Transform it to version 3 in memory, preserving the allocation state.
3. Call `CreateIfAbsent` for `allocations/<pool-id>.json`. A different existing
   object is a conflict, not an invitation to replace it.
4. Call `DeleteIfMatch` on `reservations/<pool-id>.json` with the exact legacy
   version read before creation. Creation of the version 3 document alone is
   never a committed cutover.
5. Confirm by reading both keys. A new document with a missing legacy document
   is migrated; the command must not infer success from the delete response
   alone when the backend outcome can be unknown.

If step 4 reports a condition failure or an unknown outcome, the command reads
both keys before acting further. Legacy-absent/new-valid is a completed
migration. If a valid legacy object remains, it is authoritative and the
command may remove only the version 3 object this invocation created, and only
with its exact version. It must not attempt that cleanup unless the legacy
read proves allocation state is still present. If cleanup or either read
cannot be confirmed, the command reports a conflict and leaves both objects
for a later retry; ordinary readers and writers must fail closed while both
exist.

On rerun, the valid states are:

- Legacy only: start the algorithm above.
- New only: validate and report the pool as already migrated; no write.
- Neither: report incomplete pool state; no write.
- Both semantically equal: the previous run may have stopped between steps 3
  and 4. Resume at the conditional deletion of the current legacy version.
- Both different, or either invalid: report a conflict; no write.

A store-level failure can leave earlier pools migrated. Every returned report
identifies migrated, unchanged, conflicted and unresolved pools, and a rerun
must converge without duplicating or losing state. Unsupported schemas and
access failures never cause the command to guess, overwrite or delete.

### Failure and unknown-outcome semantics

The migration itself uses `CreateIfAbsent` for the target and `DeleteIfMatch`
for the legacy key and any confirmed owned-target cleanup. Bridge-client
mutations use the existing `PutIfMatch` path on the one authoritative key. It
never uses an unconditional write. Every conditional operation follows the
existing storage contract and treats an unknown outcome as indeterminate until
confirmation reads prove which transition happened.

After an uncertain creation, equivalent new content created at the target key
may be adopted only as the resumable state; differing content is a conflict.
After an uncertain deletion, the state is success only when the legacy key is
absent and the valid new key remains. A retained legacy key is a failed
cutover. In all indeterminate states the command reports failure rather than
silently continuing with another pool or exposing an authoritative winner.

No failure path is allowed to delete the last readable allocation-state
object. The legacy object is deleted only after the complete version 3 object
has been created and confirmed, and only while the legacy version still
matches the migrated snapshot. The implementation must preserve the original
opaque storage version; parsing or comparing backend version tokens is out of
scope.

### Compatibility window and mixed-version safety

The coordinated provider release uses one bridge release. The bridge accepts a
legacy-only store and continues to write legacy versions 1 or 2 before cutover,
so it remains compatible with the previous library and provider. It accepts a
new-only store and reads and writes version 3 after cutover. If both keys are
present it fails closed, returns a conflict and performs no write. If neither
key is present while the pool exists, the existing incomplete-pool rules apply.

The compatibility window begins when the bridge is released and ends before
the provider rolls out the first allocation-only release. Completion of
`ipam migrate allocations` is a deployment prerequisite, not a calendar-based
event. During the window the library, CLI and provider are released together.
Operators then stop all workloads that can contain a previous, non-bridge
client, run the command and verify the report before resuming workloads with
the migrated provider release.

No storage marker can guarantee process quiescence. A non-bridge process must
not run against the store during or after cutover. Before cutover it may safely
share the legacy key with the bridge because every mutation remains a
conditional write to the same authoritative object. At cutover, old cached
read-modify-write attempts lose their `DeleteIfMatch` or `PutIfMatch` condition
against the removed legacy object and cannot corrupt version 3. After cutover,
an old process sees the missing legacy document and fails; it must never create
or recreate the reservations document for an existing migrated pool.

The coordinated provider release must include the matching library and CLI,
the bridge and migration command, an operator cutover procedure, rollback
instructions based on stopping writers before any reverse migration, and a
CHANGELOG entry. It must not claim that independent or lazy provider upgrades
are safe.

## Test contract

The documentation-only Story's executable evidence starts from a store created
by the previous release through the legacy public client. Its
`TestMigrateAllocationsPreviousReleaseStore` reference-migration scenarios cover
version 1 and version 2 documents, field and policy preservation, incarnation
pairing, the happy cutover, idempotent reruns and a stale conditional-delete
failure that retains readable allocation state. The scenarios use synthetic
data and the in-memory storage CAS contract; they do not add a runtime command.

Because the bridge dual-reads the two storage forms, the executable cross-version test
`TestMigrateAllocationsCrossVersionSafety` exercises the previous, bridge and
migrated clients as separate logical writers. Its happy path proves that
pre-cutover writes remain visible to the previous client, a stale
previous-client conditional write loses at cutover, and post-cutover reads and
writes use only the allocations key. Its failure path creates the resumable
both-key state and proves that the bridge performs no write. Fixed sleeps and
real backends are not used; injected storage versions make the race
deterministic.

The future implementation Story must retain these scenarios against the real
command and bridge, expanding them to cover crash recovery at every command
step and backend unknown outcomes. Those runtime-only paths are not present in
this documentation contract.

## Consequences

- The migration is explicit and cannot silently reinterpret a legacy key.
- The storage state cannot be safely migrated while a non-bridge writer is
  active; the release process must provide a quiescent deployment window.
- Both keys can exist only as a transient, resumable state and are fail-closed.
- A backend that does not honour conditional creation, replacement or deletion
  is not eligible for migration.
- Existing applications keep using `reservations` until the command commits;
  consumers that inspect raw keys must update during the coordinated release.
