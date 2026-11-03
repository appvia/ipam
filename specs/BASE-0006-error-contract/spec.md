# Feature Specification: Public error contract

**Requirement**: Baseline (no Requirement issue)
**Spec path**: specs/BASE-0006-error-contract/spec.md
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Classify a failed operation (Priority: P1)

A Go library caller distinguishes missing resources, conflicts, exhausted pools, invalid inputs and unsupported documents through public error identities. When an error includes operation context, the caller can inspect its operation, pool, allocation, reason and underlying cause.

**Why this priority**: Callers need error categories and context to choose how to handle a failed operation without parsing its human-readable message.

### Journey 2 - Diagnose an invalid allocation request (Priority: P2)

A library caller reads an allocation error to understand which requested CIDR or prefix length is incompatible with a pool. The diagnostic provides relevant network information while omitting pool descriptions and labels.

**Why this priority**: Callers need enough information to correct a request without exposing unrelated metadata.

### Journey 3 - Recognise an incomplete pool listing (Priority: P2)

A library caller receiving an unsupported-pools error identifies the omitted pools and recognises that the accompanying results remain valid partial results. Pool-listing behaviour is specified in BASE-0001 pool-lifecycle; this capability defines the error representation.

**Why this priority**: An unsupported document must be distinguishable from a wholly failed listing.

## Acceptance criteria *(mandatory)*

For pool lifecycle, allocation policy and allocation error conditions, this spec owns the reason strings and sentinel identities, and BASE-0001 pool-lifecycle, BASE-0002 allocation-policy and BASE-0003 allocations reference the applicable ACs here.

- **AC-1**: **Given** a caller using the public error contract, **When** it selects an error category, **Then** six distinct sentinels are available: `ErrNotFound` for a missing pool or allocation, `ErrConflict` for a state or identity conflict, `ErrPoolExhausted` for no matching free CIDR, `ErrInvalidCIDR` for malformed or inconsistent CIDR data, `ErrInvalidArgument` for an invalid non-CIDR argument, and `ErrUnsupportedSchema` for an unknown stored schema version, with the messages listed under Key entities.
  - Evidence: UNTESTED (complete sentinel inventory and messages in `errors.go` `ErrNotFound`, `ErrConflict`, `ErrPoolExhausted`, `ErrInvalidCIDR`, `ErrInvalidArgument`, `ErrUnsupportedSchema`; the scoped tests exercise only a subset).

- **AC-2**: **Given** an `OpError` with a pool identifier, an underlying cause and a pool operation such as `create pool` or `get pool`, **When** the caller renders the error, **Then** the message has the form `<operation> <pool-id>: <cause>`, without repeating the word `pool` between the operation and identifier.
  - Evidence: `errors_message_test.go` `TestOpErrorDoesNotRepeatPool/create pool`, `TestOpErrorDoesNotRepeatPool/get pool`.

- **AC-3**: **Given** an `OpError` with a non-empty reason such as `pool_not_empty`, **When** the caller renders the error, **Then** the reason appears in parentheses after the operation and resource context and before the underlying cause.
  - Evidence: `errors_message_test.go` `TestOpErrorDoesNotRepeatPool/delete pool with reason`.

- **AC-4**: **Given** an `OpError` with `Op` equal to `allocate`, non-empty `Pool` and `ID`, a non-nil `Err` and an empty `Reason`, **When** the caller renders it, **Then** the message has the form `allocate pool <pool-id> allocation <allocation-id>: <cause>`.
  - Evidence: `errors_message_test.go` `TestOpErrorDoesNotRepeatPool/allocate keeps the pool label`.

- **AC-5**: **Given** an `OpError` wrapping a sentinel, **When** the caller checks the error using `errors.Is` for that sentinel, **Then** the sentinel remains identifiable despite the added operation, resource or reason context.
  - Evidence: `errors_message_test.go` `TestOpErrorDoesNotRepeatPool` (every subtest checks `errors.Is` against the wrapped sentinel).

- **AC-6**: **Given** an `azblob://` URL opened with no Azure credential (see BASE-0009 backend-url AC-12), **When** the caller renders the returned error, **Then** the message contains exactly one occurrence of `invalid argument`.
  - Evidence: `errors_message_test.go` `TestOpenStorageAzblobErrorIsNotDoubled`.

- **AC-7**: **Given** an explicit allocation CIDR outside a pool, including an IPv4 CIDR supplied for an IPv6 pool, **When** allocation fails, **Then** the error matches `ErrInvalidCIDR` and its diagnostic identifies the requested CIDR, names the pool and lists all of that pool's CIDRs.
  - Evidence: `errors_message_test.go` `TestInvalidCIDRCarriesReason/outside the pool`, `TestInvalidCIDRCarriesReason/multi-CIDR outside lists every CIDR`, `TestInvalidCIDRCarriesReason/IPv4 CIDR on an IPv6 pool is outside`.

- **AC-8**: **Given** a requested allocation prefix shorter than a pool can supply, **When** allocation fails, **Then** the error matches `ErrInvalidCIDR` and reports the requested prefix length and pool identifier; for a single-CIDR pool it names the pool's prefix length, and for a multi-CIDR pool it explicitly names the largest block and its prefix length, for both IPv4 and IPv6.
  - Evidence: `errors_message_test.go` `TestInvalidCIDRCarriesReason/prefix shorter than the pool`, `TestInvalidCIDRCarriesReason/multi-CIDR shorter names the largest block`, `TestInvalidCIDRCarriesReason/IPv6 multi-CIDR shorter names the largest block`; IPv6 single-CIDR wording is UNTESTED (behaviour in `allocation.go` `invalidRequestReason`).

- **AC-9**: **Given** an IPv4 pool and a requested allocation prefix length of 33, **When** allocation fails, **Then** the error matches `ErrInvalidCIDR` and reports that the requested length exceeds the pool's `/32` addresses, naming the pool for either a single-CIDR or multi-CIDR pool.
  - Evidence: `errors_message_test.go` `TestInvalidCIDRCarriesReason/prefix longer than the address`, `TestInvalidCIDRCarriesReason/multi-CIDR longer`.

- **AC-10**: **Given** a pool with a description and labels, **When** allocation fails because an explicit CIDR is outside the pool or the requested prefix length is shorter than the pool or longer than its address width, **Then** the error matches `ErrInvalidCIDR` and its text omits the pool description and label keys and values.
  - Evidence: `errors_message_test.go` `TestInvalidCIDRReasonOmitsPoolMetadata` (checks all three request shapes).

- **AC-11**: **Given** a caller receiving `UnsupportedPoolsError` from `ListPools`, **When** it inspects that error, **Then** `PoolIDs` identifies the pools omitted because a document uses an unsupported schema version, and the accompanying listing is a valid partial result.
  - Evidence: `listpools_test.go` `TestListPoolsSkipsUnsupportedSchemaPools` (checks the error type, omitted `PoolIDs` and readable partial results; listing execution belongs to BASE-0001 pool-lifecycle).

- **AC-12**: **Given** a non-nil `UnsupportedPoolsError` with pool identifiers, **When** the caller renders it, **Then** its message is `ipam: unsupported pools: ` followed by the identifiers in ascending string order, separated by `, `, and rendering leaves the caller-visible `PoolIDs` order unchanged.
  - Evidence: UNTESTED (behaviour in `errors.go` `UnsupportedPoolsError.Error`).

- **AC-13**: **Given** a non-nil `UnsupportedPoolsError`, **When** the caller unwraps it or checks it with `errors.Is`, **Then** its underlying category is `ErrUnsupportedSchema`.
  - Evidence: `listpools_test.go` `TestListPoolsSkipsUnsupportedSchemaPools` checks `errors.Is` against `ErrUnsupportedSchema`; the direct `Unwrap` return is code-backed by `errors.go` `UnsupportedPoolsError.Unwrap`, with no direct-call assertion.

### Edge cases

- **AC-14**: **Given** a nil `*OpError` or nil `*UnsupportedPoolsError` receiver, **When** the caller invokes `Error` or `Unwrap`, **Then** `Error` returns `<nil>` and `Unwrap` returns nil.
  - Evidence: UNTESTED (behaviour in `errors.go` `OpError.Error`, `OpError.Unwrap`, `UnsupportedPoolsError.Error`, `UnsupportedPoolsError.Unwrap`).

- **AC-15**: **Given** a non-nil `OpError` with all fields empty and no underlying cause, **When** the caller renders or unwraps it, **Then** its message is `ipam operation failed` and its underlying cause is nil.
  - Evidence: UNTESTED (behaviour in `errors.go` `OpError.Error`, `OpError.Unwrap`).

- **AC-16**: **Given** an `OpError` with a non-empty operation and some empty context fields, **When** the caller renders it, **Then** an empty `Pool` adds no pool label or identifier, an empty `ID` adds no allocation label or identifier, an empty `Reason` adds no parentheses, and a nil `Err` adds no colon or cause text.
  - Evidence: UNTESTED (complete optional-field behaviour in `errors.go` `OpError.Error`; scoped formatting tests cover only some combinations).

- **AC-17**: **Given** an `OpError` whose operation is exactly `pool` and whose pool identifier is non-empty, **When** the caller renders it, **Then** the identifier follows that operation without inserting another `pool` label.
  - Evidence: UNTESTED (behaviour in `errors.go` `OpError.Error`).

- **AC-18**: **Given** caller-supplied operation, pool, allocation, reason or underlying-cause text in an `OpError`, **When** the caller renders it, **Then** those supplied values are included verbatim within the error's formatting, without automatic redaction.
  - Evidence: UNTESTED (behaviour in `errors.go` `OpError.Error`).

- **AC-19**: **Given** a non-nil `UnsupportedPoolsError` with an empty `PoolIDs` list or repeated identifiers, **When** the caller renders it, **Then** an empty list produces exactly `ipam: unsupported pools: `, including the final space, and repeated identifiers remain repeated in the sorted output.
  - Evidence: UNTESTED (behaviour in `errors.go` `UnsupportedPoolsError.Error`).

- **AC-20**: **Given** pool creation rejected because the existing pool differs from the requested specification (see BASE-0001 pool-lifecycle), **When** the caller inspects the error, **Then** `OpError.Reason` is `pool_exists` and `errors.Is` matches `ErrConflict`.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolExistingDifferentSpecWritesNothing`.

- **AC-21**: **Given** pool deletion rejected because allocations remain, including a child-pool carve-out (see BASE-0001 pool-lifecycle), **When** the caller inspects the error, **Then** `OpError.Reason` is `pool_not_empty` and `errors.Is` matches `ErrConflict`.
  - Evidence: `client_lifecycle_test.go` `TestChildPoolLifecycleAndParentDeleteGuard`.

- **AC-22**: **Given** release rejected because the allocation is a carve-out still claimed by its child pool (see BASE-0001 pool-lifecycle and BASE-0003 allocations), **When** the caller inspects the error, **Then** `OpError.Reason` is `pool_in_use` and `errors.Is` matches `ErrConflict`.
  - Evidence: `client_attempt3_test.go` `TestIncarnationKeyedCarveOutProtectsRecreatedChild`.

- **AC-23**: **Given** allocation rejected because a supplied identifier already belongs to a different request (see BASE-0003 allocations), **When** the caller inspects the error, **Then** `OpError.Reason` is `id_reused` and `errors.Is` matches `ErrConflict`.
  - Evidence: `client_test.go` `TestAllocateSameIDConvergesAndReuseConflicts`.

- **AC-24**: **Given** an explicit allocation rejected because its CIDR overlaps a live allocation (see BASE-0003 allocations), **When** the caller inspects the error, **Then** `OpError.Reason` is `overlap` and `errors.Is` matches `ErrConflict`.
  - Evidence: UNTESTED (library-level reason/category association in `allocation.go` `mapAllocatorError`, called by `allocateAttempt`); partial evidence: `cmd/ipam/main_test.go` `TestExitCodes/overlap` checks the CLI conflict exit code and `overlap` in stderr, not the library-level `errors.Is`/Reason assertion.

- **AC-25**: **Given** allocation rejected because only cooling space fits the requested prefix length (see BASE-0002 allocation-policy), **When** the caller inspects the error, **Then** `OpError.Reason` is `cooldown` and `errors.Is` matches `ErrPoolExhausted`.
  - Evidence: `cooldown_test.go` `TestCooldownStrictRefusesUntilExpiryAndPrunes`.

- **AC-26**: **Given** pool creation has exhausted its attempts while an incomplete pool remains (see BASE-0001 pool-lifecycle and BASE-0004 concurrency-and-retry), **When** the caller inspects the error, **Then** `OpError.Reason` is `pool_incomplete` and `errors.Is` matches `ErrConflict`.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolIncompleteStatesRetryThenReportWithoutWrites/pool document without reservations`, `TestCreatePoolIncompleteStatesRetryThenReportWithoutWrites/reservations without pool document`, `TestCreatePoolIncompleteStatesRetryThenReportWithoutWrites/mismatched incarnations`.

- **AC-27**: **Given** an operation other than pool creation uses bounded retries and exhausts its attempts after repeatedly encountering different incarnations in the pool document and reservations document, including `Allocate`, `Release`, `UpdateAllocationMetadata` and `UpdatePoolPolicy` (see BASE-0004 concurrency-and-retry), **When** the caller inspects the final error, **Then** `OpError.Reason` is `pool_incomplete` and `errors.Is` matches `ErrNotFound`.
  - Evidence: `UpdatePoolPolicy` is covered by AC-33's test, `updatepolicy_test.go` `TestUpdatePoolPolicyRefusesOtherIncarnation` through `assertPoolIncomplete`; the other operations are UNTESTED (complete reason/category association in `client.go` `contentionError`; `client_attempt4_test.go` `TestCreatePoolIncompleteStatesRetryThenReportWithoutWrites/mismatched incarnations` checks the final allocation error's `ErrNotFound` identity but not its reason).

- **AC-28**: **Given** pool creation has exhausted its attempts after a concurrent change interrupted publication (see BASE-0001 pool-lifecycle and BASE-0004 concurrency-and-retry), **When** the caller inspects the final error, **Then** `OpError.Reason` is `pool_create_raced` and `errors.Is` matches `ErrConflict`.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolPostCheckAbortsAfterReservationsDeleted/attempts=1`.

- **AC-29**: **Given** an operation has exhausted its attempts because of conditional-write contention rather than repeated incarnation mismatches or the pool-creation failures in AC-26 and AC-28 (see BASE-0004 concurrency-and-retry), **When** the caller inspects the final error, **Then** `OpError.Reason` is `contention`, `errors.Is` matches `ErrConflict`, and the last attempt's underlying error remains in the chain.
  - Evidence: `client_test.go` `TestAllocatePermanentPreconditionUsesExactlyMaxAttempts` checks the reason and `ErrConflict` identity; retention of the last attempt's underlying error is UNTESTED (behaviour in `client.go` `contentionError`).

- **AC-30**: Withdrawn (maintainer confirmed the read-path `validate`/`incarnation_mismatch`/`ErrConflict` result for mismatched incarnations is a bug; tracked in appvia/ipam#30).

- **AC-31**: **Given** child-pool creation is rejected because only cooling space in its parent fits the requested prefix length (see BASE-0001 pool-lifecycle and BASE-0002 allocation-policy), **When** the caller inspects the error, **Then** the outer `OpError` has `Op` equal to `create pool`, `Pool` equal to the child pool identifier, an empty `ID`, and `Reason` equal to `cooldown`, and `errors.Is` matches `ErrPoolExhausted`.
  - Evidence: `cooldown_test.go` `TestCooldownChildCreateUsesParentPolicy` through `assertCooldownError`.

- **AC-32**: **Given** child-pool creation is rejected by a parent allocation error whose reason is not `cooldown` (see BASE-0001 pool-lifecycle and BASE-0003 allocations), **When** the caller inspects the error, **Then** the outer `OpError` has `Op` equal to `create pool`, `Pool` equal to the child pool identifier, an empty `ID` and an empty `Reason`, and wraps the parent allocation error.
  - Evidence: UNTESTED (behaviour in `pool.go` `createPoolAttempt`, reached by `CreatePool`).

- **AC-33**: **Given** `UpdatePoolPolicy` uses bounded retries and the pool document keeps the incarnation `UpdatePoolPolicy` first read, while the reservations document has a different incarnation or stays absent (see BASE-0002 allocation-policy and BASE-0004 concurrency-and-retry), **When** the call exhausts its attempts and the caller inspects the error, **Then** `OpError.Reason` is `pool_incomplete` and `errors.Is` matches `ErrNotFound`.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyRefusesOtherIncarnation` through `assertPoolIncomplete` (different-incarnation case); absent-reservations-document case is UNTESTED (behaviour in `pool.go` `usable`, `readUsableDocuments` and `client.go` `contentionError`).

- **AC-34**: **Given** a pool is replaced by another incarnation during `UpdatePoolPolicy` (see BASE-0002 allocation-policy), **When** the call detects the replacement, **Then** it returns an error without exhausting its retry budget, with `OpError.Reason` equal to `pool_incomplete` and `errors.Is` matching `ErrNotFound`.
  - Evidence: `client_review_regression_test.go` `TestUpdatePoolPolicyDoesNotModifyReplacement` through `assertPoolIncomplete` (reason and sentinel at the between-phases, strategy-retry, strategy-unknown-outcome and cooldown-retry boundaries); returning without exhausting the retry budget is UNTESTED (behaviour in `pool.go` `readUsableDocuments`, `updatePoolStrategy` and `updatePoolCooldown`, reached by `UpdatePoolPolicy`).

## Non-functional requirements *(mandatory)*

- None enforced in code.

## Key entities *(include if the feature involves data)*

- **Public sentinels**: Error identities usable with `errors.Is`. Their text is:

  | Sentinel | Message |
  | --- | --- |
  | `ErrNotFound` | `ipam: not found` |
  | `ErrConflict` | `ipam: conflict` |
  | `ErrPoolExhausted` | `ipam: pool exhausted` |
  | `ErrInvalidCIDR` | `ipam: invalid CIDR` |
  | `ErrInvalidArgument` | `ipam: invalid argument` |
  | `ErrUnsupportedSchema` | `ipam: unsupported schema` |

- **`OpError`**: An exported error carrying string fields `Op` (operation), `Pool` (pool identifier), `ID` (allocation identifier) and `Reason` (optional reason), plus `Err` (underlying error). Its `Error` method provides diagnostic text and `Unwrap` exposes `Err`. `Reason` is a string rather than an exported enumeration; AC-20 through AC-34 record the emitted vocabulary and its error categories at the baseline commit without promising future vocabulary stability.
- **`UnsupportedPoolsError`**: An exported error carrying `PoolIDs`, a list of pool identifiers omitted from an otherwise valid partial listing because of unsupported schema versions. Its error category is `ErrUnsupportedSchema`.

## Out of scope *(mandatory)*

- Conditions under which pool operations fail or return partial results: see BASE-0001 pool-lifecycle. This spec records the emitted reason strings and error categories.
- Allocation execution, allocation-policy decisions and the conditions producing allocation errors: see BASE-0003 allocations and BASE-0002 allocation-policy. This spec covers only the diagnostic outcomes evidenced above.
- Retry limits, retry execution and unknown-outcome recovery: see BASE-0004 concurrency-and-retry.
- Input acceptance and canonicalisation rules: see BASE-0005 input-validation.
- Stored document schemas and the conditions identifying an unsupported version: see BASE-0007 document-format.
- Backend URL grammar, URL and credential redaction, and backend error generation: see BASE-0009 backend-url, BASE-0010 storage-contract and BASE-0011 through BASE-0013 for the backends.
- CLI exit codes and stderr presentation: see BASE-0014 cli.
- A general guarantee that arbitrary `OpError` fields or wrapped causes are safe to publish. The metadata-omission guarantee in AC-10 applies to the specified allocation diagnostics; AC-18 describes the general formatter.

## Open questions *(mandatory, empty before G2)*

- None. Questions raised during the baseline run were answered by the maintainer on 2026-11-03 and moved to issues: appvia/ipam#30.
