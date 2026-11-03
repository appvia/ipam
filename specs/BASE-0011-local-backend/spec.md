# Feature Specification: Local directory storage backend

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0011-local-backend/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Persist objects on a local disk (Priority: P1)

A library caller opens a directory with `local.New`, passes the returned store to a consumer of `storage.Storage`, and persists objects as ordinary files. The caller can reopen the same directory to access those files and closes each store when finished. Object versions are derived from the stored bytes. The shared read, conditional-write, conditional-delete, error and prefix-listing semantics are specified in BASE-0010 storage-contract; this spec covers their local filesystem realization.

**Why this priority**: Development, tests and single-host applications can persist state without provisioning an object-storage service.

### Journey 2 - Coordinate independent local writers (Priority: P2)

Library callers in multiple goroutines or processes open the same directory and perform conditional operations. Callers retry rejected conditional updates according to their own policy. A caller starting against a fresh directory does not need to create or clean up the backend's `.lock` file first.

**Why this priority**: Separate processes on one host need to share state without losing successful updates.

### Journey 3 - Diagnose and contain filesystem access (Priority: P2)

A library caller receives recognizable invalid-key or filesystem-access errors, lists ordinary objects without backend temporary files, and inspects the backend's data-protection report. The operator keeps the directory on a local disk and does not remove `.lock` while the store is in use.

**Why this priority**: Callers need predictable failures and an accurate account of the backend's operating limits.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a nonempty directory path whose missing directories can be created on a local disk, **When** a caller invokes `local.New`, **Then** the directory is created and a usable store is returned.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `New`)

- **AC-2**: **Given** an open store and an absent key such as `pools/tenant-a.json`, **When** the caller creates that object, **Then** the file at the corresponding path under the store directory contains exactly the supplied bytes, without a backend envelope, and the returned version is the lowercase hexadecimal SHA-256 digest of those bytes.
  - Evidence: `storage/local/local_test.go` `TestStoredFileIsPlainContentAtPath`

- **AC-3**: **Given** independent processes using the same store directory and retrying conditional updates rejected with `storage.ErrPreconditionFailed`, **When** they read and increment the same stored counter, **Then** every successful increment is reflected in the final value.
  - Evidence: `storage/local/local_test.go` `TestCrossProcess`

- **AC-4**: **Given** a fresh directory and concurrent callers creating the same absent key, **When** those callers use the store from goroutines or independently opened processes, **Then** exactly one creation succeeds and the other creations fail with `storage.ErrPreconditionFailed`.
  - Evidence: `storage/local/local_test.go` `TestFirstOperationsOnFreshDirectoryDoNotFail`; `storage/local/local_test.go` `TestFreshDirectoryCrossProcess`

- **AC-5**: **Given** a store on a supported local filesystem, **When** a caller creates or replaces an object while another caller reads its path, **Then** readers do not observe a partially written object; replacement exposes complete old or complete new content.
  - Evidence: UNTESTED (behaviour documented in `storage/local/local.go` package comment and implemented in `write`)

- **AC-6**: **Given** ordinary object files alongside a root `.lock` file, files whose basenames begin with `.ipam-tmp-`, and symlinks to files or directories, **When** a caller lists the store with an empty prefix, **Then** the result includes the ordinary files but omits those lock, temporary and symlink entries, including objects accessible only through a symlinked directory.
  - Evidence: `storage/local/local_test.go` `TestStaleStateIsIgnored`; `storage/local/local_test.go` `TestSymlinkEscapeIsBlocked` (symlink test skipped on Windows); in-root relative symlink listing UNTESTED (behaviour in `storage/local/local.go` `List`)

- **AC-7**: **Given** a local store used through `storage.Inspector`, **When** a caller invokes `Inspect`, **Then** the returned report has `Versioning: storage.Unknown`, `SoftDelete: storage.Unknown` and `HierarchicalNamespace: storage.No`, with no error. See BASE-0010 storage-contract for the report's shared meanings.
  - Evidence: `storage/local/inspect_test.go` `TestInspectReportsNoDataProtection`

- **AC-8**: **Given** an open store that the caller has finished using, **When** the caller invokes `Close`, **Then** the directory handle is released and any error from closing that handle is returned. The store must not be used afterwards; on Windows, closing releases the handle that otherwise prevents directory deletion.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `Close` and its documentation)

### Edge cases

- **AC-9**: **Given** an empty directory argument or a filesystem failure while creating or opening the requested directory, **When** a caller invokes `local.New`, **Then** construction fails with an error and no store; a filesystem creation or opening failure retains its underlying error for `errors.Is` or `errors.As`.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `New`)

- **AC-10**: **Given** an empty or absolute key, a key containing a NUL byte, an empty, `.` or `..` path segment, a segment beginning with `.ipam-tmp-`, or the exact key `.lock`, **When** a caller invokes `Get`, `CreateIfAbsent`, `PutIfMatch` or `DeleteIfMatch`, **Then** the call fails with an error matching `local.ErrInvalidKey`. Only the root `.lock` is reserved: a nested key such as `tenant-a/.lock` is an ordinary valid key and is returned by `List` when it names an object and matches the prefix.
  - Evidence: `storage/local/local_test.go` `TestInvalidKeys`; nested `.lock` behaviour UNTESTED (behaviour in `storage/local/local.go` `validateKey` and `List`)

- **AC-11**: **Given** a key containing a backslash or colon, **When** a caller invokes any of the four key-based object operations, **Then** the call fails with an error matching `local.ErrInvalidKey`, even on a host where the filesystem would otherwise permit that spelling.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `validateKey`, called by `Get`, `CreateIfAbsent`, `PutIfMatch` and `DeleteIfMatch`)

- **AC-12**: **Given** a file or directory symlink inside the store that leads outside its root, **When** a caller tries to read through that symlink, create an object through the directory symlink, or delete an outside object through it, **Then** the call fails and does not create or delete the outside files. The tested directory-symlink read and create calls return an error matching `local.ErrInvalidKey`; the file-symlink read is tested only for failure. The underlying OS error is included only as text and cannot be reached through `errors.Is` or `errors.As`. Creating at an existing symlink itself, rather than through a directory symlink, is covered by AC-30.
  - Evidence: `storage/local/local_test.go` `TestSymlinkEscapeIsBlocked`; `storage/local/local_test.go` `TestEscapeAndOverlongKeysAreInvalidKeys` (both skipped on Windows); error-chain behaviour UNTESTED (behaviour in `storage/local/local.go` `mapError`)

- **AC-13**: **Given** an object name that exceeds the host filesystem's supported name length, **When** a caller tries to create it, **Then** the call fails with an error matching `local.ErrInvalidKey` on Unix hosts reporting `syscall.ENAMETOOLONG`, and a generic non-sentinel error on Windows. On Unix, the underlying OS error is included only as text and cannot be reached through `errors.Is` or `errors.As`; on Windows, the underlying OS cause is preserved in the error chain. The backend does not impose a separate portable numeric name-length limit.
  - Evidence: `storage/local/local_test.go` `TestEscapeAndOverlongKeysAreInvalidKeys` (Unix creation using a 300-character component; skipped on Windows); Windows and error-chain behaviour UNTESTED (behaviour in `storage/local/local.go` `mapError`; Windows name-length failures do not match `syscall.ENAMETOOLONG`)

- **AC-14**: **Given** an existing object beneath a directory that the process lacks permission to access, **When** a caller invokes `Get`, **Then** the call fails with an error matching `storage.ErrAccessDenied`.
  - Evidence: `storage/local/local_test.go` `TestPermissionDeniedMapsToAccessDenied` (skipped on Windows and for a process running as root)

- **AC-15**: **Given** a `.lock` file that is absent before an operation or contains stale data with no live holder, **When** callers open the store and perform conditional operations, **Then** ordinary operations can succeed without manually creating or cleaning the lock file. Pre-existing temporary files do not prevent those operations, and successful create and replacement calls leave no additional temporary files behind.
  - Evidence: `storage/local/local_test.go` `TestMissingLockFileIsTolerated`; `storage/local/local_test.go` `TestStaleStateIsIgnored`

- **AC-16**: **Given** the `.lock` file is replaced after it is opened but before the lock is obtained, **When** a caller constructs a store or performs an otherwise valid `CreateIfAbsent`, `PutIfMatch` or `DeleteIfMatch` operation and replacement stops before the lock-acquisition attempt limit, **Then** lock acquisition retries against the current lock file and construction or the conditional operation succeeds.
  - Evidence: `storage/local/lock_internal_test.go` `TestAcquireRetriesWhenLockFileReplacedBeforeLocking` (construction only; skipped on Windows); conditional-operation behaviour UNTESTED (behaviour in `storage/local/local.go` `locked` and `acquire`)

- **AC-17**: **Given** an outside actor replaces `.lock` on every acquisition attempt, **When** store construction or a `CreateIfAbsent`, `PutIfMatch` or `DeleteIfMatch` operation exhausts its 100 lock-acquisition attempts, **Then** the call returns a nonnil error rather than succeeding, and the error does not misleadingly report a `<nil>` cause. The give-up error matches no sentinel and includes its last cause only as text, without wrapping it for `errors.Is` or `errors.As`.
  - Evidence: `storage/local/lock_internal_test.go` `TestAcquireGiveUpErrorDoesNotReportNil` (construction only; skipped on Windows); conditional-operation and error-chain behaviour UNTESTED (behaviour in `storage/local/local.go` `locked` and `acquire`)

- **AC-18**: **Given** `.lock` is a directory rather than a lock file, **When** a caller invokes `local.New`, **Then** construction returns an error and a nil store.
  - Evidence: `storage/local/lock_internal_test.go` `TestNewFailsWhenLockPathIsDirectory`

- **AC-19**: Withdrawn (maintainer: directory-only key handling is a bug; see OQ-5)

- **AC-20**: **Given** an object and its current version, **When** a caller successfully replaces it with identical bytes, **Then** the returned version is identical to the previous version. A version identifies content, not a unique write or object incarnation.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `PutIfMatch`, `check` and `versionOf`)

- **AC-21**: **Given** a valid key where required and a context already canceled or past its deadline, **When** a caller starts `Get`, `CreateIfAbsent`, `PutIfMatch`, `DeleteIfMatch` or `List`, **Then** the call returns the context error before performing the read, conditional mutation or directory traversal. The four key-based operations validate the key before checking the context, so an invalid key with a canceled context returns `local.ErrInvalidKey`. If the context ends during `List`, traversal stops when cancellation is checked after processing an ordinary object file, and the returned error wraps the context error so `errors.Is` holds.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `Get`, `CreateIfAbsent`, `PutIfMatch`, `DeleteIfMatch`, `locked` and `List`)

- **AC-22**: **Given** a filesystem error encountered while accessing an object or traversing the store, other than an escaping path or a Unix overlong-name error, **When** the operation fails, **Then** its error preserves the underlying filesystem cause; missing-path and permission errors additionally match `storage.ErrNotFound` and `storage.ErrAccessDenied`, respectively. Other filesystem errors are returned wrapped rather than converted to either sentinel. Escaping-path and Unix overlong-name mappings are excluded because they retain the OS cause only as text, as described in AC-12 and AC-13.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `mapError`)

- **AC-23**: **Given** an object has been successfully installed at its destination but the filesystem cannot open or synchronize its containing directory, **When** the conditional write completes, **Then** that directory-synchronization failure does not turn the write into an error. Synchronization of the object file itself must succeed before installation.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `write` and `syncDir`)

- **AC-24**: **Given** a non-regular filesystem entry other than a directory or symlink is present under the store root, **When** a caller lists the store, **Then** that entry is omitted from the returned object keys.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `List`)

- **AC-25**: **Given** a process holds the store lock and then crashes, **When** another process uses the same directory without deleting or replacing `.lock`, **Then** the crashed process does not retain the lock; the operating system releases it, allowing other callers to proceed.
  - Evidence: UNTESTED (behaviour documented in `storage/local/local.go` package comment and implemented through `storage/local/lock_unix.go` `lockFile` and `storage/local/lock_windows.go` `lockFile`)

- **AC-26**: Withdrawn (in-root symlinks are unsupported per maintainer; see OQ-3)

- **AC-27**: Withdrawn (in-root symlinks are unsupported per maintainer; see OQ-3)

- **AC-28**: Withdrawn (in-root symlinks are unsupported per maintainer; see OQ-3)

- **AC-29**: **Given** a file symlink with an absolute target, even one pointing inside the store root, **When** a caller invokes `Get`, `PutIfMatch` or `DeleteIfMatch` using the symlink's key, **Then** the call fails with an error matching `local.ErrInvalidKey`; the underlying OS error is included only as text.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `read`, `check` and `mapError`) (Unix hosts; Windows symlink behaviour unverified)

- **AC-30**: **Given** an existing file symlink, including a dangling link or one whose target escapes the store root, **When** a caller invokes `CreateIfAbsent` at the symlink's key, **Then** the call fails with `storage.ErrPreconditionFailed`, not `local.ErrInvalidKey`, without replacing the link.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `CreateIfAbsent`) (Unix hosts; Windows symlink behaviour unverified)

- **AC-31**: **Given** the `.lock` file cannot be opened or locked for a reason other than absence or a concurrent creation race, **When** a caller invokes `local.New`, `CreateIfAbsent`, `PutIfMatch` or `DeleteIfMatch`, **Then** the call fails using the filesystem-error mapping described in AC-22; a permission failure matches `storage.ErrAccessDenied` and preserves its underlying cause. Failed construction returns no store, and a conditional operation does not mutate the object.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `New`, `locked`, `acquire`, `openLockFile` and `mapError`)

- **AC-32**: **Given** an already open store with readable objects and a traversable directory, but its `.lock` file is held by another caller or cannot be opened or locked, **When** a caller invokes `Get` or `List`, **Then** the read or listing does not acquire or wait for the store lock and does not fail solely because that lock is held or unavailable.
  - Evidence: UNTESTED (behaviour in `storage/local/local.go` `Get`, `read` and `List`)

## Non-functional requirements *(mandatory)*

- **NFR-1**: Acquisition permits at most **100 attempts** to obtain a usable current lock file before failing. This bounds retries for lock-file creation or replacement races, not elapsed waiting time for a held lock. Source: `storage/local/local.go` `lockAttempts` and `acquire`.
- **NFR-2**: Each content-derived version represents **256 bits** as **64 lowercase hexadecimal characters**. Source: `storage/local/local.go` `versionOf`.
- **NFR-3**: Newly created object and lock files request Unix permission mode **0644** (**9 permission bits**, subject to the process umask). Source: `storage/local/local.go` `write` and `openLockFile`.
- **NFR-4**: Newly created store and object-parent directories request Unix permission mode **0755** (**9 permission bits**, subject to the process umask). Source: `storage/local/local.go` `New` and `write`.
- **NFR-5**: The documented supported deployment is **1 host** using a local disk. The constructor does not detect or reject network or cloud-synchronized filesystems; those filesystems are outside the documented CAS guarantee. Source: `storage/local/local.go` package comment and `New` documentation.

## Key entities *(include if the feature involves data)*

- **Local store**: An open handle to a caller-selected directory, shared by callers on one host and explicitly closed after use.
- **Object**: A relative slash-separated key and its verbatim byte content, stored at the corresponding path beneath the directory.
- **Symlink entry**: An entry omitted by `List`; absolute-target and existing-link creation cases are covered by AC-29 and AC-30.
- **Content version**: The object's SHA-256 content digest returned as a `storage.Version`. Callers round-trip it according to BASE-0010 storage-contract; equal content produces equal versions, including when content returns to a previous value.
- **Backend coordination and temporary files**: The root `.lock` file and temporary files beginning with `.ipam-tmp-`, which are not ordinary objects. The backend never deletes `.lock` itself.
- **Data-protection report**: The `storage.Info` values returned by `Inspect`; the shared report type is owned by BASE-0010 storage-contract.

## Out of scope *(mandatory)*

- Shared `storage.Storage` semantics, sentinel definitions, inspector types, access-propagation retry opt-in, the memory backend and the reusable backend contract suite: see BASE-0010 storage-contract.
- Backend selection and `file://` URL parsing or validation: see BASE-0009 backend-url.
- Remote S3 and Azure Blob behavior, cloud credentials and service-specific conditional requests: see BASE-0012 s3-backend and BASE-0013 azblob-backend.
- Pool documents, allocations, pool lifecycle, application-level retry policies and CLI presentation.
- Distributed or multi-host coordination, including guarantees on NFS, SMB, FUSE mounts or cloud-synchronized folders.
- Safe coordination if an outside actor deletes or replaces `.lock` while a holder already owns it. The package documents that separate holders can then lose updates silently; detection after acquisition does not close that window.
- Retained object versions, soft-delete recovery, unique version tokens for each write, and a guarantee of persistence through every crash or power-loss scenario. Directory synchronization is best effort.

## Open questions *(mandatory, empty before G2)*

- **OQ-1**: `storage/local/local.go` `locked` checks the context only before acquisition, and the blocking calls in `storage/local/lock_unix.go` `lockFile` and `storage/local/lock_windows.go` `lockFile` do not consume a context. An operation that began with a live context can therefore keep waiting after cancellation or expiry and mutate after acquiring the lock. Maintainer answer: this is a bug; waiting for the lock should honour cancellation. A Bug citing this spec is to be raised after merge, and this entry is then removed.
- **OQ-2**: `storage/local/local.go` `List` descends into externally introduced directories whose names begin with `.ipam-tmp-` before applying its temporary-basename filter, so it can return an ordinary child such as `.ipam-tmp-stale/item` even though the key-based operations reject that key. Maintainer answer: this is a bug; `List` should skip them. A Bug citing this spec is to be raised after merge, and this entry is then removed.
- **OQ-3**: In-root relative file symlinks currently permit `Get`, `PutIfMatch` and `DeleteIfMatch` while their keys are absent from `List`. Maintainer answer: in-root symlinks are unsupported; no AC covers key-based access through them. Remove this entry once the unsupported status is documented.
- **OQ-4**: Should operations on a key beneath an existing object, such as `tenant-a/item` when `tenant-a` is an object file, follow the BASE-0010 storage-contract missing-key rule instead of returning a generic filesystem error? `Get`, `PutIfMatch`, `DeleteIfMatch` and `CreateIfAbsent` currently return a wrapped "not a directory" error matching neither `storage.ErrNotFound` nor `storage.ErrPreconditionFailed`: `storage/local/local.go` `mapError` leaves `ENOTDIR` in its default branch. Maintainer answer (2026-11-03): this is a bug; such operations should follow the BASE-0010 missing-key rule. A Bug citing this spec is to be raised after merge, and this entry is then removed.
- **OQ-5**: A key that exists only as a directory, including an empty parent left by `DeleteIfMatch`, is absent from `List` and returns `storage.ErrNotFound` from `Get`, `PutIfMatch` and `DeleteIfMatch`, but `CreateIfAbsent` returns `storage.ErrPreconditionFailed`, leaving the key uncreatable. Maintainer answer (2026-11-03): this is a bug. A Bug citing this spec is to be raised after merge, and this entry is then removed.
