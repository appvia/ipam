# Feature Specification: Pool utilisation and fragmentation statistics

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0008-pool-stats/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Inspect pool utilisation without changing it (Priority: P1)

A library caller uses `Client.PoolStats(ctx, poolID, referenceLength)` to inspect a pool's address totals, free space, allocation count, policy and cooling count without making a reservation or modifying stored state.

**Why this priority**: A caller can assess an existing pool without consuming addresses or changing allocation behaviour.

### Journey 2 - Assess capacity for a requested subnet size (Priority: P2)

A library caller supplies a reference prefix length, or lets the client infer one from existing prefix-length allocations, to compare the number of whole subnets that fit with the capacity implied by the total free address count.

**Why this priority**: Free address totals alone do not show whether usable subnets of a particular size remain.

### Journey 3 - Interpret recently released space (Priority: P2)

A library caller inspects the cooling count and current cooldown policy alongside geometric free-space statistics, distinguishing address fragmentation from temporary restrictions on reuse.

**Why this priority**: A positive subnet count does not guarantee that an allocation subject to strict cooldown will succeed.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** an existing pool, including one with expired release records, **When** a caller requests statistics, **Then** the request makes no storage writes and does not persist removal of expired release records.
  - Evidence: `poolstats_test.go` `TestPoolStatsFragmentation`; `poolstats_test.go` `TestPoolStatsCountsOnlyUnexpiredCooling`

- **AC-2**: **Given** a usable pool, **When** a caller requests statistics, **Then** the result identifies the requested pool in `PoolID` and reports in `Policy` the allocation strategy from the pool document and the cooldown mode and duration from the reservations document; after a partially applied `UpdatePoolPolicy`, these can pair the new strategy with the previous cooldown (see [BASE-0002 allocation-policy](../BASE-0002-allocation-policy/spec.md) for partial policy updates).
  - Evidence: Partial: `poolstats_test.go` `TestPoolStatsCountsOnlyUnexpiredCooling` asserts the cooldown mode; `cmd/ipam/doctor_fragmentation_test.go` `TestDoctorFragmentationHealthyPoolPasses` asserts a non-empty strategy and cooldown `none` through doctor JSON output. The complete identity, duration and policy provenance, including partial-update pairing, are not asserted (behaviour in `stats.go` `Client.PoolStats`).

- **AC-3**: **Given** a pool containing allocation records, **When** a caller requests statistics, **Then** `Allocations` reports the number of those records; a pool with three recorded allocations reports three.
  - Evidence: `poolstats_test.go` `TestPoolStatsFragmentation`

- **AC-4**: **Given** a pool with CIDRs and allocated ranges, **When** a caller requests statistics, **Then** `Total` counts the addresses in the pool and `Free` counts addresses remaining after allocated ranges are excluded; a `/16` with two `/24` allocations and one `/26` allocation reports `Total = 65536` and `Free = 64960`.
  - Evidence: `poolstats_test.go` `TestPoolStatsFragmentation`

- **AC-5**: **Given** free space of different subnet sizes, **When** a caller requests statistics, **Then** `Histogram` counts maximal free CIDR blocks by ascending prefix length and omits lengths with no blocks; for `10.0.0.0/24` with `10.0.0.64/26` allocated, it contains one `/25` block followed by one `/26` block.
  - Evidence: `internal/alloc/stats_test.go` `TestStats` / `"/24 used .64/26 ref 25"`

- **AC-6**: **Given** a pool with free space, **When** a caller requests statistics, **Then** `Largest` is the free CIDR with the shortest prefix length, choosing the lowest address when that length is tied.
  - Evidence: `internal/alloc/stats_test.go` `TestStats` / `"/24 used .64/26 ref 25"`; `internal/alloc/stats_test.go` `TestStats` / `"largest tie lowest address"`

- **AC-7**: **Given** allocations requested by prefix length with one length occurring most frequently, **When** a caller supplies reference length `0`, **Then** `ReferenceLength` is that most frequent length; lengths `24`, `24` and `26` select `24`.
  - Evidence: `poolstats_test.go` `TestPoolStatsFragmentation`

- **AC-8**: **Given** a tie for the most frequent length among allocations requested by prefix length, **When** a caller supplies reference length `0`, **Then** the smaller numerical prefix length wins; lengths `26`, `24` and `25` select `24`.
  - Evidence: `poolstats_test.go` `TestPoolStatsReferenceTieBreaksToSmallerLength`

- **AC-9**: **Given** a valid nonzero reference length, **When** a caller supplies it explicitly, **Then** `ReferenceLength` uses that value instead of the automatically inferred length.
  - Evidence: `poolstats_test.go` `TestPoolStatsReferenceTieBreaksToSmallerLength`

- **AC-10**: **Given** a pool with no allocations requested by prefix length, **When** a caller supplies reference length `0`, **Then** `ReferenceLength` remains `0` and `Allocatable`, `Capacity` and `FragmentationIndex` are nil; explicit-CIDR allocations do not supply an inferred reference.
  - Evidence: `poolstats_test.go` `TestPoolStatsNoReferenceWithoutNextAllocations`

- **AC-11**: **Given** a selected reference length, **When** a caller requests statistics, **Then** `Allocatable` counts non-overlapping, aligned subnets of that length obtainable from whole free blocks, while `Capacity` is the integer floor of `Free` divided by the address count of one reference-sized subnet; an unused `/24` at reference `/26` reports four for both, while a `/24` with its second and fourth `/26` blocks allocated at reference `/25` reports `Allocatable = 0` and `Capacity = 1`.
  - Evidence: `internal/alloc/stats_test.go` `TestStats` / `"empty /24 ref 26"`; `internal/alloc/stats_test.go` `TestStats` / `"/24 used .64/26 and .192/26 ref 25 index 1"`

- **AC-12**: **Given** a selected reference length and positive `Capacity`, **When** a caller requests statistics, **Then** `FragmentationIndex` is the exact rational value `1 - Allocatable / Capacity`, including `0` when the counts match, `1` when no whole reference-sized subnet fits, and fractional values such as `1/2` and `1/3`.
  - Evidence: `internal/alloc/stats_test.go` `TestStatsFractionalIndex` / `"free space already unfragmented"`; `internal/alloc/stats_test.go` `TestStatsFractionalIndex` / `"only halves free"`; `internal/alloc/stats_test.go` `TestStatsFractionalIndex` / `"one of two whole blocks free"`; `internal/alloc/stats_test.go` `TestStatsFractionalIndex` / `"two of three whole blocks free"`

- **AC-13**: **Given** release records expiring before, exactly at and after the client's current clock time, **When** a caller requests statistics, **Then** `Cooling` counts only records whose expiry is strictly later than that time, and later requests cease counting those records after their expiry.
  - Evidence: `poolstats_test.go` `TestPoolStatsCountsOnlyUnexpiredCooling`

- **AC-14**: **Given** otherwise unallocated released ranges that are still cooling, **When** a caller requests statistics, **Then** those ranges are included in `Free` and the free-space geometry used for `Largest`, `Histogram`, `Allocatable`, `Capacity` and `FragmentationIndex`; `Cooling` reports them separately, so a positive `Allocatable` value does not guarantee a request subject to strict cooldown can succeed.
  - Evidence: Partial: `cmd/ipam/doctor_cooling_test.go` `TestDoctorFragmentationReportsCoolingSeparately`, through doctor output formatting, asserts `Allocatable = 1`, `Capacity = 1`, index `0` and cooling count `1`, then verifies strict `Allocate` fails with `ErrPoolExhausted`; `Free`, `Largest` and `Histogram` are not asserted (behaviour in `stats.go` `PoolStats` field documentation and `Client.PoolStats`).

### Edge cases

- **AC-15**: **Given** a pool ID that does not exist, **When** a caller requests statistics, **Then** the request fails with an error matching `ErrNotFound`.
  - Evidence: `poolstats_test.go` `TestPoolStatsErrors`

- **AC-16**: **Given** an invalid pool ID, **When** a caller requests statistics, **Then** the request fails with an error matching `ErrInvalidArgument`.
  - Evidence: `poolstats_test.go` `TestPoolStatsErrors`

- **AC-17**: **Given** a nonzero reference length less than the pool's shortest CIDR length or greater than its address-family width, **When** a caller requests statistics, **Then** the request fails with an error matching `ErrInvalidArgument`; for an IPv4 `/16` pool, `-1`, `8`, `33` and `40` are rejected.
  - Evidence: `poolstats_test.go` `TestPoolStatsErrors`

- **AC-18**: **Given** a fully allocated pool and a selected reference length, **When** a caller requests statistics, **Then** `Free`, `Allocatable` and `Capacity` are zero, `Largest` is the invalid zero-value prefix, `Histogram` has no entries and `FragmentationIndex` is nil.
  - Evidence: `internal/alloc/stats_test.go` `TestStats` / `"full pool"`

- **AC-19**: **Given** a selected reference length but fewer free addresses than one subnet of that length, **When** a caller requests statistics, **Then** `Allocatable` and `Capacity` are zero and `FragmentationIndex` is nil even if some addresses remain free.
  - Evidence: UNTESTED (behaviour in `internal/alloc/stats.go` `ComputeStats`)

- **AC-20**: **Given** adjacent CIDRs belonging to the same pool, **When** a caller requests statistics, **Then** free blocks from separate pool CIDRs are not merged; two unused adjacent `/25` pool CIDRs report two `/25` histogram blocks and a `/25` largest block rather than a `/24`.
  - Evidence: `internal/alloc/stats_test.go` `TestStats` / `"adjacent /25 parents never merge"`

- **AC-21**: **Given** an unused IPv6 `::/0` pool and reference length `64`, **When** a caller requests statistics, **Then** `Total` and `Free` each represent exactly `2^128` addresses, `Allocatable` and `Capacity` each represent exactly `2^64` subnets and `Largest` is `::/0`.
  - Evidence: `internal/alloc/stats_test.go` `TestStatsIPv6Whole`

- **AC-22**: **Given** a statistics request that fails client validation or cannot load usable pool state, **When** the request fails, **Then** the call returns the validation or state-read error and a zero-value statistics result rather than successful partial statistics, without retrying; the incarnation-mismatch error is a known bug tracked in appvia/ipam#30 (see [BASE-0006 error-contract](../BASE-0006-error-contract/spec.md)), and schema handling is specified in [BASE-0007 document-format](../BASE-0007-document-format/spec.md).
  - Evidence: UNTESTED (behaviour in `stats.go` `Client.PoolStats`)

- **AC-23**: **Given** a usable pool and an out-of-range reference length as in AC-17, **When** statistics calculation rejects the reference, **Then** the error is an `*OpError` with `Op = "stats"` and the requested `Pool` ID, and matches `ErrInvalidArgument`; calculation errors matching `ErrInvalidCIDR` receive the same wrapping and preserve that public sentinel, but that branch is effectively unreachable after pool validation accepts the data.
  - Evidence: UNTESTED for the `Op` and `Pool` fields (behaviour in `stats.go` `Client.PoolStats`; `poolstats_test.go` `TestPoolStatsErrors` checks only sentinel identity).

- **AC-24**: **Given** recorded explicit-CIDR allocations or allocations backing child-pool carve-outs, **When** a caller requests statistics for their containing pool, **Then** those records contribute to `Allocations` and their address ranges are excluded from free space just like other allocation records; carve-outs requested by prefix length also count toward reference-length inference in AC-7 and AC-8, while explicit-CIDR carve-outs do not; child-pool creation is specified in BASE-0001 pool-lifecycle.
  - Evidence: UNTESTED (behaviour in `stats.go` `Client.PoolStats`; the calculation includes every allocation record)

## Non-functional requirements *(mandatory)*

- **NFR-1**: A statistics request performs **0 storage writes per call**, including when expired release records exist. Source: `stats.go` `Client.PoolStats`; verified by `poolstats_test.go` `TestPoolStatsFragmentation` and `TestPoolStatsCountsOnlyUnexpiredCooling`.
- **NFR-2**: Address counts preserve exact integer precision through **2^128 addresses**, and subnet counts can represent **2^64 subnets** for an IPv6 `/0` measured at `/64`. Source: `internal/alloc/stats.go` `blockSize` and `ComputeStats`; verified by `internal/alloc/stats_test.go` `TestStatsIPv6Whole`.

## Key entities *(include if the feature involves data)*

- **Statistics request**: A context, pool ID and integer reference prefix length. `0` requests inference; a nonzero value must fall between the shortest pool CIDR length and the family width, inclusive: `32` bits for IPv4 or `128` bits for IPv6.
- **Pool statistics (`PoolStats`)**: The pool ID, current policy, allocation-record count, unexpired cooling-record count, exact integer `Total` and `Free` address counts, largest free prefix, histogram and optional reference metrics.
- **Free-block histogram entry (`FreeBlockCount`)**: A `PrefixLength` and the `Count` of maximal free CIDR blocks of that length. Entries describe blocks, not address counts.
- **Reference metrics**: The selected `ReferenceLength`, exact integer counts `Allocatable` and `Capacity`, and exact rational `FragmentationIndex`. The two counts are nil without a reference; the index is also nil when capacity is zero.
- **Pool, allocation and policy inputs**: Existing pool CIDRs, recorded allocations and current strategy/cooldown policy. Pool lifecycle and allocation semantics are owned by BASE-0001 pool-lifecycle and BASE-0003 allocations.

## Out of scope *(mandatory)*

- Creating, listing, updating or deleting pools, validating their CIDR collections, pairing pool incarnations and handling incomplete pool residue: see [BASE-0001 pool-lifecycle](../BASE-0001-pool-lifecycle/spec.md).
- Creating, retrieving, listing, releasing or updating allocations, and deciding whether an allocation succeeds: see [BASE-0003 allocations](../BASE-0003-allocations/spec.md).
- Read-failure error identities, see [BASE-0006 error-contract](../BASE-0006-error-contract/spec.md); the incarnation-mismatch bug is tracked in appvia/ipam#30.
- Document schema versions and unsupported-schema handling: see [BASE-0007 document-format](../BASE-0007-document-format/spec.md).
- `ipam doctor` command output, warning thresholds, backend checks, write probes and exit codes: see [BASE-0015 cli-doctor](../BASE-0015-cli-doctor/spec.md).
- Reserving the reported capacity, compacting or relocating allocations, changing policy or persisting cleanup of cooling records. Statistics are read-only measurements, not allocation guarantees.
- A cooldown-adjusted allocatable count: geometric free-space and reference metrics deliberately include cooling ranges.
- Latency, throughput or freshness targets; this capability defines no such numeric service-level targets.

## Open questions *(mandatory, empty before G2)*

- None.
