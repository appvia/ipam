# Feature Specification: Allocation policy

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0002-allocation-policy/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Choose where new allocations are placed (Priority: P1)

A library caller creates a pool with an allocation strategy, or accepts the
best-fit default, and requests allocations by prefix length. Best-fit uses the
smallest available block that accommodates the request; lowest uses the lowest
available address. The caller can inspect the concrete policy returned with the
pool.

**Why this priority**: Placement policy determines which free range each ordinary
allocation consumes.

### Journey 2 - Control reuse of released address space (Priority: P2)

A library caller configures a cooldown duration and chooses strict exclusion or
fit-aware avoidance of recently released ranges. Strict mode can report when a
fitting range becomes available. Explicit CIDR requests can reuse cooling space
without waiting, subject to the allocation checks in BASE-0003 allocations.

**Why this priority**: Callers can delay reuse without changing their pool's
address ranges, while fit-aware mode permits reuse when avoiding it would change
the fit of the selected free block.

### Journey 3 - Change an existing pool's policy (Priority: P2)

A library caller invokes `UpdatePoolPolicy` with an explicit strategy and the
desired cooldown settings. The caller can enable, change or disable cooldown,
and can repeat an interrupted update to complete a partially applied policy.

**Why this priority**: An existing pool can change placement and reuse policy
without replacing the pool or its live allocations.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a new pool with an unset allocation strategy and no cooldown, **When** a caller creates it and subsequently reads it, **Then** the returned policy is `best-fit` with cooldown disabled.
  - Evidence: `policy_test.go` `TestPolicyDefaultCreateStoresBestFitV2PoolAndV1Reservations`

- **AC-2**: **Given** a new pool with an explicit supported strategy and cooldown, **When** a caller creates and reads the pool, **Then** both returned policies contain the supplied strategy, cooldown mode and duration.
  - Evidence: `policy_test.go` `TestPolicyCreateWithCooldownStoresV2Reservations`

- **AC-3**: **Given** an existing pool whose other creation settings match, **When** a caller repeats creation with an unset strategy or unset cooldown, **Then** each unset field matches the stored value, the stored concrete policy is returned, and no document is written.
  - Evidence: `policy_test.go` `TestPolicyIdenticalCreateOnV1PoolDoesNotWrite` / `unset policy`, `explicit lowest`; `TestPolicyCreateMatchesUnsetFieldsAgainstStoredCooldown` / `unset policy`, `strategy only`, `cooldown only`, `full policy match`

- **AC-4**: **Given** an existing pool whose other creation settings match, **When** repeated creation specifies a different strategy or a different enabled cooldown, **Then** the call fails rather than changing the stored policy; changing strategy or adding cooldown to the existing no-cooldown pool performs no writes. The error reason and sentinel are specified in BASE-0006 error-contract AC-20.
  - Evidence: `policy_test.go` `TestPolicyDifferentCreateOnExistingPoolIsPoolExists` / `strict cooldown`, `best-fit`; `TestPolicyCreateMatchesUnsetFieldsAgainstStoredCooldown`

- **AC-5**: **Given** a pool stored in the pre-policy v1 document format, **When** a caller gets or lists it, **Then** its concrete policy is `lowest` with cooldown disabled, and these reads perform no writes. The document formats themselves are specified in BASE-0007 document-format.
  - Evidence: `policy_test.go` `TestPolicyGetPoolOnV1DocumentsReturnsLowestWithoutCooldown`

- **AC-6**: **Given** a parent pool with a non-default policy, **When** a caller creates a child without an explicit child policy, **Then** the child's own policy is `best-fit` with cooldown disabled, not the parent's policy.
  - Evidence: `policy_test.go` `TestPolicyChildDoesNotInheritParentPolicy`

- **AC-7**: **Given** multiple free blocks that fit a prefix-length request, **When** selection uses `best-fit` without cooldown restrictions, **Then** it ranks free blocks within each configured pool CIDR independently, without merging adjacent configured CIDRs, selects the smallest free CIDR block that fits across those candidates and places the allocation at that block's lowest address, breaking equal-size ties by lowest address. For an empty pool containing `10.0.0.0/24`, `10.0.2.0/25` and `10.0.2.128/25`, a `/26` request returns `10.0.2.0/26`.
  - Evidence: `cooldown_test.go` `TestAllocationStrategyChoosesTightHole` / `best-fit`; `internal/alloc/select_test.go` `TestSelect` / `best fit chooses the tightest hole`, `best fit ranks blocks across parents`, `best fit breaks ties by address`; UNTESTED for the distinguishing adjacent-CIDR ranking example (behaviour in `internal/alloc/alloc.go` `Select`)

- **AC-8**: **Given** a low-address free block and a smaller higher-address free block that both fit, **When** selection uses `lowest` without cooldown restrictions, **Then** the allocation starts at the lowest available address rather than preferring the smaller block.
  - Evidence: `cooldown_test.go` `TestAllocationStrategyChoosesTightHole` / `lowest`; `internal/alloc/select_test.go` `TestSelect` / `lowest chooses the lowest address`

- **AC-9**: **Given** free space containing cooling ranges and a fitting range outside them, **When** a caller requests an allocation by prefix length under `strict` cooldown, **Then** selection excludes the cooling ranges and applies the chosen strategy to the remaining space, including smaller free pieces exposed by that exclusion.
  - Evidence: `internal/alloc/select_test.go` `TestSelect` / `strict chooses non cooling space`, `strict best fit ranks newly split blocks`

- **AC-10**: **Given** `fit-aware` cooldown and a prefix-length request, **When** a non-cooling candidate fits inside a free block whose size before excluding cooling space equals the block selected without cooldown, **Then** selection prefers such non-cooling space and applies the configured strategy to the remaining non-cooling pieces.
  - Evidence: `internal/alloc/select_test.go` `TestSelect` / `fit aware chooses equally tight clean space`, `fit aware ranks clean pieces by their new size`

- **AC-11**: **Given** `fit-aware` cooldown and no fitting non-cooling candidate from a free block of the same original size as the ordinary selection, **When** a caller requests an allocation by prefix length, **Then** selection keeps the choice it would make without cooldown, even if that choice is cooling and a differently sized non-cooling block exists, or all fitting space is cooling.
  - Evidence: `internal/alloc/select_test.go` `TestSelect` / `fit aware keeps cooling choice over looser clean space`, `fit aware lowest requires equal original block size`, `fit aware falls back when all space is cooling`; `cooldown_test.go` `TestCooldownFitAwareReusesOnlyFittingSpace`

- **AC-12**: **Given** cooldown is disabled, **When** selection encounters otherwise free ranges with release records, **Then** those ranges remain eligible for immediate selection.
  - Evidence: `internal/alloc/select_test.go` `TestSelect` / `none ignores valid cooling prefixes`

- **AC-13**: **Given** a live allocation in a pool with cooldown enabled and no later release expiry for the same CIDR, **When** the caller releases it, **Then** the released CIDR has a cooldown expiry equal to the releasing client's current time plus the configured duration.
  - Evidence: `cooldown_test.go` `TestCooldownStrictRefusesUntilExpiryAndPrunes`

- **AC-14**: **Given** a release record already exists for a CIDR being released, **When** the caller releases that live allocation under an enabled cooldown, **Then** its expiry is the later of the existing expiry and the releasing client's current time plus the configured duration.
  - Evidence: `cooldown_test.go` `TestCooldownReleaseKeepsLaterExistingExpiry` / `extend`, `preserve`

- **AC-15**: **Given** a release write succeeded but its response was lost, **When** release recovers and the caller later repeats release for the same allocation ID, **Then** the landed release record is retained without extending its expiry or writing the release a second time.
  - Evidence: `cooldown_test.go` `TestCooldownReleaseUnknownOutcomeKeepsLandedRecord`

- **AC-16**: **Given** release records expiring at or before the client's current time, **When** an allocation, release, allocation metadata update, child creation or child deletion writes that pool's reservations document, **Then** expired records are removed, whether cooldown is disabled or strict.
  - Evidence: `cooldown_test.go` `TestCooldownEveryReservationsWritePrunesExpiredRecords` / `allocate/`, `allocate/strict`, `release/`, `release/strict`, `metadata/`, `metadata/strict`, `child create/`, `child create/strict`, `child delete/`, `child delete/strict`

- **AC-17**: **Given** a released range is the only fitting space and remains in strict cooldown, **When** a caller requests it by prefix length, **Then** allocation fails; after its expiry has passed, the range can be allocated again. The error reason and sentinel are specified in BASE-0006 error-contract AC-25.
  - Evidence: `cooldown_test.go` `TestCooldownStrictRefusesUntilExpiryAndPrunes`

- **AC-18**: **Given** an otherwise available CIDR is in active strict cooldown, **When** a caller requests that explicit CIDR, **Then** cooldown does not prevent allocation and its unexpired release record is preserved. Containment and live-allocation overlap checks remain owned by BASE-0003 allocations.
  - Evidence: `cooldown_test.go` `TestCooldownExplicitIgnoresActiveReleaseAndReplayWins`

- **AC-19**: **Given** an allocation already exists for the supplied ID and matching request, with no additional fitting space outside strict cooldown, **When** the caller repeats that allocation request, **Then** the original allocation is returned without a cooldown failure. General allocation replay rules are specified in BASE-0003 allocations.
  - Evidence: `cooldown_test.go` `TestCooldownExplicitIgnoresActiveReleaseAndReplayWins`

- **AC-20**: **Given** a parent pool with strict cooldown, **When** a child using the parent's only fitting range is deleted and another child requests that range by prefix length before expiry, **Then** the parent's policy prevents reuse. The error context, reason and sentinel are specified in BASE-0006 error-contract AC-31.
  - Evidence: `cooldown_test.go` `TestCooldownChildCreateUsesParentPolicy`

- **AC-21**: **Given** child creation fails after acquiring its parent carve-out, **When** rollback of the carve-out completes, **Then** rollback does not create a cooldown record and the range remains immediately available if child pool publication is known not to have occurred; if publication may have occurred, including an unknown write outcome followed by a not-found read, the rollback instead applies the parent's cooldown.
  - Evidence: `cooldown_test.go` `TestCooldownChildRollbackDoesNotRecordRelease` / `reservations/child.json`, `pools/child.json` (definite publication failures only); UNTESTED for rollback after possible publication (behaviour in `pool.go` `createOwnDocument`, `publishPool`, `releaseOwnCarveOut`; `allocation.go` `release`)

- **AC-22**: **Given** an existing pool and a valid replacement policy, **When** `UpdatePoolPolicy` succeeds, **Then** a subsequent pool read returns the requested strategy and cooldown, including a change from `lowest` without cooldown to `best-fit` with strict cooldown. Stored format selection follows BASE-0007 document-format.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyUpgradeAndDowngradeRewriteDocuments`

- **AC-23**: **Given** a pool has enabled cooldown and release records, **When** `UpdatePoolPolicy` changes cooldown to disabled, **Then** every release record is cleared and the returned pool policy has cooldown disabled.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyUpgradeAndDowngradeRewriteDocuments`

- **AC-24**: **Given** an enabled cooldown with existing unexpired release records, **When** `UpdatePoolPolicy` changes only the cooldown duration, **Then** the new duration is reported in the pool policy and the existing records retain their original absolute expiry times.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyDurationChangeKeepsRecordExpiry`

- **AC-25**: **Given** the requested policy has already converged, **When** the caller repeats `UpdatePoolPolicy`, **Then** the call succeeds without rewriting the pool document.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyRetryCompletesPartialUpdate`

- **AC-26**: Withdrawn (covered by BASE-0004 concurrency-and-retry AC-39, AC-40 and AC-48)

- **AC-27**: **Given** a pool with existing CIDRs, parent reference, metadata and live allocations, **When** `UpdatePoolPolicy` changes its policy, **Then** those non-policy values are preserved.
  - Evidence: UNTESTED (behaviour in `pool.go` `UpdatePoolPolicy`, `updatePoolStrategy`, `updatePoolCooldown`)

### Edge cases

- **AC-28**: **Given** a create request with an unknown strategy or cooldown mode, an enabled mode without a valid duration, a duration without a mode, fractional seconds, or a duration outside the permitted range, **When** the caller creates the pool, **Then** creation fails with `ErrInvalidArgument` before performing any writes.
  - Evidence: `policy_test.go` `TestPolicyCreateRejectsInvalidPolicies` / `mode without duration`, `duration without mode`, `fractional seconds`, `above 8760h`, `below 1s`, `unknown strategy`, `unknown mode`

- **AC-29**: **Given** an existing pool, **When** the caller invokes `UpdatePoolPolicy` with an unset strategy, **Then** the call fails with `ErrInvalidArgument` and does not change the pool document; unlike creation, update requires an explicit strategy.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyRequiresExplicitStrategy`

- **AC-30**: **Given** an update request with an unknown strategy, unknown cooldown mode, mode/duration mismatch, fractional-second duration or out-of-range duration, **When** the caller invokes `UpdatePoolPolicy`, **Then** it fails with `ErrInvalidArgument` before reading or writing storage.
  - Evidence: UNTESTED (behaviour in `pool.go` `UpdatePoolPolicy`; policy validation in `validate.go` `validatePolicy`, `validateCooldown`)

- **AC-31**: **Given** the requested pool does not exist, **When** the caller invokes `UpdatePoolPolicy` with a valid policy, **Then** the call fails with `ErrNotFound`.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyUnknownPoolIsNotFound`

- **AC-32**: **Given** a valid requested policy and an existing pool document whose reservations document is absent or belongs to a different incarnation, **When** `UpdatePoolPolicy` exhausts its finite configured attempt budget without obtaining a usable pair, **Then** it fails and leaves the existing documents unchanged, including the remaining pool document when the reservations document is absent. See BASE-0006 error-contract AC-33 for the reason and sentinel.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyRefusesOtherIncarnation` (mismatched incarnations); UNTESTED for absent reservations (behaviour in `pool.go` `readUsableDocuments`, `updatePoolStrategy`, `retryPolicyWrite`; `client.go` `contentionError`)

- **AC-33**: **Given** both strategy and cooldown need changing, **When** the strategy change succeeds but the cooldown change fails, **Then** the pool can expose the new strategy with the old cooldown, the returned error wraps the underlying failure, and repeating the same policy update can complete the change.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyRetryCompletesPartialUpdate`

- **AC-34**: **Given** the requested strategy already matches and only cooldown needs changing, **When** the cooldown write fails, **Then** the underlying error is returned without claiming a partially applied strategy change, the pool document remains unchanged, and retrying the same update can succeed.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyCooldownFailureWithEqualStrategyIsReportedPlainly`

- **AC-35**: **Given** strict cooldown is the only reason no requested prefix fits, **When** allocation fails, **Then** the error is an `OpError` with operation `allocate`, the pool ID and the allocation ID; its text includes the first release expiry at which the requested size could fit, rounded upward to a whole second in RFC3339 form, not merely the earliest record expiry. The error reason and sentinel are specified in BASE-0006 error-contract AC-25.
  - Evidence: `cooldown_test.go` `TestCooldownUntilUsesFirstFittingExpiryRoundedUp` / `whole second`, `fractional second`; `TestCooldownUntilSkipsExpiryThatCannotFreeFittingBlock`

- **AC-36**: **Given** the only requested `/24` is blocked by 256 cooling `/32` ranges with distinct expiries, **When** a strict allocation fails, **Then** its reported availability time reflects the last expiry needed to free that `/24`, rounded upward to a whole second.
  - Evidence: `cooldown_expiry_test.go` `TestCooldownUntilManyExpiriesRequiresLastRecord`

- **AC-37**: **Given** live reservations leave no free block large enough for a request even when cooldown is ignored, **When** selection runs with cooldown disabled, strict or fit-aware, **Then** it reports exhaustion from unavailable free space rather than classifying cooldown as the sole blocker.
  - Evidence: `internal/alloc/select_test.go` `TestSelect` / `exhausted before cooldown` (all three cooldown-mode table entries)

- **AC-38**: **Given** the same sets of pool CIDRs, live reservations and cooling ranges, **When** their ordering changes, **Then** selection returns the same CIDR or error for each supported strategy and cooldown mode.
  - Evidence: `internal/alloc/select_test.go` `TestSelectPermutations`

- **AC-39**: Withdrawn (covered by BASE-0007 document-format AC-23 and AC-33)

- **AC-40**: **Given** a policy update has identified one pool incarnation and the pool is replaced by another incarnation before a subsequent phase or retry reads it, **When** the update observes that replacement, **Then** it fails rather than changing the replacement pool's policy. The error reason and sentinel are specified in BASE-0006 error-contract AC-34.
  - Evidence: UNTESTED (behaviour in `pool.go` `UpdatePoolPolicy`, `readUsableDocuments`, `updatePoolStrategy`, `updatePoolCooldown`)

- **AC-41**: **Given** enabled cooldown has unexpired release records, **When** a caller changes its mode between `strict` and `fit-aware` using `UpdatePoolPolicy`, **Then** the new mode is applied while the existing records retain their absolute expiry times.
  - Evidence: UNTESTED (behaviour in `pool.go` `updatePoolCooldown`)

- **AC-42**: **Given** a `best-fit` parent pool without cooldown contains `10.0.0.0/24` with live reservations for `10.0.0.128/26` and `10.0.0.224/27`, **When** a caller creates a child by requesting a `/28` and sets the child's own strategy to `lowest`, **Then** the child's range is `10.0.0.192/28`, from the parent's tightest fitting free block, rather than `10.0.0.0/28`; the parent's policy governs carving the child range, while the child's `lowest` strategy governs subsequent prefix-length allocations within the child.
  - Evidence: UNTESTED (contrasting-strategy child placement in `pool.go` `createPoolAttempt`, `allocation.go` `allocateAttempt`, and `internal/alloc/alloc.go` `Select`)

- **AC-43**: **Given** an enabled cooldown has both expired and unexpired release records, **When** a caller successfully changes its mode or duration using `UpdatePoolPolicy` while keeping cooldown enabled, **Then** records expiring at or before the updating client's current time are removed, while unexpired records retain their original absolute expiry times; this pruning occurs when cooldown settings change, not when identical settings are requested.
  - Evidence: UNTESTED (behaviour in `pool.go` `updatePoolCooldown` and `allocation.go` `pruneReleased`)

## Non-functional requirements *(mandatory)*

- **NFR-1**: An enabled cooldown duration is at least 1 second and at most 8760 hours, inclusive. Source: `validate.go` `minCooldown`, `maxCooldown`, `validateCooldown`; public contract: `types.go` `Cooldown`. Boundary acceptance is asserted by `policy_test.go` `TestPolicyValidateRequiresStrategyOutsideCreate`.
- **NFR-2**: Cooldown duration has a granularity of 1 second; fractional-second durations are rejected rather than rounded. Source: `validate.go` `validateCooldown`; evidence: `policy_test.go` `TestPolicyCreateRejectsInvalidPolicies` / `fractional seconds`.

## Key entities *(include if the feature involves data)*

- **Pool policy (`PoolPolicy`)**: The pool's concrete placement strategy and cooldown configuration. Returned pools expose this policy through `Pool.Policy`; creation accepts it through `PoolSpec.Policy`.
- **Allocation strategy (`AllocationStrategy`)**: `lowest` or `best-fit`. The empty `StrategyDefault` is an unset value accepted at creation, not a concrete strategy returned for a usable pool.
- **Cooldown (`Cooldown`)**: A mode and duration. `CooldownNone` is the empty mode paired with zero duration; `strict` and `fit-aware` require an enabled duration within the limits above.
- **Cooling range**: A previously released CIDR with an absolute expiry, assessed using the client's clock. It is not a live allocation. Explicit allocation can overlap an unexpired release record, but not a live allocation.
- **Selection request**: A prefix-length request affected by strategy and cooldown, or an explicit CIDR request that bypasses cooldown. Request validation, identity and allocation ownership are specified in BASE-0003 allocations.

## Out of scope *(mandatory)*

- Pool creation, listing, metadata, deletion, incarnation pairing and child lifecycle beyond their policy effects: see BASE-0001 pool-lifecycle.
- Allocation identity, containment, live overlap, general replay semantics, metadata and general release behaviour: see BASE-0003 allocations.
- Retry budgets, backoff, client clock configuration, concurrent-writer correctness and the general unknown-outcome recovery contract: see BASE-0004 concurrency-and-retry.
- General pool ID, CIDR, UUID and caller-supplied metadata validation: see BASE-0005 input-validation. This spec does not redefine the metadata write limits.
- General sentinel and `OpError` contracts: see BASE-0006 error-contract.
- Stored keys, JSON field layouts, schema versions, byte compatibility, and malformed release-record decoding: see BASE-0007 document-format.
- Pool utilisation and fragmentation reporting: see BASE-0008 pool-stats.
- CLI flags, parsing and output: see BASE-0014 cli.
- Atomic replacement of strategy and cooldown together: a policy update can be partially applied as described in AC-33.
- A guaranteed reuse delay under fit-aware mode, or under explicit CIDR allocation in any mode.
- Strategies other than `lowest` and `best-fit`, and cooldown modes other than disabled, `strict` and `fit-aware`.

## Open questions *(mandatory, empty before G2)*

- None. Questions raised during the baseline run were answered by the maintainer on 2026-11-03 and moved to issues: appvia/ipam#27.
