# Feature Specification: Allocations

**Requirement**: Baseline (no Requirement issue)
**Spec path**: specs/BASE-0003-allocations/spec.md
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Reserve a network from a pool (Priority: P1)

A library caller uses `Client.Allocate` with an `AllocationSpec` to reserve a CIDR by prefix length or to request an explicit CIDR. The caller can supply an allocation ID so repeating the same request retrieves the existing reservation instead of consuming more space. Omitting the ID requests a generated ID.

**Why this priority**: Reserving non-overlapping address space is the primary purpose of the allocation API.

### Journey 2 - Inspect and annotate reservations (Priority: P2)

A library caller retrieves an allocation with `GetAllocation`, inventories a pool with `ListAllocations`, and changes an allocation's description and labels with `UpdateAllocationMetadata` without changing its network or identity.

**Why this priority**: Callers need to reconcile existing assignments and maintain their ownership metadata.

### Journey 3 - Release an assignment (Priority: P2)

A library caller releases an allocation by pool and allocation ID. Releasing an already absent allocation in an existing pool succeeds. An operator recovering an orphaned carve-out can also release its reserved allocation ID, subject to the child ownership guard and the requirement that no child creation is still running.

**Why this priority**: Removing assignments permits subsequent reuse under the pool's policy and supports recovery of abandoned carve-outs.

## Acceptance criteria *(mandatory)*

These criteria assume a usable pool, valid caller inputs, and a live context unless a criterion states otherwise. Pool usability is defined in BASE-0001 pool-lifecycle. Selection strategy and cooldown behavior are defined in BASE-0002 allocation-policy; ID and CIDR normalization rules are defined in BASE-0005 input-validation.

- **AC-1**: **Given** a pool whose policy permits a free prefix of the requested length, **When** a caller allocates by prefix length, **Then** the returned CIDR is aligned, has that length, lies wholly within one pool CIDR, and does not overlap a live allocation.
  - Evidence: `internal/alloc/property_test.go` `TestPropertySelect`; `client_test.go` `TestAllocateConcurrentGeneratedIDs`
- **AC-2**: Withdrawn (CIDR normalization belongs to BASE-0005 input-validation; allocation-specific canonical-request idempotency remains in AC-5).
- **AC-3**: **Given** an IPv6 pool whose policy permits matching free space, **When** a caller requests a `/64` by length or requests an available explicit contained IPv6 CIDR, **Then** the length request succeeds with an aligned `/64` wholly within a pool CIDR, and the explicit request succeeds with its canonical requested IPv6 CIDR.
  - Evidence: `internal/alloc/alloc_test.go` `TestNext/allocates IPv6 including a 64 from a 48` (internal selection result); `internal/alloc/explicit_test.go` `TestExplicit/accepts IPv6 in a matching parent` (internal acceptance check only); UNTESTED (client-level success and returned IPv6 CIDRs in `allocation.go` `Allocate` and `allocateAttempt`)
- **AC-4**: **Given** sufficient space and an ID generator producing distinct valid IDs, **When** concurrent callers omit the allocation ID, **Then** each successful call returns a distinct ID and a distinct non-overlapping CIDR.
  - Evidence: `client_test.go` `TestAllocateConcurrentGeneratedIDs`
- **AC-5**: **Given** an allocation created with a supplied ID, **When** callers repeat the same canonical request with that ID in the same pool, including concurrently or with equivalent masked and unmasked explicit CIDRs, **Then** the calls succeed with the existing CIDR rather than creating another allocation.
  - Evidence: `client_test.go` `TestAllocateSameIDConvergesAndReuseConflicts`; `client_test.go` `TestAllocateExplicitCanonicalizesBeforeIdempotency`
- **AC-6**: **Given** an existing allocation ID in a pool, **When** a caller reuses that ID for a different request, such as a different prefix length, **Then** allocation fails; see BASE-0006 error-contract AC-23 for the reason string and sentinel identity.
  - Evidence: `client_test.go` `TestAllocateSameIDConvergesAndReuseConflicts`
- **AC-7**: **Given** an existing allocation, **When** a caller uses `GetAllocation` with its pool and ID, **Then** the result contains the saved ID, pool ID, request, CIDR, description, labels, and creation time.
  - Evidence: `client_test.go` `TestClientPoolAndAllocationAPI`
- **AC-8**: **Given** an existing allocation in a pool, **When** a caller uses `ListAllocations`, **Then** the list includes that allocation with its saved fields.
  - Evidence: `client_test.go` `TestClientPoolAndAllocationAPI`
- **AC-9**: **Given** an existing ordinary allocation, **When** a caller changes its description and labels with `UpdateAllocationMetadata`, **Then** the supplied metadata is available on retrieval while its ID, CIDR, recorded request, and creation time remain unchanged.
  - Evidence: `client_test.go` `TestClientPoolAndAllocationAPI`
- **AC-10**: **Given** an existing ordinary allocation, **When** a caller releases it and repeats the release while the pool still exists, **Then** both releases succeed and subsequent retrieval fails with `ErrNotFound`.
  - Evidence: `client_test.go` `TestClientPoolAndAllocationAPI`
- **AC-11**: **Given** a request that creates a new allocation, **When** allocation succeeds, **Then** its result records the chosen ID, requested pool ID, canonical request, assigned CIDR, supplied description and labels, and a creation timestamp from the client's configured clock expressed in UTC.
  - Evidence: UNTESTED (behaviour in `allocation.go` `allocateAttempt`)
- **AC-12**: **Given** an existing allocation and a request with the same ID and canonical network request but different description or labels, **When** the caller repeats `Allocate`, **Then** it returns the existing allocation and does not use the new metadata to update it.
  - Evidence: UNTESTED (behaviour in `allocation.go` `allocateAttempt` and `requestsEqual`)
- **AC-13**: **Given** a usable pool, **When** a caller lists its allocations, **Then** the results are ordered lexicographically by allocation ID, and a pool with no allocations produces an empty successful result.
  - Evidence: UNTESTED (behaviour in `allocation.go` `ListAllocations`)

### Edge cases

- **AC-14**: **Given** a valid explicit CIDR and a nonzero `PrefixLength` in the same allocation specification, **When** the caller allocates, **Then** the call fails with `ErrInvalidArgument` because the selectors are mutually exclusive.
  - Evidence: UNTESTED (behaviour in `allocation.go` `canonicalAllocationRequest`)
- **AC-15**: **Given** a pool containing an available `/0` and no cooldown restriction, **When** a caller supplies neither a valid explicit CIDR nor a nonzero prefix length, **Then** the zero-valued prefix length is treated as a request for `/0`, rather than as an omitted-selector error.
  - Evidence: UNTESTED (behaviour in `allocation.go` `canonicalAllocationRequest` and `internal/alloc/alloc.go` `Select`)
- **AC-16**: **Given** an available host-sized IPv4 or IPv6 pool CIDR, **When** a caller requests a `/32` or `/128` respectively, **Then** the host-sized CIDR can be allocated.
  - Evidence: `internal/alloc/alloc_test.go` `TestNext/supports zero and host-length boundaries`
- **AC-17**: **Given** a prefix-length request that is negative or exceeds 128 regardless of whether its allocation ID is already in use, or a request with an unused allocation ID whose prefix length exceeds the pool family's address width or is shorter than every individual pool CIDR's prefix length, **When** the caller allocates, **Then** the call fails with `ErrInvalidCIDR` and does not reserve space; adjacent pool CIDRs are not joined to satisfy an oversized request.
  - Evidence: UNTESTED (public error mapping and request checks in `allocation.go` `canonicalAllocationRequest`, `invalidRequestReason`, and `mapAllocatorError`; containment checks in `internal/alloc/alloc.go` `Select`)
- **AC-18**: **Given** a new allocation ID and a valid explicit CIDR not wholly contained within any single same-family pool CIDR, including ranges outside the pool, larger than its blocks, or spanning adjacent blocks, **When** the caller allocates, **Then** the call fails with `ErrInvalidCIDR` and does not reserve space.
  - Evidence: UNTESTED (public error mapping in `allocation.go` `allocateAttempt` and `mapAllocatorError`; containment checks in `internal/alloc/alloc.go` `CheckExplicit`)
- **AC-19**: **Given** a new allocation ID and an explicit CIDR wholly contained in one pool CIDR but intersecting a live allocation, whether equal to it, contained by it, or containing it, **When** the caller allocates, **Then** the call fails and does not reserve another allocation; see BASE-0006 error-contract AC-24 for the reason string and sentinel identity.
  - Evidence: UNTESTED (public conflict mapping in `allocation.go` `mapAllocatorError` and overlap checks in `internal/alloc/alloc.go` `CheckExplicit`)
- **AC-20**: **Given** an unused allocation ID and a prefix length that fits the pool's dimensions but no sufficiently large free aligned CIDR, even before considering cooldown, **When** the caller allocates by length, **Then** the call fails with `ErrPoolExhausted` and does not reserve space, including when fragmentation prevents a fit despite sufficient aggregate free addresses.
  - Evidence: UNTESTED (public exhaustion mapping in `allocation.go` `mapAllocatorError` and availability checks in `internal/alloc/alloc.go` `Select`)
- **AC-21**: **Given** an allocation ID in the reserved `pool:` namespace, **When** a caller uses it with public `Allocate` or `UpdateAllocationMetadata`, **Then** the call fails with `ErrInvalidArgument`; these APIs do not create or edit carve-outs.
  - Evidence: UNTESTED (behaviour in `allocation.go` `allocateWithStatus` and `UpdateAllocationMetadata`)
- **AC-22**: **Given** a carve-out whose child still has either a pool document or a reservations document with the carve-out's incarnation, **When** a caller attempts to release the carve-out, **Then** release fails; see BASE-0006 error-contract AC-22 for the reason string and sentinel identity.
  - Evidence: UNTESTED (behaviour in `allocation.go` `release` and `childIncarnationExists`; child ownership is defined in BASE-0001 pool-lifecycle)
- **AC-23**: **Given** a carve-out in a usable parent pool, no child creation still running, and no child pool document or reservations document with that carve-out's incarnation, **When** an operator releases the reserved allocation ID, **Then** release removes the orphaned carve-out, including when remaining child documents belong to a different incarnation.
  - Evidence: UNTESTED (behaviour and documented recovery precondition in `allocation.go` `Release`, `release`, and `childIncarnationExists`)
- **AC-24**: **Given** a usable pool without the specified ordinary allocation ID, **When** a caller uses `UpdateAllocationMetadata`, **Then** the call fails with `ErrNotFound` rather than creating an allocation.
  - Evidence: UNTESTED (behaviour in `allocation.go` `UpdateAllocationMetadata`)
- **AC-25**: **Given** valid identifiers for a nonexistent pool and an ordinary allocation, **When** a caller allocates, retrieves, lists, releases, or updates allocation metadata in that pool, **Then** the operation fails with `ErrNotFound`; absence of the pool is not treated as an already completed release.
  - Evidence: UNTESTED (pool reads in `allocation.go` `readPoolState` and its allocation API callers; sentinel semantics are defined in BASE-0006 error-contract)
- **AC-26**: **Given** an ordinary allocation with existing metadata, **When** a caller updates it with an empty description and nil or empty labels, **Then** its metadata is cleared; supplying a new labels map replaces the old labels rather than merging them.
  - Evidence: UNTESTED (behaviour in `allocation.go` `UpdateAllocationMetadata`)
- **AC-27**: **Given** two usable pools with sufficient available space, **When** callers allocate with the same supplied allocation ID in both pools, **Then** each pool can contain its own allocation under that ID; idempotency and ID-reuse conflicts are scoped to a pool.
  - Evidence: UNTESTED (pool-scoped lookup in `allocation.go` `allocateAttempt`)
- **AC-28**: **Given** a carve-out recorded as an allocation in a parent pool, **When** a caller retrieves its reserved ID or lists the parent's allocations, **Then** the carve-out is visible as an allocation rather than being filtered out.
  - Evidence: UNTESTED (behaviour in `allocation.go` `GetAllocation` and `ListAllocations`; reserved ID validation is defined in BASE-0005 input-validation)
- **AC-29**: **Given** a usable pool and a valid ordinary allocation ID that has never been allocated in it, **When** a caller uses `GetAllocation` with that pool and ID, **Then** the call fails with `ErrNotFound`.
  - Evidence: UNTESTED (behaviour in `allocation.go` `GetAllocation`)
- **AC-30**: **Given** labels supplied to successful `Allocate` or `UpdateAllocationMetadata` calls and allocation snapshots returned by `Allocate`, `GetAllocation`, or `ListAllocations`, **When** a caller subsequently changes a supplied labels map or one snapshot's labels, **Then** the saved labels and labels in other snapshots remain unchanged.
  - Evidence: UNTESTED (behaviour in `allocation.go` `allocateAttempt`, `UpdateAllocationMetadata`, `GetAllocation`, `ListAllocations`, and `cloneAllocation`)
- **AC-31**: **Given** an unused allocation ID and an available explicit CIDR wholly contained within one same-family pool CIDR, **When** a caller first allocates with that ID and explicit CIDR, **Then** the call succeeds and returns that exact canonical CIDR, for both IPv4 and IPv6.
  - Evidence: IPv4: `client_test.go` `TestAllocateExplicitCanonicalizesBeforeIdempotency`; IPv6: UNTESTED (client-level success and exact canonical result in `allocation.go` `Allocate` and `allocateAttempt`; internal acceptance only is covered by `internal/alloc/explicit_test.go` `TestExplicit/accepts IPv6 in a matching parent`)

## Non-functional requirements *(mandatory)*

- None enforced in code.

Address-width boundaries are functional criteria above. Retry, cancellation, and contention limits belong to BASE-0004 concurrency-and-retry.

## Key entities *(include if the feature involves data)*

- **AllocationSpec**: Caller input with an optional allocation ID, either a prefix length or an explicit CIDR, a description, and labels. A zero prefix length is an actual `/0` request when no explicit CIDR is supplied.
- **Request**: The recorded network request. `RequestNext` records a prefix length; `RequestExplicit` records the canonical explicit CIDR. Request equality, rather than metadata equality, determines idempotency for an existing allocation ID.
- **Allocation**: One assigned CIDR, identified within a pool by its ID, with its pool ID, original request, description, labels, and creation time.
- **Carve-out**: See BASE-0007 document-format Key entities for the shared definition. Creation and ownership belong to BASE-0001 pool-lifecycle; the allocation APIs expose it for inspection and guarded recovery.

## Out of scope *(mandatory)*

- Pool creation, deletion, child relationships, usable pool state, incarnation pairing, and residue handling: see BASE-0001 pool-lifecycle.
- Choosing lowest-address or best-fit placement, release cooldown records and expiry, explicit-request cooldown bypass, and policy updates: see BASE-0002 allocation-policy.
- CAS coordination, retry and backoff settings, attempt exhaustion, cancellation during retries, generated-ID stability across retries, and recovery after an unknown write outcome: see BASE-0004 concurrency-and-retry.
- Pool and allocation ID grammar, UUIDv7 generation rules, and CIDR normalization: see BASE-0005 input-validation. No description or label validation rules exist yet; see BASE-0005 input-validation OQ-1 for the maintainer's answer. This spec records allocation-specific selector and pool-containment behavior.
- Sentinel identities, allocation-specific failure reasons, operation error structure and formatting, and safe error text: see BASE-0006 error-contract. This spec states the conditions and outcomes of allocation failures and references the error contract for their reason strings and sentinel identities.
- Storage keys, document schemas, unsupported-schema rejection, and JSON compatibility: see BASE-0007 document-format.
- Pool utilisation and fragmentation reporting: see BASE-0008 pool-stats. This capability allocates CIDRs, not individual usable-host lists or routing configuration.
- CLI flags, command output, and process exit codes: see BASE-0014 cli.

## Open questions *(mandatory, empty before G2)*

- **OQ-1**: See BASE-0004 concurrency-and-retry OQ-1: `GetAllocation` and `ListAllocations` skip client validation; the maintainer answered that this is a bug (2026-11-03). Remove this pointer when that Bug is raised.
- **OQ-2**: See BASE-0005 input-validation OQ-1 for the absence of description and label constraints and the maintainer's answer that metadata should be limited; a Bug or Story is to be raised after merge.
