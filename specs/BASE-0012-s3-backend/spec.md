# Feature Specification: S3 backend

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0012-s3-backend/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Use S3 for conditional object storage (Priority: P1)

A library caller configures an existing S3 bucket, an optional key prefix,
region, and optional S3-compatible endpoint. The caller reads objects and
uses the returned ETags for conditional updates and deletes. Listing returns
keys relative to the configured prefix. The common storage semantics are
defined in [BASE-0010 storage-contract](../BASE-0010-storage-contract/spec.md);
this specification describes their S3-specific realization.

**Why this priority**: Conditional object operations are the backend's primary
service to library callers coordinating changes through object storage.

### Journey 2 - Handle failed writes and new access grants (Priority: P2)

A library caller distinguishes a rejected condition, missing object, denied
access, and a write whose result cannot be established. When access grants
have just changed, the caller opts into bounded access-denied retries through
the context flag defined in BASE-0010 storage-contract.

**Why this priority**: Callers need an accurate failure classification before
deciding whether to reread state or retry an operation.

### Journey 3 - Inspect bucket protection settings (Priority: P2)

A diagnostic caller reads the bucket's versioning state without writing
objects. A caller without permission to inspect versioning receives an unknown
state rather than a failed inspection.

**Why this priority**: Protection reporting helps operators assess the backend
without requiring object mutations or bucket-administration permissions.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a configuration without a bucket name, **When** a caller constructs the S3 backend with `New`, **Then** construction fails.
  - Evidence: `storage/s3/s3_test.go` `TestNewValidatesConfig`

- **AC-2**: **Given** an explicit `Region` and a different region in `AWS_REGION` or shared configuration, **When** a caller constructs the backend, **Then** the explicit `Region` takes precedence.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `Config`, `New`)

- **AC-3**: **Given** a caller constructing the S3 backend, **When** AWS configuration and credentials are selected, **Then** the backend uses the AWS SDK default configuration and credential chain, with no backend-specific credential fields.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` package documentation, `Config`, `New`)

- **AC-4**: **Given** a caller constructing the backend, **When** `New` runs, **Then** construction makes no network calls, and an AWS configuration-loading failure is returned with its underlying error preserved.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `New`)

- **AC-5**: **Given** a custom S3-compatible endpoint and `UsePathStyle` enabled, **When** a caller performs an object operation, **Then** the request reaches that endpoint with the bucket in the URL path.
  - Evidence: `storage/s3/s3_test.go` `TestConditionalHeaders`

- **AC-6**: **Given** a configured key prefix of `root`, `root/`, or `/root/`, **When** a caller reads key `k`, **Then** the request addresses `root/k` inside the configured bucket.
  - Evidence: `storage/s3/s3_test.go` `TestPrefixNormalisation` subtests `root`, `root/`, `/root/`

- **AC-7**: **Given** an object response containing bytes and an ETag, **When** a caller invokes `Get`, **Then** it receives those bytes and the ETag verbatim, including its quotation marks, and the request has no conditional-read headers.
  - Evidence: `storage/s3/s3_test.go` `TestConditionalHeaders/get sends no conditional headers and returns the ETag verbatim`

- **AC-8**: **Given** a caller creating an object, **When** `CreateIfAbsent` succeeds, **Then** the backend sends the supplied data in a PUT with `If-None-Match: *`, no `If-Match`, and returns the response ETag verbatim.
  - Evidence: `storage/s3/s3_test.go` `TestConditionalHeaders/create sends If-None-Match star`

- **AC-9**: **Given** a caller replacing an object with a nonempty version, **When** `PutIfMatch` succeeds, **Then** the backend sends the supplied data in a PUT with that version verbatim in `If-Match`, no `If-None-Match`, and returns the response ETag verbatim.
  - Evidence: `storage/s3/s3_test.go` `TestConditionalHeaders/put sends If-Match with the version verbatim`

- **AC-10**: **Given** a caller deleting an object with a nonempty version, **When** `DeleteIfMatch` succeeds, **Then** the backend sends one DELETE request with that version verbatim in `If-Match` and no `If-None-Match`.
  - Evidence: `storage/s3/s3_test.go` `TestConditionalHeaders/delete sends If-Match with the version verbatim`

- **AC-11**: **Given** a caller uploading bytes through either create or replacement, **When** the backend prepares the object upload, **Then** it labels the content `application/json` without validating that the supplied bytes are JSON.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `CreateIfAbsent`, `PutIfMatch`, `put`)

- **AC-12**: **Given** a configured prefix and a requested list prefix with results spread across multiple S3 pages, **When** the caller invokes `List`, **Then** the backend requests their concatenated prefix through ListObjectsV2, follows continuation tokens, and returns all keys, including keys at any depth below the prefix (no delimiter), in sorted order with only the configured prefix stripped.
  - Evidence: `storage/s3/s3_test.go` `TestListPaginatesSortsAndStripsPrefix`; UNTESTED for keys at arbitrary depth (behaviour in `storage/s3/s3.go` `List`).

- **AC-13**: **Given** an enabled, suspended, or never-enabled bucket-versioning configuration, **When** the caller invokes `Inspect`, **Then** versioning is respectively `storage.Yes`, `storage.No`, or `storage.No`; soft delete and hierarchical namespace are `storage.No`, and inspection reads bucket versioning without writing objects.
  - Evidence: `storage/s3/inspect_test.go` `TestInspectBucketVersioning` subtests `enabled`, `suspended`, `never enabled`

- **AC-14**: **Given** bucket-versioning inspection receives any HTTP 403 response not subject to a more specific S3 error-code mapping in AC-22 or AC-23, **When** `Inspect` returns after any applicable retries, **Then** it succeeds with unknown versioning and reports soft delete and hierarchical namespace as `storage.No`. (Whether this also applies when the response indicates invalid or expired credentials is not part of this contract; see OQ-2.)
  - Evidence: `storage/s3/inspect_test.go` `TestInspectBucketVersioning/access denied` covers `AccessDenied`; UNTESTED for other HTTP 403 error codes (behaviour in `storage/s3/s3.go` `Inspect`, `mapError`).

- **AC-15**: **Given** an operation receives HTTP 403 AccessDenied and its context does not opt into access-propagation retries, **When** the operation returns, **Then** it reports `storage.ErrAccessDenied` after one request, without reporting `storage.ErrUnknownOutcome`, and preserves the SDK error for inspection.
  - Evidence: `storage/s3/s3_test.go` `TestStatusMapping/403 AccessDenied fails immediately without the propagation flag` subtests `Get`, `CreateIfAbsent`, `PutIfMatch`, `DeleteIfMatch`, `List`

- **AC-16**: **Given** a context marked with `storage.WithAccessPropagationRetry` and an operation receiving two AccessDenied responses followed by success, **When** the caller reads, creates, replaces, or deletes an object, **Then** the operation retries the denied requests and succeeds on the third request.
  - Evidence: `storage/s3/s3_test.go` `TestAccessPropagationRetry` subtests `Get/with flag succeeds after 3 requests`, `CreateIfAbsent/with flag succeeds after 3 requests`, `PutIfMatch/with flag succeeds after 3 requests`, `DeleteIfMatch/with flag succeeds after 3 requests`

- **AC-17**: **Given** a context marked with `storage.WithAccessPropagationRetry`, **When** the caller lists objects or inspects bucket versioning and S3 keeps returning HTTP 403 with SDK error code `AccessDenied`, **Then** the operation retries up to the NFR-1 attempt limit; if denial persists, `List` reports `storage.ErrAccessDenied`, while `Inspect` succeeds with unknown versioning.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `List`, `Inspect`, `callOptions`, `retryAccessDenied`)

### Edge cases

- **AC-18**: **Given** a caller supplies an empty version, **When** it invokes `PutIfMatch` or `DeleteIfMatch`, **Then** the operation returns `storage.ErrPreconditionFailed`, not `storage.ErrUnknownOutcome`, without sending a request.
  - Evidence: `storage/s3/s3_test.go` `TestUnknownOutcome/errors before sending are not unknown outcomes/empty version is rejected without sending`

- **AC-19**: **Given** a conditional write's first attempt receives HTTP 412 PreconditionFailed or HTTP 409 ConditionalRequestConflict, **When** the operation returns, **Then** it reports `storage.ErrPreconditionFailed`, not `storage.ErrUnknownOutcome`, without retrying, and preserves the SDK error.
  - Evidence: `storage/s3/outcome_test.go` `TestFirstAttemptPreconditionFailureIsNotUnknown`; `storage/s3/s3_test.go` `TestStatusMapping` subtests `412 PreconditionFailed is not retried`, `409 ConditionalRequestConflict`

- **AC-20**: **Given** access-propagation retries are enabled and an object replacement receives HTTP 412 PreconditionFailed on its first attempt, **When** the operation returns, **Then** it reports `storage.ErrPreconditionFailed` after one request rather than retrying the condition failure.
  - Evidence: `storage/s3/s3_test.go` `TestAccessPropagationRetry/with flag still does not retry 412`

- **AC-21**: **Given** a read, replacement, or delete receives HTTP 404 NoSuchKey, **When** the operation returns, **Then** it reports `storage.ErrNotFound`, not `storage.ErrUnknownOutcome`, after one request and preserves the SDK error.
  - Evidence: `storage/s3/s3_test.go` `TestStatusMapping/404 NoSuchKey` subtests `Get`, `PutIfMatch`, `DeleteIfMatch`; UNTESTED with `storage.WithAccessPropagationRetry` (behaviour in `storage/s3/s3.go` `callOptions`, `retryAccessDenied`).

- **AC-22**: **Given** an S3 operation receives NoSuchBucket, **When** it returns, **Then** the SDK missing-bucket error remains inspectable and is not translated into any of the four storage sentinels.
  - Evidence: `storage/s3/s3_test.go` `TestMissingBucketIsPropagatedUnchanged`

- **AC-23**: **Given** S3 returns SDK error code `NotFound`, or an otherwise-unclassified HTTP 404, 412, or 403 response, **When** the backend classifies the failure and no write attempt may have applied, **Then** it maps these to `storage.ErrNotFound`, `storage.ErrNotFound`, `storage.ErrPreconditionFailed`, or `storage.ErrAccessDenied`, respectively, preserving the underlying error; errors outside these mappings pass through.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `mapError`)

- **AC-24**: **Given** a create, replacement, or delete request has reached the server, **When** the server hangs beyond the caller's deadline, resets the connection after receiving the request, or keeps returning server errors until retries end, **Then** the operation reports `storage.ErrUnknownOutcome`.
  - Evidence: `storage/s3/s3_test.go` `TestUnknownOutcome` subtests `server hangs past the client timeout`, `connection reset after the request was sent`, `final 5xx after SDK retries`

- **AC-25**: **Given** a conditional write first experiences a connection reset after sending, HTTP 408, HTTP 500, or HTTP 503 and its retry receives HTTP 412 or HTTP 409 ConditionalRequestConflict, **When** the operation returns, **Then** it reports only `storage.ErrUnknownOutcome` rather than `storage.ErrPreconditionFailed` and preserves the final SDK error.
  - Evidence: `storage/s3/outcome_test.go` `TestRetryAfterPossiblyAppliedAttemptIsUnknownOutcome`

- **AC-26**: **Given** a conditional write fails before sending because DNS lookup, connection establishment, or the TLS handshake fails, **When** the operation returns, **Then** it returns an error without `storage.ErrUnknownOutcome`.
  - Evidence: `storage/s3/s3_test.go` `TestUnknownOutcome/errors before sending are not unknown outcomes` subtests `dns failure`, `connection refused`; `storage/s3/outcome_test.go` `TestPreSendFailuresAreNotUnknown`

- **AC-27**: **Given** a context is already canceled and a conditional write otherwise has valid arguments, **When** the caller invokes create, replacement, or delete, **Then** it receives `context.Canceled` directly, without wrapping, and no request is sent.
  - Evidence: `storage/s3/outcome_test.go` `TestCancelledContextReturnsContextError`

- **AC-28**: **Given** a read operation receives server errors until retries end, **When** `Get` returns, **Then** it returns an error without `storage.ErrUnknownOutcome`.
  - Evidence: `storage/s3/s3_test.go` `TestUnknownOutcome/reads are never unknown outcomes`

- **AC-29**: **Given** a successful response lacks an ETag, **When** the response is from `Get`, **Then** the call fails with a missing-ETag error rather than `storage.ErrNotFound`; **When** it is from create or replacement, **Then** the call reports `storage.ErrUnknownOutcome`.
  - Evidence: `storage/s3/s3_test.go` `TestMissingETag` subtests `get without an ETag is an error`, `CreateIfAbsent without an ETag is an unknown outcome`, `PutIfMatch without an ETag is an unknown outcome`

- **AC-30**: **Given** callers share one backend instance for concurrent conditional writes, **When** some writes receive HTTP 500 then HTTP 412 while others receive HTTP 412 on their first attempt, **Then** each write is classified from its own attempts: the former report only `storage.ErrUnknownOutcome` and the latter only `storage.ErrPreconditionFailed`.
  - Evidence: `storage/s3/concurrent_test.go` `TestSharedStoreClassifiesEachWriteOnItsOwnAttempts`

- **AC-31**: **Given** an earlier attempt of a conditional write may have applied but a later retry succeeds, **When** the operation returns, **Then** it returns success rather than `storage.ErrUnknownOutcome`, with the response ETag still required for create and replacement.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `put`, `DeleteIfMatch`)

- **AC-32**: **Given** an object response body cannot be read completely, **When** the caller invokes `Get`, **Then** the call returns no data or version and an error preserving the body-read failure.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `Get`)

- **AC-33**: **Given** listing fails while fetching a page, **When** `List` returns, **Then** it returns the classified error and no partial list of keys.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `List`)

- **AC-34**: **Given** bucket-versioning inspection fails for a reason other than denied access, **When** `Inspect` returns, **Then** it returns an error rather than a successful unknown-state report.
  - Evidence: `storage/s3/inspect_test.go` `TestInspectBucketVersioning/missing bucket`

- **AC-35**: **Given** an object read, create, replacement, delete, or list receives an HTTP 403 response and no earlier write attempt may have applied, **When** the operation returns, **Then** it reports `storage.ErrAccessDenied` with the SDK error preserved regardless of the SDK error code, including invalid-key, signature, and clock-skew errors, unless a more specific S3 error-code mapping in AC-22 or AC-23 takes precedence.
  - Evidence: UNTESTED for error codes other than `AccessDenied` (behaviour in `storage/s3/s3.go` `mapError`, `Get`, `put`, `DeleteIfMatch`, `List`); `storage/s3/s3_test.go` `TestStatusMapping/403 AccessDenied fails immediately without the propagation flag` covers `AccessDenied` only.

- **AC-36**: **Given** a context marked with `storage.WithAccessPropagationRetry` and an HTTP 403 response whose SDK error code is neither `AccessDenied` nor independently retryable under the SDK's default rules, **When** the caller reads, creates, replaces, deletes, or lists objects, **Then** the response is not retried and the operation fails after one request with `storage.ErrAccessDenied`, unless a more specific S3 error-code mapping in AC-22 or AC-23 takes precedence.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `callOptions`, `retryAccessDenied`, `mapError`)

- **AC-37**: **Given** a caller configures a custom `Endpoint`, **When** the backend uploads an object or receives an object response, **Then** uploads send the supplied body without optional SDK checksum headers or trailers, and request checksum calculation and response checksum validation occur only when required by the operation; without a custom endpoint, the AWS SDK default checksum behaviour applies.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `New`, `put`)

- **AC-38**: **Given** a configured prefix that is empty or consists only of `/` characters, **When** the caller addresses key `k` or lists objects, **Then** `k` addresses the bucket-root key `k` without an added leading separator, and `List` strips nothing from returned keys.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `normalisePrefix`, `objectKey`, `List`)

- **AC-39**: **Given** neither `Region` nor the AWS SDK environment or shared configuration supplies a region, **When** a caller constructs the backend, **Then** `New` fails without looking up a region through instance metadata.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `Config`, `New`)

- **AC-40**: **Given** `Region` is unset and a region is available through the AWS SDK environment or shared configuration, **When** a caller constructs the backend, **Then** the backend uses that SDK-provided region.
  - Evidence: UNTESTED (behaviour in `storage/s3/s3.go` `Config`, `New`)

## Non-functional requirements *(mandatory)*

- **NFR-1**: Access-propagation retry mode allows at most **10 attempts per SDK operation**, including the initial attempt, for both `AccessDenied` and the SDK's default retryable errors, including server errors and throttling. Each call uses a fresh standard retryer with this limit, replacing any SDK retry settings from the environment or shared configuration, including `AWS_MAX_ATTEMPTS` and `retry_mode`. Source: `storage/s3/s3.go` `propagationMaxAttempts`, `callOptions`.
- **NFR-2**: Access-propagation retry mode caps an individual retry backoff at **30 seconds**; this is not a total operation deadline. Source: `storage/s3/s3.go` `propagationMaxBackoff`, `callOptions`.
- **NFR-3**: Without access-propagation mode or an AWS SDK retry-configuration override, persistent HTTP 500 or HTTP 408 failures allow at most **3 attempts per conditional write** (SDK default; retries may be refused earlier once the SDK's shared retry quota is exhausted). Source: `storage/s3/s3.go` `New`; `storage/s3/outcome_test.go` `TestBackendRetryerIsExercised` asserts 3 attempts for one write per fresh backend, not quota exhaustion.

## Key entities *(include if the feature involves data)*

- **S3 backend configuration**: The existing bucket name, optional key prefix, region override, endpoint override, and path-style-addressing choice supplied by a library caller.
- **Stored object**: A bucket-relative key and its bytes, addressed beneath the configured prefix.
- **Object version**: The opaque S3 ETag, including any quotation marks, returned to callers and supplied unchanged for conditional operations.
- **Operation outcome**: Success or an error that may identify a missing object, rejected condition, denied access, or uncertain write result through the storage sentinels defined in BASE-0010 storage-contract.
- **Protection report**: Bucket versioning, soft-delete, and hierarchical-namespace states exposed through the shared Inspector interface.

## Out of scope *(mandatory)*

- Backend-independent storage semantics, sentinel definitions, context-flag API, report types, and the reusable backend contract suite: see [BASE-0010 storage-contract](../BASE-0010-storage-contract/spec.md).
- Backend URL parsing and validation, URL credential restrictions, and secret-safe URL errors: see [BASE-0009 backend-url](../BASE-0009-backend-url/spec.md). This spec concerns direct S3 backend construction and operations.
- Directory storage and Azure Blob authentication or storage behavior: see [BASE-0011 local-backend](../BASE-0011-local-backend/spec.md) and [BASE-0013 azblob-backend](../BASE-0013-azblob-backend/spec.md).
- Creating buckets, changing bucket versioning or IAM policies, managing credentials, and recovering historical object versions. The backend uses an existing bucket and the AWS SDK credential chain.
- Client-level reconciliation of an unknown write outcome, pool or allocation behavior, and transactions spanning multiple objects.
- Guarantees that every S3-compatible endpoint enforces the conditional headers it receives. The integration runner explicitly permits conditional-delete cases to be skipped for emulators known to ignore `If-Match`; sending the header alone does not establish server enforcement.

## Open questions *(mandatory, empty before G2)*

- **OQ-1**: Which S3-compatible endpoints are supported for production conditional deletes, and where is their enforcement verified? `storage/s3/integration_test.go` `TestS3Contract` documents that some emulators ignore DELETE `If-Match` and permits those cases to be skipped; the request-header tests do not establish service-side enforcement. Backend maintainers must clarify the supported compatibility boundary.

- **OQ-2**: Observed behaviour: `Inspect` succeeds with unknown versioning for any HTTP 403, without distinguishing missing versioning permission from invalid or expired credentials, signature errors or clock skew (`storage/s3/s3.go` `Inspect`, `mapError`). Maintainer answer (2026-11-03, baseline-spec run): this is a bug; credential failures should surface as an error. No AC treats the credential-failure case as the contract. A Bug citing this spec is to be raised after merge, and this entry is then removed.
