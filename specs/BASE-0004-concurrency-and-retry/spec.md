# Feature Specification: Concurrency and retry

**Requirement**: Baseline (no Requirement issue)
**Spec path**: specs/BASE-0004-concurrency-and-retry/spec.md
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Share address space between independent writers (Priority: P1)

As a library caller running several services or processes against the same storage, I create pools and allocate or release ranges without arranging a shared application lock. Concurrent requests must preserve allocation ownership and prevent overlapping allocations, including when a pool is deleted and recreated during another request.

**Why this priority**: Independent writers sharing address space are the primary reason the client coordinates changes through conditional storage operations.

### Journey 2 - Choose how long to tolerate contention (Priority: P2)

As a service integrator, I use the default finite attempt budget for bounded retries, or configure unlimited attempts with my own context deadline. I can configure retry delays and distinguish contention from cancellation, an incomplete pool, or a backend error.

**Why this priority**: A service needs to decide whether to retry a request, repair an incomplete pool, or return an error to its caller.

### Journey 3 - Recover after interrupted operations (Priority: P2)

As a library caller recovering from a failed writer, I continue using unaffected pools and retry operations with a healthy client. If an interrupted operation leaves an incomplete pool, I can use the recovery operations defined by BASE-0001 pool-lifecycle rather than assuming an ambiguous write succeeded.

**Why this priority**: A lost response or crashed writer must not permanently prevent safe use of the remaining address space.

### Journey 4 - Supply runtime dependencies and release resources (Priority: P3)

As an integrator or test author, I supply a clock, allocation ID generator, entropy source, or cancellable sleeper through client options. When finished with a client, I close the storage resources it owns.

**Why this priority**: These options make client behaviour controllable without changing the pool or allocation API.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** independent processes using clients over the same local storage and a pool with sufficient capacity, **When** they allocate different IDs concurrently, **Then** every successful allocation remains present and their CIDRs do not overlap; the client does not require coordination shared between those processes.
  - Evidence: `client_crossprocess_test.go` `TestCrossProcessLocalAllocationProof` (four processes each allocate 25 ranges; the final list contains all 100 non-overlapping ranges).

- **AC-2**: **Given** independent clients concurrently creating and deleting child pools, allocating and releasing ordinary (non-carve-out) allocations, and changing policy, **When** their operations finish, **Then** ordinary allocations remain non-overlapping across the parent and child pools, allocations are not left in an unusable pool incarnation, and each published child remains backed by its matching carve-out, without requiring in-process coordination shared between the writers. Carve-outs (`pool:<child>:<incarnation>`; see BASE-0007 document-format Key entities) intentionally contain child allocations and are excluded from the cross-pool non-overlap guarantee. This guarantee excludes operator carve-out recovery while a `CreatePool` for that child may still run; see BASE-0001 pool-lifecycle and BASE-0003 allocations.
  - Evidence: `client_model_test.go` `TestLifecycleStressMemoryNoLocks`; `client_model_test.go` `TestLifecycleStressMemory`; `client_model_test.go` `TestLifecycleStressLocal` (all invoke the lifecycle invariant checks).

- **AC-3**: **Given** independent clients racing to create one pool ID with unlimited attempts and a live context, **When** requests use equal specifications, **Then** they converge successfully on the same pool; **When** requests use different specifications, **Then** only one specification wins, requests for the other specification fail with `pool_exists`, and the winning pool remains allocatable.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolRaceLocalLoop` (equal-specification and different-specification iterations).

- **AC-4**: **Given** a caller configuring `WithMaxAttempts(n)`, **When** a conditional operation requires retries, **Then** a positive `n` sets an attempt limit that includes the initial attempt rather than granting `n` additional retries; zero removes the count limit, allowing retriable work to continue until success, a non-retriable error, or context completion.
  - Evidence: UNTESTED (general option semantics in `client.go` `WithMaxAttempts` and `attemptsExhausted`; the specific three-attempt incomplete-create case is covered by AC-17).

- **AC-5**: **Given** a retriable operation that exhausts its attempt budget with a last cause other than an `incarnation_mismatch` conflict, **When** the client reports exhaustion as contention, **Then** it returns the exhaustion error specified by BASE-0006 error-contract AC-29.
  - Evidence: UNTESTED (behaviour in `client.go` `contentionError`).

- **AC-6**: **Given** positive base and cap durations configured with `WithBackoff`, **When** a retry delay is requested, **Then** the sleeper receives a full-jitter delay between zero and the exponentially increasing delay limit, inclusive; the first limit is the smaller of the base and cap, and successive limits double until reaching the cap.
  - Evidence: UNTESTED (window growth, cap enforcement and inclusive endpoints in `client.go` `waitRetry`; `client_gate_regression_test.go` `TestRetryBackoffUsesFullJitterAndCap` asserts injected delay samples of 3, 7 and 11 nanoseconds and their delivery to the sleeper, but those samples do not distinguish the window limits).

- **AC-7**: **Given** a storage backend and options passed to `New`, **When** the client is constructed, **Then** its defaults are the system clock, cryptographic entropy, the cancellable default sleeper, and `NewAllocationID` for allocation IDs; options apply in argument order, a nil option is ignored, and nil clock, entropy, sleeper or ID-generator callbacks leave the previously configured value unchanged. Allocation ID validity is defined by BASE-0005 input-validation.
  - Evidence: UNTESTED (behaviour in `client.go` `New`, `WithClock`, `WithRand`, `WithSleeper` and `WithIDGenerator`).

- **AC-8**: **Given** non-nil functions supplied through `WithClock` and `WithIDGenerator`, **When** the client needs an allocation creation time or a generated allocation ID, **Then** it uses the supplied functions instead of the corresponding defaults.
  - Evidence: UNTESTED (public callback contracts and configuration in `client.go` `Clock`, `IDGenerator`, `WithClock` and `WithIDGenerator`; the scoped model tests inject these functions but do not directly assert timestamp and generated-ID equality).

- **AC-9**: **Given** a non-nil entropy reader supplied through `WithRand`, **When** the client needs a new pool incarnation or retry jitter, **Then** it obtains entropy from that reader rather than the default source. Incarnation representation belongs to BASE-0007 document-format.
  - Evidence: UNTESTED (the combined contract is in `client.go` `WithRand`, `newIncarnation` and `waitRetry`; `TestRetryBackoffUsesFullJitterAndCap` covers the jitter use but not incarnation generation).

- **AC-10**: **Given** a custom `WithSleeper` callback, **When** the client requests a retry wait, **Then** the callback receives the retry context and computed delay, and an error returned by the callback remains identifiable as the cause of the failed wait.
  - Evidence: UNTESTED (behaviour in `client.go` `WithSleeper` and `waitRetry`; the scoped jitter test observes delay arguments but does not assert propagation of a sleeper error).

- **AC-11**: **Given** a client whose backend may implement `io.Closer`, **When** the caller invokes `Close`, **Then** the client calls the backend's `Close` and returns its error if that interface is implemented, otherwise it returns nil. The caller must not use the client afterwards.
  - Evidence: UNTESTED (behaviour and documented caller restriction in `client.go` `Client.Close`).

### Edge cases

- **AC-12**: **Given** a nil client, a client with no storage backend, or a client configured with a negative maximum-attempt count, **When** a caller invokes `CreatePool`, `GetPool`, `ListPools`, `UpdatePoolMetadata`, `UpdatePoolPolicy`, `DeletePool`, `Allocate`, `Release` or `UpdateAllocationMetadata`, **Then** it fails with `ErrInvalidArgument`; `New` itself does not return a construction-time validation error.
  - Evidence: UNTESTED (`client.go` `validateClient`, `WithMaxAttempts` and `New`; validation calls in the listed methods in `pool.go` and in `allocation.go` `allocateWithStatus`, `release` and `UpdateAllocationMetadata`). The allocation-reader inconsistency is recorded in appvia/ipam#28.

- **AC-13**: **Given** a backoff base or cap that is zero or negative, **When** a retry wait is requested with a live context, **Then** the sleeper is called with a zero-duration delay without reading jitter entropy.
  - Evidence: UNTESTED (behaviour in `client.go` `waitRetry`).

- **AC-14**: **Given** an injected entropy reader that fails while supplying a new incarnation or retry jitter, **When** that entropy is required, **Then** the failure preserves the reader error as a wrapped cause, with `generate incarnation` or `retry jitter` context respectively, rather than silently substituting another entropy source.
  - Evidence: UNTESTED (behaviour in `client.go` `newIncarnation` and `waitRetry`).

- **AC-15**: **Given** an already-cancelled context, **When** a retriable client operation reaches its retry wait, **Then** the operation returns an error matching `context.Canceled` without invoking the configured sleeper or reading jitter entropy.
  - Evidence: UNTESTED (the explicit context check in `client.go` `waitRetry`; `client_gate_regression_test.go` `TestDefaultSleeperHonoursCanceledContext` checks only the default sleeper with a still-pending one-hour timer, not this retry-wait contract).

- **AC-16**: **Given** same-pool work holding a client's in-process coordination slot, **When** an allocation request waits with a cancelled context, **Then** the request returns an error matching `context.Canceled` without waiting for that slot to become available.
  - Evidence: `client_gate_regression_test.go` `TestPoolLockWaitHonoursContextCancellation`.

- **AC-17**: **Given** a pool that remains incomplete because one of its paired documents is absent or their incarnations differ, **When** a caller requests the same pool specification with `WithMaxAttempts(3)`, **Then** creation performs two backoff waits, writes nothing, and returns the exhaustion error specified by BASE-0006 error-contract AC-26. Pool completeness and explicit residue recovery belong to BASE-0001 pool-lifecycle and BASE-0007 document-format.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolIncompleteStatesRetryThenReportWithoutWrites` / `pool document without reservations`, `reservations without pool document`, and `mismatched incarnations`.

- **AC-18**: **Given** a same-specification create request observing another creator's incomplete pool, **When** the other creator finishes before the next retry, **Then** the waiting request returns the completed pool successfully without writing its own pool state.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolIncompleteResolvesWhenCreatorFinishes`.

- **AC-19**: **Given** a retry budget exhausted with an `incarnation_mismatch` conflict as its last failure, **When** the client reports that failure through its contention error contract, **Then** it returns the exhaustion error specified by BASE-0006 error-contract AC-27, rather than reporting ordinary contention.
  - Evidence: UNTESTED (exact exhaustion classification in `client.go` `contentionError`; `client_attempt4_test.go` `TestUnusablePoolIsNotFoundWithoutWrites` asserts not-found results and no writes, but does not assert this reason).

- **AC-20**: **Given** a storage error being translated for a failed client operation, **When** it matches `storage.ErrNotFound`, **Then** the caller receives an `OpError` matching the library's `ErrNotFound` with the supplied operation and resource context; an error not matching `storage.ErrNotFound` is returned unchanged. Sentinel definitions belong to BASE-0006 error-contract and BASE-0010 storage-contract.
  - Evidence: UNTESTED (general translation contract in `client.go` `mapStorageError`).

- **AC-21**: **Given** a child-pool creation request whose parent read fails with access denied or cancellation, **When** the failure is returned, **Then** it still matches the original storage or context error and is not converted into a `parent_carve_missing` conflict.
  - Evidence: `client_attempt3_test.go` `TestCreatePoolParentReadErrorsRemainUnchanged` / `access denied` and `canceled`.

- **AC-22**: **Given** an allocation write reporting `storage.ErrUnknownOutcome` together with a failed precondition, while the stored allocation under the requested ID describes a different request, **When** the client resolves that result, **Then** it reports an `id_reused` conflict rather than treating the other allocation as confirmation of success. Allocation idempotency is defined by BASE-0003 allocations.
  - Evidence: `client_gate_regression_test.go` `TestUnknownOutcomeDifferentRequestIsNotSuccess`.

- **AC-23**: **Given** create and allocation requests that began against a child pool before another client deleted it, **When** the stale requests resume and the parent allocates available space, **Then** neither a successful stale allocation nor any successfully created child overlaps that parent allocation.
  - Evidence: `client_attempt4_test.go` `TestStaleCreateAndAllocateAfterDeleteNeverOverlap`.

- **AC-24**: **Given** a policy update begun against a pool that is deleted and recreated at a policy-write boundary, **When** the original update continues, **Then** it does not overwrite either of the replacement pool's documents, including after a lost write response. Policy semantics belong to BASE-0002 allocation-policy.
  - Evidence: `client_review_regression_test.go` `TestUpdatePoolPolicyDoesNotModifyReplacement` / `between phases`, `strategy retry`, `strategy unknown outcome`, and `cooldown retry`.

- **AC-25**: **Given** a child creator whose in-flight reservations document is removed by a concurrent delete before publication completes, **When** creation resumes, **Then** a one-attempt client returns the publication-exhaustion error specified by BASE-0006 error-contract AC-28 and removes its child state and carve-out; with an eight-attempt budget and no further interference it retries successfully using a fresh incarnation and leaves a usable child backed by exactly its own carve-out.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolPostCheckAbortsAfterReservationsDeleted` / `attempts=1` and `attempts=8`.

- **AC-26**: **Given** two child creators racing for the same ID, **When** one loses creation and rolls back, **Then** it releases only its own carve-out, preserving the winner's carve-out and preventing a later parent allocation from overlapping the winning child.
  - Evidence: `client_attempt4_test.go` `TestChildCreateLoserReleasesOnlyItsOwnCarveOut`.

- **AC-27**: **Given** an interrupted delete leaving residue for one pool, **When** a healthy client lists pools, gets an unrelated pool, or allocates from that unrelated pool, **Then** those operations can still succeed without first repairing the interrupted pool.
  - Evidence: `client_attempt3_test.go` `TestOtherPoolsUnaffectedByCrashedDeleteResidue`.

- **AC-28**: **Given** a writer failing before or after a storage call during create, delete or allocation, or conditional writes returning unknown outcomes during concurrent work, **When** a healthy client subsequently operates on the reachable state, **Then** safe progress remains possible: it can use a usable pool or recover incomplete state through the explicit delete-and-recreate procedure, without violating allocation and carve-out ownership.
  - Evidence: `client_attempt4_test.go` `TestCrashAtEveryStoreCallThenHealthyRecovery`; `client_model_test.go` `TestLifecycleStressMemoryNoLocks` (injects unknown outcomes before and after writes, then checks invariants and healthy progress).

- **AC-29**: **Given** a client asked to create a self-parented pool or concurrently create two pools that name one another as parents, **When** those requests run, **Then** they return without deadlocking the client; the self-parented request fails with `ErrInvalidArgument`.
  - Evidence: `client_gate_regression_test.go` `TestCreatePoolRejectsSelfParentWithoutDeadlock`; `client_gate_regression_test.go` `TestCreatePoolMutualParentsNeverDeadlock`.

- **AC-30**: **Given** an allocation, release, metadata update, policy update or pool deletion whose conditional update or delete fails with `storage.ErrPreconditionFailed` without also matching `storage.ErrUnknownOutcome`, **When** another attempt is allowed and the context remains live, **Then** the client waits according to its backoff settings, reads the current state and re-evaluates the requested change against that state rather than replaying a write based on the rejected version. If the retry budget is exhausted instead, it reports contention under AC-5.
  - Evidence: UNTESTED (general retry contract in `allocation.go` `allocateWithStatus`, `allocateAttempt`, `release` and `UpdateAllocationMetadata`; `pool.go` `UpdatePoolMetadata`, `updatePoolStrategy`, `updatePoolCooldown`, `DeletePool` and `deletePoolDocument`).

- **AC-31**: **Given** a conditional storage write for a pool or allocation operation that returns an ordinary backend error, such as `storage.ErrAccessDenied`, without matching `storage.ErrPreconditionFailed`, `storage.ErrUnknownOutcome` or `storage.ErrNotFound`, **When** the client handles that failure, **Then** the operation fails with the backend cause preserved and does not schedule another attempt or a retry wait for that write. Backend-internal retry behaviour belongs to BASE-0010 storage-contract and its backend specs.
  - Evidence: UNTESTED (terminal write-error branches in `allocation.go` `allocateAttempt`, `release` and `UpdateAllocationMetadata`; `pool.go` `createOwnDocument`, `UpdatePoolMetadata`, `updatePoolStrategy`, `updatePoolCooldown` and `deletePoolDocument`).

- **AC-32**: **Given** an `Allocate` call with an empty allocation ID and a configured ID generator returning a valid ID, **When** conditional-write failures require retries, **Then** the generator is invoked once for that call and the same generated ID is used throughout its attempts.
  - Evidence: UNTESTED (public contract in `allocation.go` `Allocate`; generation precedes the attempt loop in `allocateWithStatus`).

- **AC-33**: **Given** an allocation write that reports `storage.ErrUnknownOutcome`, **When** the subsequent read finds an allocation under the requested ID with the same canonical allocation request, **Then** `Allocate` returns that observed allocation successfully. A different request remains the conflict described by AC-22; ordinary idempotency belongs to BASE-0003 allocations.
  - Evidence: `client_test.go` `TestAllocateUnknownOutcomeObservesCommittedWrite` (successful return, requested CIDR, one conditional write and one stored allocation).

- **AC-34**: **Given** a release write that reports `storage.ErrUnknownOutcome`, **When** a subsequent read of a usable pool confirms that the allocation ID is absent, **Then** `Release` returns success.
  - Evidence: `client_lifecycle_test.go` `TestReleaseUnknownOutcomeObservesCommittedDelete` (successful return and no remaining allocation).

- **AC-35**: **Given** a pool creation whose storage create reports `storage.ErrUnknownOutcome` without also reporting `storage.ErrPreconditionFailed`, **When** the confirmation read finds the document carrying this call's incarnation, **Then** `CreatePool` accepts that document as its own completed write and continues publication; if the remaining publication and verification succeed, it returns the created pool rather than failing merely because the write response was lost. Pool publication and incarnation pairing belong to BASE-0001 pool-lifecycle.
  - Evidence: UNTESTED (confirmation in `pool.go` `createOwnDocument`, with completion in `publishPool`, `verifyPublishedPool` and `CreatePool`).

- **AC-36**: **Given** deletion of an empty top-level pool whose conditional document deletion reports `storage.ErrUnknownOutcome` without also matching `storage.ErrPreconditionFailed`, **When** the confirmation read reports that document absent and all remaining deletion work succeeds, **Then** `DeletePool` returns success rather than treating the lost response as a failed deletion.
  - Evidence: UNTESTED (`pool.go` `deletePoolDocument`, `deletePoolAttempt` and `DeletePool`).

- **AC-37**: **Given** an `UpdatePoolMetadata` write that reports `storage.ErrUnknownOutcome`, **When** a subsequent read decodes the pool with the requested description and equal labels, **Then** the metadata update returns success.
  - Evidence: UNTESTED (`pool.go` `UpdatePoolMetadata`).

- **AC-38**: **Given** an `UpdateAllocationMetadata` write that reports `storage.ErrUnknownOutcome`, **When** a subsequent allocation lookup returns the requested description and equal labels, **Then** the metadata update returns success.
  - Evidence: UNTESTED (`allocation.go` `UpdateAllocationMetadata`).

- **AC-39**: **Given** an `UpdatePoolPolicy` strategy write that reports `storage.ErrUnknownOutcome`, **When** a subsequent read finds the requested strategy in the pool's original incarnation, **Then** the client accepts the strategy phase as applied and completes the requested policy update if the cooldown phase succeeds. Policy phases belong to BASE-0002 allocation-policy.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyUnknownOutcome` / `strategy landed` (successful return and requested policy).

- **AC-40**: **Given** an `UpdatePoolPolicy` cooldown write that reports `storage.ErrUnknownOutcome`, **When** a subsequent read finds the requested cooldown in the pool's original incarnation, **Then** the policy update returns success.
  - Evidence: `updatepolicy_test.go` `TestUpdatePoolPolicyUnknownOutcome` / `cooldown landed` (successful return and requested policy).

- **AC-41**: **Given** any of the writes described in AC-33 through AC-40 reports an unknown outcome on its final allowed attempt, excluding a document deletion that also matches `storage.ErrPreconditionFailed`, **When** its operation-specific confirmation condition is satisfied and any remaining operation phases succeed, **Then** the operation returns success without requiring an additional attempt; exhausting the count alone does not override successful confirmation.
  - Evidence: UNTESTED (confirmation precedes exhaustion checks in `allocation.go` `allocateAttempt`, `allocateWithStatus`, `release` and `UpdateAllocationMetadata`; `pool.go` `createOwnDocument`, `CreatePool`, `deletePoolDocument`, `DeletePool`, `UpdatePoolMetadata`, `updatePoolStrategy` and `updatePoolCooldown`). The tests cited in AC-33, AC-34, AC-39 and AC-40 do not exercise the final-attempt boundary.

- **AC-42**: **Given** an allocation write that reports `storage.ErrUnknownOutcome`, **When** confirmation returns `ErrNotFound` for the allocation or pool, **Then** the client waits and starts a fresh attempt with the same allocation ID if the budget permits; on the final allowed attempt it instead returns the exhaustion error specified by BASE-0006 error-contract AC-29. If confirmation returns another error, the client returns that error without a retry wait for the ambiguous write.
  - Evidence: UNTESTED (`allocation.go` `allocateAttempt`, `observeAllocation` and `allocateWithStatus`; `client.go` `contentionError`).

- **AC-43**: **Given** a release write that reports `storage.ErrUnknownOutcome`, **When** confirmation still finds the allocation present or reports an `incarnation_mismatch` conflict, **Then** the client waits and starts a fresh attempt if the budget permits; on the final allowed attempt it instead returns the exhaustion error specified by BASE-0006 error-contract AC-29. Other confirmation-read errors, including `ErrNotFound`, are returned without that retry wait.
  - Evidence: UNTESTED (`allocation.go` `release`; `client.go` `contentionError`).

- **AC-44**: **Given** `CreatePool` cannot establish ownership of its reservations document because the create reports `storage.ErrPreconditionFailed` (including an error also matching `storage.ErrUnknownOutcome`), or because an unknown-outcome confirmation finds the document absent or carrying another incarnation, **When** the attempt ends in that incomplete-create condition, **Then** the client waits and re-reads pool state if the budget permits; if that condition remains through exhaustion it returns the exhaustion error specified by BASE-0006 error-contract AC-26, rather than treating the ambiguous create as success. A confirmation read or decode error instead ends creation with that error.
  - Evidence: UNTESTED (`pool.go` `createOwnDocument`, `publishPool`, `createPoolAttempt`, `CreatePool` and `createRetryError`).

- **AC-45**: **Given** `CreatePool` owns its reservations but cannot establish ownership of the pool document because its create reports `storage.ErrPreconditionFailed` (including an error also matching `storage.ErrUnknownOutcome`), or because an unknown-outcome confirmation finds the pool document absent or carrying another incarnation, **When** cleanup of this call's reservations succeeds, **Then** it waits and re-reads pool state if the budget permits; on the final allowed attempt it instead returns the exhaustion error specified by BASE-0006 error-contract AC-28. A confirmation read, decode or cleanup error instead ends creation with that error, preserving combined causes when both confirmation and cleanup fail.
  - Evidence: UNTESTED (`pool.go` `createOwnDocument`, `publishPool`, `createPoolAttempt`, `CreatePool` and `createRetryError`).

- **AC-46**: **Given** a pool document deletion that reports `storage.ErrUnknownOutcome` without also matching `storage.ErrPreconditionFailed`, **When** confirmation still finds the target document present, **Then** `DeletePool` waits and re-reads current pool state if the budget permits; on the final allowed attempt it instead returns the exhaustion error specified by BASE-0006 error-contract AC-29. A confirmation-read error other than storage not-found ends the operation with that error rather than confirming success.
  - Evidence: UNTESTED (`pool.go` `deletePoolDocument`, `deletePoolAttempt` and `DeletePool`; `client.go` `contentionError`).

- **AC-47**: **Given** a pool or allocation metadata write that reports `storage.ErrUnknownOutcome`, **When** confirmation does not establish the requested description and labels, whether because the values differ or the confirmation read or decode fails, **Then** the client waits and starts a fresh attempt if the budget permits; on the final allowed attempt it instead returns the exhaustion error specified by BASE-0006 error-contract AC-29. Any terminal error encountered by a subsequent fresh attempt is returned normally rather than being ignored.
  - Evidence: UNTESTED (`pool.go` `UpdatePoolMetadata`; `allocation.go` `UpdateAllocationMetadata`; `client.go` `contentionError`).

- **AC-48**: **Given** either policy-write phase reports `storage.ErrUnknownOutcome`, **When** confirmation does not establish both the requested phase value and the original incarnation, including a failed confirmation read or decode, **Then** the client waits and starts a fresh attempt of that phase if the budget permits; on the final allowed attempt it instead returns the exhaustion error specified by BASE-0006 error-contract AC-29. A later fresh attempt still rejects a replacement incarnation under AC-24; any partial-update error context follows BASE-0002 allocation-policy.
  - Evidence: UNTESTED (`pool.go` `updatePoolStrategy`, `updatePoolCooldown`, `retryPolicyWrite` and `UpdatePoolPolicy`; `client.go` `contentionError`). `updatepolicy_test.go` `TestUpdatePoolPolicyUnknownOutcome` / `strategy dropped` and `cooldown dropped` assert eventual success after one uncommitted unknown outcome, but do not assert exhaustion.

- **AC-49**: Withdrawn (maintainer confirmed the observed precondition-before-unknown-outcome ordering in DeletePool is a bug; see appvia/ipam#29).

- **AC-50**: **Given** a prefix-length allocation request that read pool state before its pool was deleted and recreated with sufficient capacity, **When** the request resumes with attempts remaining and a live context, **Then** it retries and succeeds with a CIDR inside the replacement pool rather than the deleted pool.
  - Evidence: `client_lifecycle_test.go` `TestPoolIncarnationPreventsABAOnMemoryAndLocal` / `memory` and `local`.

- **AC-51**: **Given** a child-pool creation request with attempts remaining and no further interference, **When** the request encounters a lost creation race, **Then** `CreatePool` retries and later succeeds, returning the created pool with a fresh incarnation, backed by only its own carve-out and usable for allocations. Non-retriable publication failures belong to BASE-0001 pool-lifecycle AC-35.
  - Evidence: `client_attempt4_test.go` `TestCreatePoolPostCheckAbortsAfterReservationsDeleted/attempts=8`.

## Non-functional requirements *(mandatory)*

- **NFR-1**: The default retry budget is **8 attempts** for a retriable conditional operation, including the initial attempt. This is an attempt-count limit, not a limit on total storage calls or elapsed time of a multi-phase public method.
  - Source: `client.go` `defaultMaxAttempts`, `New` and `attemptsExhausted`.

- **NFR-2**: With default positive backoff settings, the first retry's delay window is **0 to 10 milliseconds**, inclusive; successive windows double up to a maximum of **250 milliseconds**. The requested delay, not wall-clock scheduling latency, is bounded.
  - Source: `client.go` `defaultBackoffBase`, `defaultBackoffCap` and `waitRetry`.

- **NFR-3**: The memory-backend regression check requires one uncontended allocation from a pool containing **5,000 allocations** to complete in **at most 10 seconds**. This is the enforced test budget for that scenario, not a latency guarantee for remote backends or contended requests.
  - Source: `client_attempt3_test.go` `TestAllocateLatencyAndCallCountAtFiveThousand`.

- **NFR-4**: In that uncontended **5,000-allocation** regression scenario, one allocation uses **2 Get calls**, **1 PutIfMatch call**, and **0 CreateIfAbsent, DeleteIfMatch or List calls**.
  - Source: `client_attempt3_test.go` `TestAllocateLatencyAndCallCountAtFiveThousand`.

## Key entities *(include if the feature involves data)*

- **Client**: A library caller's handle to a storage backend, configured with retry limits, backoff, time, entropy, waiting and allocation-ID dependencies. Independent clients may share a backend.
- **Attempt budget**: The configured count controlling retriable conditional work. A positive value is finite; zero removes the count limit. It does not impose a request deadline.
- **Retry delay**: A requested wait sampled within the configured exponential full-jitter window and performed through a cancellable sleeper. A supplied `Sleeper` must return promptly when its context completes; the client does not impose a separate timeout on that callback.
- **Operation context**: The caller's cancellation and deadline signal, used by storage requests, retry waits and same-pool coordination.
- **Pool incarnation**: The identity distinguishing one lifetime of a pool from another with the same pool ID. Pool ownership and stored representation are specified by BASE-0001 pool-lifecycle and BASE-0007 document-format.
- **Conditional write outcome**: An acknowledged update, failed precondition, or unknown result that may already have changed storage. The storage-side meanings belong to BASE-0010 storage-contract.
- **Contention result**: An operation error identifying exhausted retry work and preserving causes for programmatic matching. The general error API belongs to BASE-0006 error-contract.

## Out of scope *(mandatory)*

- Ordinary create, get, list, metadata update and delete behaviour, incarnation pairing, residue shapes and operator recovery procedures: see BASE-0001 pool-lifecycle. This spec covers their concurrent safety and retry outcomes only.
- Allocation strategies, cooldown selection and expiry, policy changes and their non-atomic phases: see BASE-0002 allocation-policy.
- Allocation selection, supplied-ID idempotency, lookup, release and metadata update semantics: see BASE-0003 allocations.
- Pool IDs, metadata validation, CIDR canonicalisation and UUIDv7 validation: see BASE-0005 input-validation.
- General sentinel definitions, `OpError` formatting and partial-list errors: see BASE-0006 error-contract.
- Stored key names, JSON schemas and incarnation encoding: see BASE-0007 document-format.
- Backend CAS guarantees, opaque versions, backend access-propagation retry opt-in and storage error production: see BASE-0010 storage-contract. Local, S3 and Azure implementation details belong to BASE-0011 local-backend, BASE-0012 s3-backend and BASE-0013 azblob-backend.
- A distributed application lock, lease service, background repair service, automatic adoption of incomplete pools, or guaranteed completion under perpetual contention.
- An automatic wall-clock timeout for unlimited attempts, or a production latency or throughput guarantee derived from stress-test worker counts, seed counts or operation counts.

## Open questions *(mandatory, empty before G2)*

- None. Questions raised during the baseline run were answered by the maintainer on 2026-11-03 and moved to issues: appvia/ipam#28, appvia/ipam#29.
