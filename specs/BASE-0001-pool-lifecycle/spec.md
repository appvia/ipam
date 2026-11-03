# Feature Specification: Pool lifecycle

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0001-pool-lifecycle/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Manage a root pool (Priority: P1)

As a library caller, I create a named pool from IPv4 or IPv6 CIDRs, retrieve or list it, update its description and labels, and delete it once it has no live allocations.

**Why this priority**: A usable pool is the starting point for managing address space.

### Journey 2 - Delegate address space to a child pool (Priority: P2)

As a library caller, I create a child pool by requesting a prefix length or an explicit CIDR from a parent. I can identify the child's carve-out, and deleting the empty child releases that carve-out so the parent can subsequently be deleted if otherwise empty.

**Why this priority**: Child pools let callers divide an existing pool without making the same space available to unrelated allocations.

### Journey 3 - Recover an interrupted lifecycle operation (Priority: P3)

As a library caller recovering from an interrupted create or delete, I distinguish a usable pool from leftover documents, clear removable residue with `DeletePool`, and retry creation. I do not assume that deleting a child ID discovers carve-outs whose child documents have already disappeared.

**Why this priority**: Interrupted operations can leave address space reserved or pool documents incomplete; recovery must preserve existing reservations.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** an unused valid pool ID and valid root CIDRs from one address family, **When** a caller invokes `CreatePool`, **Then** the returned pool has that ID, canonical network CIDRs, and an inferred family of `4` for IPv4 or `6` for IPv6.
  - Evidence: `client_test.go` `TestClientPoolAndAllocationAPI`; `example_test.go` `ExampleOpen`, `ExampleWithMaxAttempts`

- **AC-2**: **Given** a successfully created pool, **When** a caller invokes `GetPool` with its ID, **Then** the call returns the saved pool ID and description.
  - Evidence: `client_test.go` `TestClientPoolAndAllocationAPI`

- **AC-3**: **Given** readable pools in storage, **When** a caller invokes `ListPools`, **Then** the readable results are returned in ascending pool-ID order.
  - Evidence: `listpools_test.go` `TestListPoolsSkipsUnsupportedSchemaPools`

- **AC-4**: **Given** a new valid pool specification with a description and labels, **When** creation succeeds and the caller retrieves the pool, **Then** the description and label entries supplied at creation are retained.
  - Evidence: UNTESTED (behaviour in `pool.go` `createPoolAttempt`, `GetPool`)

- **AC-5**: **Given** a usable parent with space satisfying a prefix-length request, **When** a caller creates a child using `ParentPool` and that request, **Then** the child has one CIDR of the requested prefix length and a parent reference naming the parent pool and its `pool:<child>:<incarnation>` carve-out.
  - Evidence: `client_lifecycle_test.go` `TestChildPoolLifecycleAndParentDeleteGuard`; `example_test.go` `ExampleClient_CreatePool_child`

- **AC-6**: **Given** a usable parent in which an explicit CIDR can be reserved, **When** a caller creates a child requesting that CIDR, **Then** the child's sole CIDR is the canonical requested range and its parent reference identifies the carve-out of that range.
  - Evidence: UNTESTED (behaviour in `pool.go` `validatePoolSpec`, `createPoolAttempt`)

- **AC-7**: **Given** a complete pool matching the requested CIDRs or child request, parent, description and labels, with policy matching as defined by BASE-0002 allocation-policy, **When** a caller repeats `CreatePool`, **Then** it returns the existing pool without writing pool documents or reserving another child range.
  - Evidence: UNTESTED (behaviour in `pool.go` `createPoolAttempt`, `poolMatchesSpec`)

- **AC-8**: Withdrawn (covered by BASE-0004 concurrency-and-retry AC-3)

- **AC-9**: **Given** an existing pool, **When** a caller successfully invokes `UpdatePoolMetadata` with a new description and labels, **Then** a subsequent `GetPool` returns the new description and supplied label values.
  - Evidence: `client_test.go` `TestClientPoolAndAllocationAPI`

- **AC-10**: **Given** a complete pool whose allocations have all been released, **When** a caller invokes `DeletePool`, **Then** deletion succeeds and a subsequent `GetPool` fails with `ErrNotFound`.
  - Evidence: `client_test.go` `TestClientPoolAndAllocationAPI`

- **AC-11**: **Given** an empty child pool and a parent whose only allocation is that child's carve-out, **When** the caller deletes the child, **Then** that carve-out is removed and the parent can subsequently be deleted.
  - Evidence: `client_lifecycle_test.go` `TestChildPoolLifecycleAndParentDeleteGuard`

### Edge cases

- **AC-12**: **Given** a root pool specification without `ParentPool` but with a request kind, a valid request CIDR or a nonzero request prefix length, **When** a caller invokes `CreatePool`, **Then** the call fails with `ErrInvalidArgument`.
  - Evidence: UNTESTED (behaviour in `pool.go` `validatePoolSpec`)

- **AC-13**: **Given** a child specification that names itself as parent, also supplies root CIDRs, or supplies an invalid or inconsistent child request, **When** a caller invokes `CreatePool`, **Then** the call fails with `ErrInvalidArgument`.
  - Evidence: UNTESTED (behaviour in `pool.go` `validatePoolSpec`)

- **AC-14**: **Given** that reserving a requested child range fails, **When** `CreatePool` returns that failure, **Then** creation fails and preserves the underlying allocation error; the error context, reason and sentinel identity are defined by BASE-0006 error-contract AC-31 and AC-32.
  - Evidence: UNTESTED (behaviour in `pool.go` `createPoolAttempt`)

- **AC-15**: **Given** otherwise decodable documents where the pool document or reservations document is absent, or where their incarnations differ, **When** a caller invokes `GetPool`, **Then** the call fails with `ErrNotFound` rather than returning a usable pool.
  - Evidence: UNTESTED (behaviour in `pool.go` `GetPool`)

- **AC-16**: **Given** listing entries that do not identify valid pool documents, or pools whose documents are absent or decodable but form incomplete incarnation pairs when read, **When** a caller invokes `ListPools`, **Then** those entries are omitted; if no readable or unsupported-schema pools remain, the call returns an empty result without error.
  - Evidence: UNTESTED (behaviour in `pool.go` `ListPools`, `idFromKey`)

- **AC-17**: **Given** readable pools alongside pools whose documents use unsupported schema versions, **When** a caller invokes `ListPools`, **Then** it returns the readable pools together with a non-nil `UnsupportedPoolsError` naming the skipped pool IDs and matching `ErrUnsupportedSchema`.
  - Evidence: `listpools_test.go` `TestListPoolsSkipsUnsupportedSchemaPools`

- **AC-18**: **Given** a malformed pool document rather than an unsupported schema version, **When** a caller invokes `ListPools`, **Then** listing fails with a non-schema error and returns a nil pool result rather than valid partial results.
  - Evidence: `listpools_test.go` `TestListPoolsAbortsOnInvalidDocument`

- **AC-19**: **Given** a backend list failure or a read or decode failure for a pool document or reservations document other than a missing document, **When** a caller invokes `GetPool` or `ListPools` and the failure occurs during that read, **Then** the call reports the error rather than treating the failed read as an absent pool; listing's unsupported-schema exception is defined in AC-17.
  - Evidence: UNTESTED (behaviour in `pool.go` `readPoolDocuments`, `GetPool`, `ListPools`)

- **AC-20**: **Given** a reservations document without a pool document, or a pool document that matches the creation specification but lacks a matching-incarnation reservations document, **When** `CreatePool` encounters that residue, **Then** it leaves the residue unmodified; the caller recovers by using `DeletePool` to clear removable residue and retrying creation. Retry and exhaustion outcomes belong to BASE-0004 concurrency-and-retry AC-17; the `pool_incomplete` reason and `ErrConflict` identity are defined in BASE-0006 error-contract AC-26.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolIncompleteStatesRetryThenReportWithoutWrites/pool document without reservations`, `TestCreatePoolIncompleteStatesRetryThenReportWithoutWrites/reservations without pool document`, `TestCreatePoolIncompleteStatesRetryThenReportWithoutWrites/mismatched incarnations`

- **AC-21**: **Given** a pool with existing description and labels, **When** a caller successfully invokes `UpdatePoolMetadata`, **Then** the supplied description and label set replace the old values rather than merge with them; an empty description and nil or empty labels clear those values, while ID, family, CIDRs, parent reference and policy remain unchanged.
  - Evidence: UNTESTED (behaviour in `pool.go` `UpdatePoolMetadata`)

- **AC-22**: Withdrawn (pending the product owner's decision in OQ-2 on metadata updates to unusable pool residue).

- **AC-23**: **Given** a usable pool with a live allocation, including a child's carve-out, **When** a caller invokes `DeletePool`, **Then** deletion is refused; the error reason and sentinel identity are defined by BASE-0006 error-contract AC-21.
  - Evidence: `client_lifecycle_test.go` `TestChildPoolLifecycleAndParentDeleteGuard`

- **AC-24**: **Given** a pool ID for which neither the pool document nor the reservations document exists, **When** a caller invokes `DeletePool`, **Then** the call fails with `ErrNotFound`.
  - Evidence: `client_attempt4_test.go` `TestDeletePoolCleansEveryResidueShape/nothing`

- **AC-25**: **Given** an interrupted child deletion that removed its reservations document but left its pool document, **When** the caller retries `DeletePool`, **Then** the remaining pool document and its carve-out are removed and the child is no longer retrievable.
  - Evidence: `client_lifecycle_test.go` `TestDeletePoolCrashPointsConverge/after step 3 reservations delete`

- **AC-26**: **Given** an empty reservations document without its pool document, **When** a caller invokes `DeletePool` without concurrent changes, **Then** deletion succeeds and removes that residue.
  - Evidence: `client_attempt4_test.go` `TestDeletePoolCleansEveryResidueShape/c empty reservations without pool document`

- **AC-27**: **Given** a pool document and an empty reservations document carrying different incarnations, **When** a caller invokes `DeletePool` without concurrent changes, **Then** deletion clears both documents rather than leaving the mismatched pair in place.
  - Evidence: `client_attempt4_test.go` `TestDeletePoolCleansEveryResidueShape/b pool document with foreign reservations`

- **AC-28**: **Given** a non-empty reservations document without a matching-incarnation pool document, **When** a caller invokes `DeletePool`, **Then** it refuses to discard that document; the error reason and sentinel identity are defined by BASE-0006 error-contract AC-21.
  - Evidence: UNTESTED (behaviour in `pool.go` `deletePoolAttempt`)

- **AC-29**: **Given** a complete pool with no live allocations but with cooldown release records, **When** a caller invokes `DeletePool`, **Then** those release records do not make the pool non-empty and do not by themselves prevent deletion.
  - Evidence: UNTESTED (behaviour in `pool.go` `deletePoolAttempt`)

- **AC-30**: **Given** child creation that returned an error while creating the child's reservations document, **When** a caller retries the same child request and creation succeeds, **Then** the returned child's carve-out exists and reserves the same CIDR as the child.
  - Evidence: `client_lifecycle_test.go` `TestChildCreateCrashThenRetryConverges`

- **AC-31**: **Given** child deletion that returned an error after removing only the child's reservations document, or after removing its carve-out, **When** a caller retries deletion without intervening creation, **Then** the retry succeeds or reports `ErrNotFound`, and retrieving the child reports `ErrNotFound`.
  - Evidence: `client_lifecycle_test.go` `TestDeletePoolCrashPointsConverge/after step 3 reservations delete`, `TestDeletePoolCrashPointsConverge/after step 5 parent release`

- **AC-32**: **Given** a usable pool and an otherwise valid repeated creation specification that changes its root CIDRs, parent pool or requested child prefix length, **When** a caller invokes `CreatePool` with that specification, **Then** creation fails without writing pool documents or reserving another parent range; the error reason and sentinel identity are defined by BASE-0006 error-contract AC-20.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolExistingDifferentSpecWritesNothing`

- **AC-33**: **Given** a usable pool and an otherwise valid repeated creation specification that changes its description, labels or requested explicit child CIDR, **When** a caller invokes `CreatePool` with that specification, **Then** creation fails without writing pool documents or reserving another parent range; the error reason and sentinel identity are defined by BASE-0006 error-contract AC-20. Policy mismatch rules belong to BASE-0002 allocation-policy.
  - Evidence: UNTESTED (behaviour in `pool.go` `poolMatchesSpec`, `createPoolAttempt`)

- **AC-34**: **Given** a valid pool ID whose pool document is absent, whether or not a reservations document exists, **When** a caller invokes `UpdatePoolMetadata`, **Then** the call fails with `ErrNotFound` without creating a pool or changing any reservations document left as residue.
  - Evidence: UNTESTED (behaviour in `pool.go` `UpdatePoolMetadata`; `client.go` `mapStorageError`)

- **AC-35**: **Given** child creation that has newly reserved a range from its parent, **When** publishing the child fails with a storage or encoding error and cleanup succeeds, **Then** `CreatePool` returns an error preserving the original publication failure and removes that call's new carve-out only when no document from that call's incarnation remains; otherwise, release is refused as `pool_in_use`, the carve-out stays, and only the publication error is returned. Losing the creation race to another incarnation's document is retryable; see BASE-0004 concurrency-and-retry AC-51. Cooldown semantics belong to BASE-0002 allocation-policy.
  - Evidence: `cooldown_test.go` `TestCooldownChildRollbackDoesNotRecordRelease`

- **AC-36**: **Given** a usable parent with space satisfying a prefix-length request and an otherwise valid child specification with no valid request CIDR and an omitted `Request.Kind`, **When** a caller invokes `CreatePool`, **Then** creation succeeds using `RequestNext` and the child's sole CIDR has the requested prefix length.
  - Evidence: UNTESTED (behaviour in `pool.go` `validatePoolSpec`, `createPoolAttempt`; `allocation.go` `canonicalAllocationRequest`)

- **AC-37**: **Given** a usable parent in which an explicit CIDR can be reserved and an otherwise valid child specification with that valid request CIDR, zero request prefix length and an omitted `Request.Kind`, **When** a caller invokes `CreatePool`, **Then** creation succeeds using `RequestExplicit` and the child's sole CIDR is the canonical requested range.
  - Evidence: UNTESTED (behaviour in `pool.go` `validatePoolSpec`, `createPoolAttempt`; `allocation.go` `canonicalAllocationRequest`)

- **AC-38**: **Given** a new child specification with a description and labels, **When** creation succeeds and the caller retrieves the carve-out identified by the returned child's parent reference using `GetAllocation` or `ListAllocations`, **Then** that carve-out carries the description and label entries supplied in the creation specification.
  - Evidence: UNTESTED (behaviour in `pool.go` `createPoolAttempt`; `allocation.go` `allocateAttempt`)

## Non-functional requirements *(mandatory)*

- None enforced in code.

Retry budgets and backoff limits are specified by BASE-0004 concurrency-and-retry. Input limits are specified by BASE-0005 input-validation, and policy limits are specified by BASE-0002 allocation-policy.

## Key entities *(include if the feature involves data)*

- **Pool**: A named, single-family collection of disjoint CIDRs, with an inferred address family, optional parent reference, description, labels and concrete allocation policy. A root obtains its ranges directly from its creation specification; a child obtains one range reserved from its parent.
- **Pool creation specification**: A pool ID, description, labels and optional policy, together with either root CIDRs or a parent pool ID and a request. Pool policy defaults and matching semantics belong to BASE-0002 allocation-policy.
- **Child request**: A request for the next suitable range of a prefix length or for an explicit CIDR. Placement and reservation eligibility belong to BASE-0003 allocations and BASE-0002 allocation-policy.
- **Parent reference**: The parent pool ID and the allocation ID of the child's carve-out. The carve-out identifier format is defined by BASE-0007 document-format.
- **Pool incarnation and residue**: A creation incarnation pairs a pool document with its reservations document. A missing document or unequal incarnations makes the pair unusable. Interrupted operations can leave documents requiring deletion, or a carve-out whose child documents no longer exist. Document and carve-out terminology and the stored representation belong to BASE-0007 document-format.

## Out of scope *(mandatory)*

- Allocation strategy, cooldown configuration, default policy, policy matching on repeated creates, and `UpdatePoolPolicy`: see BASE-0002 allocation-policy.
- General allocation, release, allocation metadata and allocation-ID behaviour: see BASE-0003 allocations. Child creation and deletion use those operations, but this spec does not redefine their selection or release rules.
- CAS coordination, retry/backoff settings, unknown-outcome recovery and stale-writer protection across deletion and recreation: see BASE-0004 concurrency-and-retry.
- Pool-ID grammar, CIDR canonicalisation rules and other shared input validation: see BASE-0005 input-validation.
- Error reasons, sentinel identity, error rendering and diagnostic fields: see BASE-0006 error-contract. This spec states the lifecycle-specific conditions and outcomes callers encounter.
- Storage key layout, JSON schema versions and document validation: see BASE-0007 document-format.
- Pool statistics, backend configuration and CLI commands: see BASE-0008 pool-stats, BASE-0009 backend-url, BASE-0010 storage-contract, BASE-0011 local-backend, BASE-0012 s3-backend, BASE-0013 azblob-backend, BASE-0014 cli and BASE-0015 cli-doctor.
- Resizing pools, changing a pool's parent or recursively deleting allocated pools. The lifecycle metadata operation changes only the description and labels.
- Automatically finding and releasing abandoned carve-outs by scanning other pools. A process stopping after a carve-out is committed can leave its range reserved. (Losing the reference needed to release a carve-out during a failed deletion is a maintainer-confirmed bug; see OQ-4.) Explicit release by its complete carve-out ID is covered by BASE-0003 allocations and is safe only when no creation for that child can still be running (`doc.go`).

## Open questions *(mandatory, empty before G2)*

- **OQ-1**: `CreatePool` reports `pool_exists` for interrupted-operation residue when the surviving pool document conflicts with the requested specification. Maintainer answer (2026-11-03): this is a bug. `CreatePool` should report `pool_incomplete` for interrupted-operation residue even when the surviving pool document conflicts with the requested specification. A Bug citing this spec is to be raised after merge, and this entry is then removed.
- **OQ-2**: `pool.go` `UpdatePoolMetadata` reads and writes only the pool document, so it can succeed for a pool that `GetPool` rejects as incomplete or incarnation-mismatched without repairing that pair. AC-22 is withdrawn. Maintainer answer (2026-11-03, baseline-spec run): this is a bug; metadata updates should require a usable pair. A Bug citing this spec is to be raised after merge, and this entry is then removed.
- **OQ-4**: A carve-out release failure after the child's documents have been deleted leaves the carve-out reserved, and another `DeletePool` reports `ErrNotFound` without releasing it because the reference is gone. Maintainer answer (2026-11-03): this is a bug. `DeletePool` must not leave an unreleasable carve-out when the carve-out release fails after the child's documents are deleted. A Bug citing this spec is to be raised after merge, and this entry is then removed.
