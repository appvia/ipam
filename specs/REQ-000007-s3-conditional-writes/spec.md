# Feature Specification: Conditional-write protection on open

**Requirement**: REQ-000007 ([Requirement issue](https://github.com/appvia/ipam/issues/7))
**Spec path**: `specs/REQ-000007-s3-conditional-writes/spec.md`
**Created**: 2026-11-05
**Status**: Draft - region confirmation required before G2
**Input**: Approved [brief](brief.md), [author clarifications](clarifications.md),
and the [constitution](../../.specify/memory/constitution.md)

## User journeys *(mandatory)*

### Journey 1 - Prevent unsafe address changes (Priority: P1)

A platform operator opens the object-store backend identified in the brief,
through either a library consumer or the command-line client. Before any pool
or allocation can change, the backend checks whether the store actually rejects
conditional creates and replacements whose conditions are false. A store that
ignores either condition cannot accept IPAM writes. A verified, enforcing store
continues to support existing operations.

**Why this priority**: Silently accepted conflicting changes can give different
allocations the same address range.

### Journey 2 - Read without write verification (Priority: P1)

An operator with read-only access lists pools, inspects allocations, previews
changes, or runs diagnostics in no-write mode. These activities remain available
without successful write verification. Attempts to change data are refused with
a reason that distinguishes unavailable verification from demonstrated failure
to enforce conditions.

**Why this priority**: Safety must not remove existing read-only workflows or
misrepresent missing permission as proof of an unsafe store.

### Journey 3 - Open again without reusing stale assurance (Priority: P2)

An operator opens the backend again after a configuration or permission change.
That opening has its own check; a previous successful opening is not evidence
that the newly opened backend is safe. Further operations on one verified
opening do not repeat the opening check.

**Why this priority**: Assurance must apply to the store actually being used,
without imposing a check on every pool or allocation operation.

### Journey 4 - Understand temporary check residue (Priority: P2)

An operator inspects residue after an interrupted check. A later check cleans
up its own leftovers, diagnostics report remaining residue, and documentation
explains lifecycle protection for stores that retain historical versions.

**Why this priority**: Temporary verification data must not be confused with
address-management data or silently accumulate without operator guidance.

### Functional requirements

- **FR-1**: Start one verification sequence per backend opening, including the
  reference store and custom stores within the brief's scope. Require positive
  verification before permitting any user-data creation, replacement, or deletion.
- **FR-2**: Establish that an existing check object cannot be created again under
  an absent-object condition, and cannot be replaced under an incorrect version
  condition. Rejection alone is insufficient if valid check writes cannot succeed.
- **FR-3**: Keep reads available under their existing access rules while a store
  is unverified. Report blocked writes explicitly; never offer an unverified
  write bypass or describe an inconclusive check as successful verification.
- **FR-4**: Restrict all verification mutations to temporary, synthetic check
  objects under a reserved prefix within the configured storage scope. Do not
  change existing pool, reservation, allocation, or unrelated objects.
- **FR-5**: Clean up a check's own temporary objects and, on a later check, its
  own recoverable leftovers. Do not remove another concurrent check's objects.
  Historical versions and deletion records may remain under the reserved prefix.
- **FR-6**: Make diagnostic residue reports and lifecycle-rule guidance available
  to operators. Preserve the diagnostic command's explicit no-write mode.
- **FR-7**: Preserve other backend behavior and the supported-store rule in the
  [backend baseline](../BASE-0012-s3-backend/spec.md). Opening-time verification
  intentionally replaces the no-network-contact guarantee in that baseline's
  AC-4 for openings covered here; it does not change unrelated error behavior.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a store that accepts valid conditional writes and rejects
  false create and replacement conditions, **When** a caller opens the backend
  and verification completes, **Then** subsequent authorized pool and allocation
  changes remain available without running a separate diagnostic command.
- **AC-2**: **Given** a store that allows an absent-object-only create to replace
  an existing check object, **When** the backend is opened and a user-data write
  is requested, **Then** that write is refused before reaching user data and the
  caller receives an error stating that conditional-write protection failed.
- **AC-3**: **Given** a store that rejects duplicate conditional creation but
  accepts replacement with an incorrect version, **When** verification runs and
  a user-data write is requested, **Then** that write is refused before reaching
  user data and the caller receives a conditional-write-protection error.
- **AC-4**: **Given** existing pool, reservation, allocation, and unrelated objects
  and a store that ignores conditions, **When** opening-time verification runs,
  **Then** their contents, versions, and existence remain unchanged and any
  verification mutations affect only that check's reserved temporary objects.
- **AC-5**: **Given** credentials that permit reads but not verification writes,
  **When** a caller opens the backend and reads or lists existing data,
  **Then** those authorized reads succeed without requiring a successful probe.
- **AC-6**: **Given** an opening that has not been positively verified,
  **When** a caller attempts to create, replace, or delete user data,
  **Then** every such operation is refused without a user-data mutation and
  reports that verification is required and why it was unavailable or failed.
- **AC-7**: **Given** an enforcing store and one successfully verified opening,
  **When** several reads and writes are performed through that opening,
  **Then** the opening verification sequence occurs exactly once.
- **AC-8**: **Given** an earlier opening verified the same store,
  **When** a new backend opening is made, **Then** that opening performs its own
  verification sequence rather than inheriting the earlier result.
- **AC-9**: **Given** a check that created a temporary object and can remove it,
  **When** the check finishes normally, **Then** its live temporary object is
  removed without changing user data; retained historical versions or deletion
  records are permitted only under the reserved prefix.
- **AC-10**: **Given** recoverable leftovers from an interrupted check and
  permission to remove them, **When** a later check runs, **Then** it cleans up
  its own leftovers without removing user data or another active check's objects.
- **AC-11**: **Given** remaining check residue visible to the diagnostic caller,
  **When** the operator runs the diagnostic command, **Then** the report identifies
  remaining residue under the reserved prefix rather than silently reporting
  that no residue exists.
- **AC-12**: **Given** an operator using a store with retained object versions,
  **When** the operator reads verification and cleanup guidance, **Then** it
  recommends a lifecycle rule for the reserved prefix and explains that removing
  a live check object may leave historical versions or deletion records.

### Edge cases

- **AC-13**: **Given** a store that rejects every check write, including valid
  ones, **When** verification runs, **Then** the store remains unverified and is
  not reported as enforcing conditions merely because requests were rejected.
- **AC-14**: **Given** a check interrupted by cancellation, an unavailable store,
  or an inconclusive response, **When** a write is requested through that opening,
  **Then** it is refused without changing user data and the caller receives an
  explicit explanation that verification did not complete successfully.
- **AC-15**: **Given** read access and the diagnostic command's explicit no-write
  mode, **When** the operator opens the backend for that diagnostic,
  **Then** no check objects or user objects are created, replaced, or removed,
  available read-only diagnostics run, and conditional-write protection is not
  reported as verified without evidence from that opening.
- **AC-16**: **Given** two independent openings checking the same storage scope
  concurrently, **When** both checks and their cleanup run, **Then** neither
  changes the other's check objects or user data and neither inherits the other's
  verification result.
- **AC-17**: **Given** verification cannot remove its temporary object,
  **When** the check reports its outcome and diagnostics subsequently inspect
  visible residue, **Then** cleanup failure is surfaced explicitly, the remaining
  object is reported, and cleanup is not represented as complete.
- **AC-18**: **Given** an earlier opening succeeded but the store now ignores
  either required write condition, **When** a new opening checks the store and
  a write is requested, **Then** the new opening refuses the write without
  relying on the earlier success.

## Non-functional requirements *(mandatory)*

- **NFR-1**: Opening-time verification adds at most **1 second per backend
  opening** on the reference store identified in the approved brief. The author's
  estimate of **4-5 small requests per sequence** is context, not a prescribed
  request design or an additional hard limit.
- **NFR-2**: Verification runs **1 sequence per backend opening**, with
  **0 additional opening-verification sequences per subsequent operation** on
  that same opening.
- **NFR-3**: Verification causes **0 changes to pre-existing user-data objects**,
  including their contents, versions, and existence, even when conditions are
  ignored. Temporary check residue is confined to the reserved prefix.
- **NFR-4**: Check objects, diagnostics, examples, and error messages contain
  **0 real personal records, client identifiers, credentials, or secret values**.
  Check data is synthetic and cannot be traced to a person or customer.
- **NFR-5**: Storage and processing take place in **0 regions outside the UK or
  EU**, subject to constitution article V's pending owner confirmation. The
  permitted named storage and processing regions remain an approval blocker
  until Q1 is answered; this draft does not choose them.

## Key entities *(include if the feature involves data)*

- **Backend opening**: One newly opened access path to the configured store;
  its verification evidence is not shared with a different opening.
- **Verification outcome**: Positive evidence of enforced write conditions,
  demonstrated non-enforcement, or inability to complete verification. Only
  positive evidence permits user-data writes.
- **Check object**: Temporary synthetic content belonging to a verification
  sequence under the reserved prefix, separate from user address-management data.
- **Check residue**: A leftover live check object, historical version, or deletion
  record that may require later cleanup or lifecycle handling.

## Out of scope *(mandatory)*

- Proving conditional deletion behavior for every vendor or certifying stores.
  The existing supported-store rule still requires conditional deletion support;
  successful opening verification is not vendor certification.
- Changes to backends outside the approved brief's object-store scope.
- An opt-out that permits writes without verification; the optional bypass
  suggested in the original report was not part of the approved brief.
- Repairing allocations already affected by an unsafe store, changing allocation
  policy, or replacing existing coordination semantics.
- Changing storage permissions, selecting a deployment region, enabling
  version retention, or automatically installing lifecycle rules.
- Guaranteeing immediate deletion of retained historical versions or deletion
  records after a check.
- Choosing implementation technologies, request sequences, or internal design.

## Open questions *(mandatory, empty before G2)*

1. **Q1 - Residency (Requirement owner):** Which named UK or EU region or regions
   are permitted for the existing store, temporary check objects, and any
   third-party processing involved in verification? Please confirm constitution
   article V's UK/EU restriction and identify any such processors, or confirm
   that verification introduces none. The approved inputs do not supply this
   information; no region or processor is assumed.
   [NEEDS CLARIFICATION: Owner confirmation of permitted regions and processors.]
