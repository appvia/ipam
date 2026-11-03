# Feature Specification: Storage contract and in-memory backend

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0010-storage-contract/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Read and conditionally change stored objects (Priority: P1)

As a library caller, I create an object, read its bytes and opaque version, and supply that version when replacing or deleting it. A conflicting writer must not overwrite my newer object using an old version. I can list keys under a prefix without depending on a particular backend.

**Why this priority**: Conditional object operations are the common contract on which the IPAM client's persistence depends.

### Journey 2 - Use an isolated in-memory store (Priority: P2)

As a library caller or test author, I construct an empty in-memory store and exercise the same conditional operations without configuring an external service. I can use a store concurrently and inspect its reported data-protection properties.

**Why this priority**: An in-memory implementation makes the storage contract available without filesystem or cloud setup.

### Journey 3 - Inspect backend protection and mark access-propagation retries (Priority: P2)

As a library caller, I use the optional inspection interface to distinguish enabled, disabled and unknown backend properties. When I want a supporting backend to allow access-denied retries, I mark the operation's context explicitly.

**Why this priority**: Callers need to distinguish unavailable information from a disabled protection feature and to opt into access-propagation retry behavior.

### Journey 4 - Check a backend against the reusable contract (Priority: P2)

As a backend implementer, I provide a factory for isolated stores to the reusable contract suite. The suite checks observable read, conditional-write, delete, listing and concurrent-write behavior. For an emulator known to ignore conditional deletes, I can supply a reason to skip the affected checks.

**Why this priority**: A shared suite gives backend implementers the same behavioral checks without duplicating them.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a key that does not exist, **When** a caller uses `CreateIfAbsent` and then `Get`, **Then** creation succeeds and the read returns the supplied bytes and the same opaque `storage.Version` returned by creation.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/create then get round trips data and version` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-2**: **Given** an object created from a caller-owned byte slice, **When** the caller changes that input slice or a byte slice returned by `Get`, **Then** a subsequent read still returns the originally stored bytes.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/create then get round trips data and version` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-3**: **Given** a key that already exists, **When** a caller invokes `CreateIfAbsent` for that key, **Then** the call fails with an error matching `storage.ErrPreconditionFailed` through `errors.Is`.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/second create returns precondition failed` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-4**: **Given** an existing object and its current version, **When** a caller replaces its bytes with different bytes using `PutIfMatch`, **Then** the call succeeds, returns a different version, and a subsequent read returns the replacement bytes and that returned version.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/put with the current version returns a new version` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-5**: **Given** an existing object and its current version, **When** a caller uses `DeleteIfMatch` with that version and then creates the key again, **Then** deletion succeeds, the key is not found between deletion and recreation, and recreation returns a version different from the deleted object's version.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/delete with the current version removes the object` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-6**: **Given** stored keys both inside and outside a requested prefix, **When** a caller invokes `List`, **Then** it returns only matching keys in ascending string order, retaining the requested prefix in each returned key.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/list returns matching keys in sorted order` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-7**: **Given** an in-memory store used through `storage.Inspector`, **When** a caller invokes `Inspect`, **Then** it succeeds and reports `HierarchicalNamespace: storage.No`. (Its `Versioning` and `SoftDelete` values are not part of this contract; see appvia/ipam#37.)
  - Evidence: `storage/memory/inspect_test.go` `TestInspectReportsNoDataProtection` (asserts the hierarchical-namespace value among others).

- **AC-8**: **Given** a context without an access-propagation retry marker, **When** a caller queries `storage.AccessPropagationRetry`, **Then** the result is false.
  - Evidence: `storage/context_test.go` `TestAccessPropagationRetry_returns_false_when_not_enabled`.

- **AC-9**: **Given** a context containing an existing value, **When** a caller derives a context with `storage.WithAccessPropagationRetry`, **Then** `storage.AccessPropagationRetry` reports true for the derived context and the existing value remains available.
  - Evidence: `storage/context_test.go` `TestAccessPropagationRetry_returns_true_when_enabled`.

- **AC-10**: **Given** a caller using `storage.Inspector`, **When** the caller invokes `Inspect(ctx)`, **Then** the method returns `(storage.Info, error)`, where `storage.Info` has three independent `storage.TriState` fields named `Versioning`, `SoftDelete` and `HierarchicalNamespace`, and the zero value of `storage.TriState` is `storage.Unknown`.
  - Evidence: UNTESTED (behavior in `storage/inspect.go` `Inspector`, `Info` and `TriState`).

- **AC-11**: **Given** a `storage.TriState` value, **When** a caller obtains its string representation, **Then** `storage.Yes` becomes `"yes"`, `storage.No` becomes `"no"`, and `storage.Unknown` or any other value becomes `"unknown"`.
  - Evidence: UNTESTED (behavior in `storage/inspect.go` `TriState.String`).

- **AC-12**: **Given** the four exported storage sentinels `storage.ErrNotFound`, `storage.ErrPreconditionFailed`, `storage.ErrAccessDenied` and `storage.ErrUnknownOutcome`, **When** a caller compares them through `errors.Is`, **Then** each matches itself and none matches another sentinel.
  - Evidence: UNTESTED (behavior in `storage/errors.go` `ErrNotFound`, `ErrPreconditionFailed`, `ErrAccessDenied` and `ErrUnknownOutcome`).

- **AC-13**: Withdrawn (the exported sentinel contract is consolidated into AC-12; each backend capability specifies when it produces these errors).

- **AC-14**: **Given** a backend implementer supplying a factory that returns an isolated store for every invocation, **When** the implementer calls `storagetest.Run` or `storagetest.RunWithOptions` with zero-valued options, **Then** the same contract checks run as parallel subtests, the factory is invoked once per subtest with that subtest's `*testing.T`, and no conditional-delete checks are skipped by the suite.
  - Evidence: UNTESTED (behavior in `storage/storagetest/contract.go` `Run` and `RunWithOptions`; `TestMemoryContract` invokes the suite but does not separately assert its execution configuration).

- **AC-15**: **Given** an emulator for which the implementer supplies a non-empty `Options.SkipConditionalDelete` reason, **When** `storagetest.RunWithOptions` runs, **Then** the stale-version delete check and missing-key delete check are skipped with a skip message containing that reason, while the current-version delete check still runs; this option is documented for emulators known to ignore delete conditions, not real backends.
  - Evidence: UNTESTED (behavior in `storage/storagetest/contract.go` `Options.SkipConditionalDelete` and `RunWithOptions`).

- **AC-16**: **Given** an in-memory store with a `BeforeWrite` callback configured, **When** a caller attempts `CreateIfAbsent`, `PutIfMatch` or `DeleteIfMatch`, **Then** the callback receives the key and respectively `"create"`, `"put"` or `"delete"` before the operation checks its context and write conditions, including on attempts that subsequently fail. The callback runs without holding the store's lock, so a callback that blocks does not block other operations on the store; `Get` and `List` do not invoke it.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `Store.BeforeWrite`, `CreateIfAbsent`, `PutIfMatch` and `DeleteIfMatch`).

### Edge cases

- **AC-17**: **Given** a key that does not exist, **When** a caller invokes `Get`, **Then** the call fails with an error matching `storage.ErrNotFound` through `errors.Is`.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/get on a missing key returns not found` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-18**: **Given** a version superseded by a successful replacement, **When** a caller supplies that stale version to `PutIfMatch`, **Then** the call fails with an error matching `storage.ErrPreconditionFailed`, preserving the current bytes and current version.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/put with a stale version preserves data` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-19**: **Given** a version superseded by a successful replacement, **When** a caller supplies that stale version to `DeleteIfMatch`, **Then** the call fails with an error matching `storage.ErrPreconditionFailed` and the current object remains readable with its current bytes.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/delete with a stale version preserves the object` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-20**: **Given** a key that does not exist, **When** a caller invokes `PutIfMatch`, **Then** the reusable storage contract accepts an error matching either `storage.ErrNotFound` or `storage.ErrPreconditionFailed`, and requires that the call does not create the key.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/put on a missing key never creates it` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-21**: **Given** a missing key and another existing object, **When** a caller invokes `DeleteIfMatch` on the missing key, **Then** the reusable storage contract accepts an error matching either `storage.ErrNotFound` or `storage.ErrPreconditionFailed`, and requires that the other object remains readable with its original bytes.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/delete on a missing key does not affect other objects` (defined in `storage/storagetest/contract.go` `RunWithOptions`).

- **AC-22**: **Given** multiple writers holding the same current object version, **When** they concurrently invoke `PutIfMatch` on that object, **Then** exactly one succeeds and every other writer receives an error matching `storage.ErrPreconditionFailed`.
  - Evidence: `storage/memory/memory_test.go` `TestMemoryContract/concurrent puts with one version allow exactly one writer` (defined in `storage/storagetest/contract.go` `RunWithOptions`, exercised with 8 writers).

- **AC-23**: **Given** a missing key in an in-memory store, **When** a caller invokes `PutIfMatch` or `DeleteIfMatch`, **Then** the error specifically matches `storage.ErrNotFound`, rather than the alternative precondition error permitted by the reusable suite.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `PutIfMatch` and `DeleteIfMatch`; the reusable suite asserts only the alternatives in AC-20 and AC-21).

- **AC-24**: **Given** separately constructed stores returned by `memory.New`, **When** a caller lists a new store or changes an object in one store, **Then** each store starts empty and changes in one store do not change another store's objects.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `New`).

- **AC-25**: **Given** an in-memory store, **When** a caller lists with an empty prefix or a prefix matching no keys, **Then** the result is respectively all keys in ascending string order or an empty key list; prefix matching is literal rather than path normalization.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `List`).

- **AC-26**: **Given** a context that is already canceled or has already exceeded its deadline, **When** a caller invokes an in-memory `Get`, `CreateIfAbsent`, `PutIfMatch`, `DeleteIfMatch` or `List`, **Then** the call returns an error wrapping the context error, recognizable through `errors.Is`, without performing the requested storage mutation.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `Get`, `CreateIfAbsent`, `PutIfMatch`, `DeleteIfMatch` and `List`).

- **AC-27**: **Given** a canceled context, **When** a caller invokes the in-memory store's `Inspect`, **Then** inspection still succeeds with the report in AC-7.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `Inspect`).

- **AC-28**: **Given** a key not yet used in an in-memory store, **When** a caller creates an object with an empty key, a key containing path-like segments, or a nil or empty payload, **Then** the store does not reject the key or payload for its format and a subsequent `Get` of the same literal key succeeds and returns zero-length data for a nil or empty payload.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `CreateIfAbsent` and `Get`).

- **AC-29**: **Given** an existing in-memory object and its current version, **When** a caller replaces it using `PutIfMatch` and subsequently changes the supplied byte slice, **Then** the stored replacement bytes are unaffected.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `PutIfMatch`).

- **AC-30**: **Given** an existing in-memory object and its current version, **When** a caller successfully replaces it with identical bytes using `PutIfMatch`, **Then** the call still returns a different version.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `PutIfMatch`).

- **AC-31**: **Given** an existing in-memory object, **When** a second `CreateIfAbsent` for that key fails, **Then** the original bytes and version remain unchanged.
  - Evidence: UNTESTED (behavior in `storage/memory/memory.go` `CreateIfAbsent`; the test cited by AC-3 checks the error but does not read the object afterward).

## Non-functional requirements *(mandatory)*

- None enforced in code.

## Key entities *(include if the feature involves data)*

- **Stored object**: Bytes associated with a string key and an opaque current version. Storage methods operate on bytes rather than interpreting IPAM documents.
- **Object version (`storage.Version`)**: A token returned by reads and successful creates or replacements, supplied unchanged to conditional replacement or deletion. Callers need not interpret its contents.
- **Storage error**: A caller-visible classification for a missing object, failed write condition, denied access or unknown conditional-operation outcome. Callers identify the relevant sentinel through error matching.
- **Protection report (`storage.Info`)**: Independent versioning, soft-delete and hierarchical-namespace properties, each expressed as `Unknown`, `Yes` or `No`. `No` means disabled or unsupported; `Unknown` means the property could not be determined.
- **Access-propagation retry context**: A context carrying an explicit opt-in that supporting backends can query. The marker does not itself execute or schedule retries.
- **Contract-suite options**: Configuration for a backend implementer's tests, including an optional reason for skipping the two checks requiring rejection of stale or missing conditional-delete versions.
- **In-memory store**: A process-local, concurrency-safe implementation constructed with `memory.New`. It keeps current objects without retaining overwritten or deleted contents for recovery.

## Out of scope *(mandatory)*

- IPAM document contents, pool and allocation behavior, and application-level interpretation of stored objects.
- Client retry attempts, backoff, contention errors and recovery after an unknown write outcome; see BASE-0004 concurrency-and-retry.
- Filesystem persistence, directory restrictions, filesystem locking and local-backend version construction; see BASE-0011 local-backend.
- S3 requests, credentials, ETag handling, authorization-error retries and service-specific protection inspection; see BASE-0012 s3-backend.
- Azure Blob authentication, conditional requests, service-specific protection inspection and hierarchical-namespace rejection; see BASE-0013 azblob-backend.
- Backend URL parsing and CLI diagnostic presentation. This capability provides the storage and inspection interfaces, not those entrypoints.
- Enabling versioning or soft delete, changing backend configuration, and retrieving or restoring historical object versions. Inspection only reports properties.
- Persistent or cross-process sharing of an in-memory store, and a common backend-independent key-validation policy.
- Retry budgets, performance targets and object-size limits: none are established by the scoped interface, in-memory implementation or contract suite.

## Open questions *(mandatory, empty before G2)*

- None. Questions raised during the baseline run were answered by the maintainer on 2026-11-03 and moved to issues: appvia/ipam#37.
