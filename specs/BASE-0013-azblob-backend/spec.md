# Feature Specification: Azure Blob backend

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0013-azblob-backend/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Store documents with conditional writes (Priority: P1)

A library caller uses an existing Azure Blob container and an optional key prefix to read, create, replace, delete and list documents. The caller passes the returned ETag back on replacement or deletion, distinguishes definite conflicts from uncertain writes, and follows the shared storage contract in BASE-0010 storage-contract.

**Why this priority**: Conditional document access is the backend's primary service to the IPAM client.

### Journey 2 - Configure Azure authentication (Priority: P2)

A library caller or an operator configuring a consumer of the library supplies Azure authentication settings through `AuthConfig`, the supported environment variables, or both. They select client-secret, client-certificate, OIDC, managed-identity, Azure CLI or AKS workload-identity sources and construct a credential without contacting Azure.

**Why this priority**: Callers need an authenticated backend before accessing their documents.

### Journey 3 - Inspect account protection settings (Priority: P2)

An operator's diagnostic client calls `Inspect` to learn the account's hierarchical-namespace and soft-delete settings and whether reservation documents demonstrate blob versioning. Missing permissions leave individual properties unknown without changing the account.

**Why this priority**: Operators need to distinguish observed protection settings from settings their credentials cannot establish.

### Journey 4 - Use a local Azure emulator (Priority: P3)

A developer connects to Azurite using its published development account through a loopback connection string in `AZURE_STORAGE_CONNECTION_STRING`.

**Why this priority**: Local emulator access supports development without accepting real-account shared keys.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a valid backend configuration and a valid managed-identity authentication configuration, **When** a caller constructs the credential and calls `New`, **Then** construction succeeds without sending a network request.
  - Evidence: `storage/azblob/azblob_test.go` `TestConstructionDoesNotTouchTheNetwork`

- **AC-2**: **Given** Azure authentication settings in the primary `ARM_*` variables listed under Key entities, **When** a caller calls `AuthConfigFromEnv`, **Then** the returned configuration contains the corresponding tenant, client, secret, certificate and OIDC settings.
  - Evidence: `storage/azblob/auth_test.go` `TestAuthConfigFromEnvAndMerge/env only, including the alternate variable names`; `TestAuthConfigFromEnvAndMerge/mixed: set attributes win, unset attributes fall back to env, ARM_ names win over alternates`

- **AC-3**: **Given** more than one supported environment variable for an OIDC setting, **When** a caller calls `AuthConfigFromEnv`, **Then** the first nonempty value wins in these orders: token file, `ARM_OIDC_TOKEN_FILE_PATH` then `AZURE_FEDERATED_TOKEN_FILE`; request token, `ARM_OIDC_REQUEST_TOKEN` then `ACTIONS_ID_TOKEN_REQUEST_TOKEN` then `SYSTEM_ACCESSTOKEN`; request URL, `ARM_OIDC_REQUEST_URL` then `ACTIONS_ID_TOKEN_REQUEST_URL` then `SYSTEM_OIDCREQUESTURI`; service connection, `ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID` then `ARM_OIDC_AZURE_SERVICE_CONNECTION_ID` then `AZURESUBSCRIPTION_SERVICE_CONNECTION_ID`.
  - Evidence: `storage/azblob/auth_test.go` `TestAuthConfigFromEnvAndMerge/env only, including the alternate variable names`; `TestAuthConfigFromEnvAndMerge/env only, ADO request variables when GitHub ones are absent`; `TestAuthConfigFromEnvAndMerge/mixed: set attributes win, unset attributes fall back to env, ARM_ names win over alternates`

- **AC-4**: **Given** unset authentication-source flags, **When** a caller loads authentication settings from the environment or calls `AuthConfig.Credential` with a zero-value configuration, **Then** Azure CLI authentication defaults to enabled and OIDC, managed identity and AKS workload identity default to disabled.
  - Evidence: `storage/azblob/auth_test.go` `TestAuthConfigFromEnvAndMerge/env only, including the alternate variable names`; `TestAuthConfigFromEnvAndMerge/attributes only`; `TestAuthConfigOptions/zero value defaults to the Azure CLI like azapi`

- **AC-5**: **Given** a base authentication configuration and an override, **When** the caller calls `Merge`, **Then** nonempty override strings and explicitly supplied boolean flags, including false, replace the corresponding base values; unset override fields retain their base values.
  - Evidence: `storage/azblob/auth_test.go` `TestAuthConfigFromEnvAndMerge/attributes only`; `TestAuthConfigFromEnvAndMerge/mixed: set attributes win, unset attributes fall back to env, ARM_ names win over alternates`

- **AC-6**: **Given** a client secret or secret-file path, or an inline certificate or certificate-file path, **When** a caller calls `AuthConfig.Credential`, **Then** the credential is configured to use the corresponding secret or certificate source with the configured material, client-ID file and certificate password where supplied.
  - Evidence: `storage/azblob/auth_test.go` `TestAuthConfigOptions/client secret material enables the client secret source`; `TestAuthConfigOptions/client secret file and client id file`; `TestAuthConfigOptions/client certificate path with password`; `TestAuthConfigOptions/inline client certificate`

- **AC-7**: **Given** OIDC material, **When** a caller calls `AuthConfig.Credential`, **Then** the credential is configured to use OIDC token, token-file and token-request sources only when OIDC is enabled and their required material is present, except for the AKS token-file behavior in AC-9; enabling OIDC without material configures no OIDC source.
  - Evidence: `storage/azblob/auth_test.go` `TestAuthConfigOptions/use_oidc with every OIDC material enables each OIDC source`; `TestAuthConfigOptions/OIDC material without use_oidc is ignored, like azapi`; `TestAuthConfigOptions/use_oidc without any OIDC material enables no OIDC source`

- **AC-8**: **Given** managed identity is enabled and Azure CLI is disabled, **When** a caller calls `AuthConfig.Credential`, **Then** the credential is configured to use managed identity with the supplied client ID and has no Azure CLI source.
  - Evidence: `storage/azblob/auth_test.go` `TestAuthConfigOptions/managed identity only`

- **AC-9**: **Given** AKS workload identity is enabled, no explicit client ID is supplied, and the AKS client ID, tenant ID and federated token-file settings are available, **When** a caller calls `AuthConfig.Credential`, **Then** the credential uses the AKS client and tenant and is configured with the token-file source even without enabling general OIDC.
  - Evidence: `storage/azblob/auth_test.go` `TestAuthConfigOptions/AKS workload identity takes client, tenant and token file from the AKS variables`

- **AC-10**: **Given** a readable blob with an ETag, **When** a caller calls `Get`, **Then** the backend returns its body and ETag unchanged, including the ETag's quotation marks.
  - Evidence: `storage/azblob/azblob_test.go` `TestGetReturnsDataAndETagVerbatim`

- **AC-11**: **Given** a key and document bytes, **When** a caller calls `CreateIfAbsent`, **Then** the Azure write uses `If-None-Match: *`, sends the supplied bytes and returns the successful response's ETag unchanged; creation over an existing blob fails with `storage.ErrPreconditionFailed`.
  - Evidence: `storage/azblob/azblob_test.go` `TestCreateIfAbsentSendsIfNoneMatchAny`; `TestErrorMapping/create over existing blob (409 BlobAlreadyExists)`; `storage/azblob/integration_test.go` `TestAzuriteConditionalFailures`

- **AC-12**: **Given** a blob version and replacement bytes, **When** a caller calls `PutIfMatch`, **Then** the Azure write uses that exact ETag in `If-Match` and returns the successful response's ETag; a stale ETag fails with `storage.ErrPreconditionFailed`.
  - Evidence: `storage/azblob/azblob_test.go` `TestPutIfMatchSendsIfMatch`; `TestErrorMapping/put stale ETag (412 ConditionNotMet)`; `storage/azblob/integration_test.go` `TestAzuriteConditionalFailures`

- **AC-13**: **Given** a blob version, **When** a caller calls `DeleteIfMatch`, **Then** the Azure delete uses that exact ETag in `If-Match`; a stale ETag fails with `storage.ErrPreconditionFailed`.
  - Evidence: `storage/azblob/azblob_test.go` `TestDeleteIfMatchSendsIfMatch`; `TestErrorMapping/delete stale ETag (412 ConditionNotMet)`; `storage/azblob/integration_test.go` `TestAzuriteConditionalFailures`

- **AC-14**: **Given** a configured backend prefix and multiple Azure listing pages, **When** a caller lists a key prefix, **Then** the listing request includes both prefixes, all pages are included, and the returned keys are sorted with only the backend prefix removed.
  - Evidence: `storage/azblob/azblob_test.go` `TestListPagesStripsPrefixAndSorts`

- **AC-15**: **Given** a definitive Azure error response, **When** a storage operation returns an error, **Then** `BlobNotFound` matches `storage.ErrNotFound`, `ConditionNotMet` or `BlobAlreadyExists` matches `storage.ErrPreconditionFailed`, and HTTP 403 with `AuthorizationPermissionMismatch` or `AuthorizationFailure` matches `storage.ErrAccessDenied`; the tested definite failures do not also match `storage.ErrUnknownOutcome`.
  - Evidence: `storage/azblob/azblob_test.go` `TestErrorMapping` (all named cases)

- **AC-16**: **Given** a transport error eligible for retry or an HTTP 408, 429, 500, 502, 503 or 504 response, **When** a caller makes a storage request that encounters that condition, **Then** the request receives additional attempts while the configured retry limits and caller's context permit; a successful response or HTTP 501 receives no further attempt.
  - Evidence: `storage/azblob/azblob_test.go` `TestUnknownOutcomeAfterFinal5xx/create`; `TestUnknownOutcomeAfterFinal5xx/put`; `TestUnknownOutcomeAfterFinal5xx/delete` (HTTP 503 produces 3 requests with `MaxRetries` 2); `TestShouldRetry/transport error`; `TestShouldRetry/408`; `TestShouldRetry/429`; `TestShouldRetry/500`; `TestShouldRetry/502`; `TestShouldRetry/503`; `TestShouldRetry/504`; `TestShouldRetry/201`; `TestShouldRetry/501`. The cited transport-error, HTTP 408, 429, 500, 502, 504, 201 and 501 cases have retry-predicate-level evidence only, not end-to-end attempt-count assertions.

- **AC-17**: **Given** HTTP 403 with `AuthorizationPermissionMismatch` or `AuthorizationFailure`, **When** a caller makes a storage request using a context marked by `storage.WithAccessPropagationRetry`, **Then** the request receives further attempts while retry limits and the caller's context permit; without that opt-in it receives only one attempt, and `AuthenticationFailed` or a missing Azure error code receives only one attempt even with the opt-in.
  - Evidence: `storage/azblob/azblob_test.go` `TestAccessPropagationRetry/retried with the propagation flag` (3 requests for `AuthorizationPermissionMismatch`); `TestAccessPropagationRetry/not retried without the flag` (1 request); `TestErrorMapping/create 403 AuthorizationFailure`; `TestErrorMapping/delete 403 AuthorizationFailure` (1 request each without opt-in); `TestShouldRetry` (the six named HTTP 403 cases). Opted-in `AuthorizationFailure`, `AuthenticationFailed` and missing-code responses have retry-predicate-level evidence only, not end-to-end attempt-count assertions; the counted precondition-failure case is cited in AC-29.

- **AC-18**: **Given** readable account and blob-service properties, **When** a caller calls `Inspect`, **Then** its report reflects the hierarchical-namespace and soft-delete booleans as `storage.Yes` or `storage.No`, using only GET and HEAD requests.
  - Evidence: `storage/azblob/inspect_test.go` `TestInspectAzureAccount/everything readable and enabled`; `TestInspectAzureAccount/no blob has a version id: documents may predate versioning, so unknown`; `TestInspectAzureAccount/hierarchical namespace`

- **AC-19**: **Given** a reservation JSON document under the configured backend prefix has a nonempty Azure version ID, **When** a caller calls `Inspect`, **Then** versioning is reported as `storage.Yes`, including when an earlier inspected reservation has no version ID.
  - Evidence: `storage/azblob/inspect_test.go` `TestInspectAzureAccount/everything readable and enabled`; `TestInspectAzureAccount/first document unversioned, a later one versioned`

- **AC-20**: **Given** reservation documents have no version IDs, there are no reservation documents, reservation listing is denied or a reservation's properties cannot be read because access is denied, **When** a caller calls `Inspect`, **Then** versioning is `storage.Unknown`, not `storage.No`, without failing the inspection for those conditions.
  - Evidence: `storage/azblob/inspect_test.go` `TestInspectAzureAccount/no blob has a version id: documents may predate versioning, so unknown`; `TestInspectAzureAccount/no reservations document to read`; `TestInspectAzureAccount/listing denied`; `TestInspectAzureAccount/reservations document not readable`

- **AC-21**: **Given** the caller cannot read account information or blob-service properties, **When** a caller calls `Inspect`, **Then** the inaccessible hierarchical-namespace or soft-delete property is `storage.Unknown` and the inspection can still return a report.
  - Evidence: `storage/azblob/inspect_test.go` `TestInspectAzureAccount/container-scoped role cannot read service properties or account info`

### Edge cases

- **AC-22**: **Given** a missing container, a missing token credential without an emulator connection string, or a token-credential configuration whose account URL is empty, lacks a scheme, uses plain HTTP or carries a SAS query, **When** a caller calls `New`, **Then** construction fails with `azblob.ErrInvalidArgument` before sending any request.
  - Evidence: `storage/azblob/azblob_test.go` `TestNewValidatesConfig/missing container`; `TestNewValidatesConfig/no credential and no connection string`; `TestNewValidatesConfig/empty account URL`; `TestNewValidatesConfig/account URL without scheme`; `TestNewValidatesConfig/plain http account URL`; `TestNewValidatesConfig/account URL carrying a SAS token`

- **AC-23**: **Given** no explicit token credential, **When** a caller constructs the backend using `AZURE_STORAGE_CONNECTION_STRING`, **Then** shared-key authentication is accepted only with the published Azurite development account name and key and an explicit permitted loopback `BlobEndpoint`; real-account names or other keys are rejected with `azblob.ErrInvalidArgument`.
  - Evidence: `storage/azblob/azblob_test.go` `TestNewAcceptsAzuriteConnectionString`; `TestNewValidatesConfig/connection string: real account`; `TestNewValidatesConfig/connection string: loopback endpoint with a real account name`; `TestNewValidatesConfig/connection string: loopback endpoint with the dev account but another key`

- **AC-24**: **Given** an emulator connection string, **When** a caller calls `New` with that connection string and no token credential, **Then** only HTTP or HTTPS with lowercase `localhost`, an IPv4 address in `127.0.0.0/8` or IPv6 `::1` is accepted; non-loopback hosts, lookalike hostnames, uppercase `LOCALHOST`, IPv4-mapped IPv6 loopback, unspecified addresses and user information are rejected with `azblob.ErrInvalidArgument`.
  - Evidence: `storage/azblob/azblob_test.go` `TestNewAcceptsAzuriteConnectionString`; `TestIsLoopback`; `TestRequireLoopbackURL`; `TestNewValidatesConfig/connection string: upper-case LOCALHOST`; `TestNewValidatesConfig/connection string: IPv4-mapped IPv6 loopback`; `TestNewValidatesConfig/connection string: unspecified address`; `TestNewValidatesConfig/connection string: non-http scheme`; `TestNewValidatesConfig/connection string: userinfo trick`

- **AC-25**: **Given** a malformed emulator connection string, **When** a caller calls `New`, **Then** duplicate keys, known keys with different casing or surrounding whitespace, absent explicit blob endpoints, SAS authentication with or without the development key, empty middle segments, trailing whitespace segments and `UseDevelopmentStorage` shorthand are rejected with `azblob.ErrInvalidArgument` without sending a request.
  - Evidence: `storage/azblob/azblob_test.go` `TestNewValidatesConfig` (the named `connection string:` cases); `TestParseAzuriteConnectionStringChecksEndpoint`

- **AC-26**: **Given** a connection-string configuration with a disagreeing explicit account URL, or a connection string that the Azure client resolves to a non-loopback endpoint, **When** a caller calls `New`, **Then** construction fails with `azblob.ErrInvalidArgument` without sending a request.
  - Evidence: `storage/azblob/azblob_test.go` `TestNewValidatesConfig/account URL disagreeing with the connection string`; `TestNewRejectsNonLoopbackResolvedURL`

- **AC-27**: **Given** an empty version or the wildcard `*`, **When** a caller calls `PutIfMatch` or `DeleteIfMatch`, **Then** the call fails with `storage.ErrPreconditionFailed` without sending a request.
  - Evidence: `storage/azblob/azblob_test.go` `TestEmptyOrWildcardVersionIsRejectedWithoutRequest`

- **AC-28**: **Given** Azure reports `ContainerNotFound` rather than `BlobNotFound`, **When** a caller calls `Get`, **Then** it returns an error that does not match `storage.ErrNotFound`.
  - Evidence: `storage/azblob/azblob_test.go` `TestContainerNotFoundIsNotObjectNotFound`

- **AC-29**: **Given** a storage request receives HTTP 404, 409 or 412, **When** the caller's storage operation completes, **Then** that response causes no further request attempt, including with access-propagation retry enabled; a direct precondition failure remains a definite `storage.ErrPreconditionFailed`.
  - Evidence: `storage/azblob/azblob_test.go` `TestShouldRetry/404`; `TestShouldRetry/409`; `TestShouldRetry/412`; `TestPreconditionFailedIsNeverRetried`

- **AC-30**: **Given** a create, replacement or delete may have reached Azure, **When** the server hangs past an attempt timeout or the operation ends after HTTP 5xx responses, **Then** the failed operation matches `storage.ErrUnknownOutcome`.
  - Evidence: `storage/azblob/azblob_test.go` `TestUnknownOutcomeWhenTheServerHangsPastTryTimeout/create`; `TestUnknownOutcomeWhenTheServerHangsPastTryTimeout/put`; `TestUnknownOutcomeWhenTheServerHangsPastTryTimeout/delete`; `TestUnknownOutcomeAfterFinal5xx/create`; `TestUnknownOutcomeAfterFinal5xx/put`; `TestUnknownOutcomeAfterFinal5xx/delete`

- **AC-31**: **Given** an earlier replacement attempt received HTTP 500 and a later attempt receives HTTP 412, **When** the overall replacement fails, **Then** its error matches `storage.ErrUnknownOutcome` and does not claim a definite `storage.ErrPreconditionFailed`.
  - Evidence: `storage/azblob/azblob_test.go` `TestUnknownOutcomeWhenAnEarlierAttemptMayHaveApplied`

- **AC-32**: **Given** token acquisition fails or a traced transport (defined under Backend configuration) cannot establish the connection or complete its TLS handshake, **When** a caller attempts a conditional create, replacement or delete, **Then** the call returns an error without matching `storage.ErrUnknownOutcome`.
  - Evidence: `storage/azblob/azblob_test.go` `TestPreSendErrorsAreNotUnknownOutcome/token acquisition failure`; `TestPreSendErrorsAreNotUnknownOutcome/connection refused`; `TestTLSHandshakeFailureIsNotUnknownOutcome`

- **AC-33**: **Given** the context is already canceled or its deadline has already expired, **When** a caller attempts a conditional create, replacement or delete with a usable version where required, **Then** no request is sent, the error preserves `context.Canceled` or `context.DeadlineExceeded` respectively, and it does not match `storage.ErrUnknownOutcome`.
  - Evidence: `storage/azblob/azblob_test.go` `TestPreCancelledContextIsNotUnknownOutcome/create`; `TestPreCancelledContextIsNotUnknownOutcome/put`; `TestPreCancelledContextIsNotUnknownOutcome/delete`

- **AC-34**: **Given** an untraced transport (defined under Backend configuration) returns `io.ErrUnexpectedEOF`, **When** a conditional create, replacement or delete fails, **Then** the result matches `storage.ErrUnknownOutcome`.
  - Evidence: `storage/azblob/azblob_test.go` `TestUntraceableTransportErrorsStayUnknownOutcome`

- **AC-35**: **Given** Azure CLI is disabled and no usable authentication source is enabled, **When** a caller calls `AuthConfig.Credential`, **Then** it fails with `azblob.ErrInvalidArgument`.
  - Evidence: `storage/azblob/auth_test.go` `TestCredentialRejectsConfigWithoutSource`; `TestAuthConfigOptions/no source enabled is rejected`

- **AC-36**: **Given** AKS workload identity is enabled and an explicit tenant ID disagrees with the AKS tenant ID, **When** a caller calls `AuthConfig.Credential`, **Then** credential construction fails with `azblob.ErrInvalidArgument`.
  - Evidence: `storage/azblob/auth_test.go` `TestAuthConfigOptions/AKS tenant mismatch is rejected like azapi`

- **AC-37**: **Given** account-information retrieval fails with an unexpected error rather than access denial, **When** a caller calls `Inspect`, **Then** the inspection returns an error.
  - Evidence: `storage/azblob/inspect_test.go` `TestInspectAzureUnexpectedErrorIsReturned`

- **AC-38**: **Given** both an explicit token credential and `AZURE_STORAGE_CONNECTION_STRING`, **When** a caller calls `New`, **Then** the token-credential configuration is used and the connection string is not consulted.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `newClients`)

- **AC-39**: **Given** a configured backend prefix with leading or trailing slashes and a relative key without a leading slash, **When** the backend addresses that key, **Then** surrounding prefix slashes are removed and exactly one slash separates a nonempty backend prefix from the caller's key; an empty or all-slash backend prefix adds nothing.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `New`, `Store.name`)

- **AC-40**: **Given** a token-credential account URL that cannot be parsed, has no host or contains a fragment or user information, **When** a caller calls `New`, **Then** it is rejected with `azblob.ErrInvalidArgument`.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `validateAccountURL`)

- **AC-41**: **Given** a nonempty authentication-source environment flag, **When** `AuthConfigFromEnv` reads it, **Then** only the exact value `true` enables that flag; other nonempty values, including `TRUE` and `1`, disable it.
  - Evidence: UNTESTED (behavior in `storage/azblob/auth.go` `envBool`)

- **AC-42**: **Given** OIDC is enabled and an OIDC request token and Azure service-connection ID are supplied without a request URL, **When** a caller calls `AuthConfig.Credential`, **Then** the credential is configured with an OIDC token-request source using that service connection.
  - Evidence: UNTESTED (behavior in `storage/azblob/auth.go` `AuthConfig.options`)

- **AC-43**: **Given** AKS workload identity is enabled and both an explicit client ID and an AKS environment client ID are available, **When** a caller calls `AuthConfig.Credential`, **Then** the credential uses the explicit client ID.
  - Evidence: UNTESTED (behavior in `storage/azblob/auth.go` `AuthConfig.options`)

- **AC-44**: **Given** an otherwise successful read response has no ETag, **When** `Get` completes, **Then** it returns an error instead of returning a document version.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `Store.Get`)

- **AC-45**: **Given** downloading a blob's body fails while reading or closing it, **When** `Get` completes, **Then** it returns an error retaining the read or close failure and returns no document bytes or version.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `Store.Get`)

- **AC-46**: **Given** an Azure listing page fails, including after earlier successful pages, **When** `List` completes, **Then** it returns an error and no partial key list.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `Store.List`)

- **AC-47**: **Given** service-property retrieval, reservation listing or reservation-property retrieval fails for a reason other than the tolerated access-denied or missing-blob conditions, **When** `Inspect` completes, **Then** it returns an error rather than a partial protection report.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `Store.Inspect`, `Store.versioning`)

- **AC-48**: **Given** a readable account or service response omits the hierarchical-namespace boolean, delete-retention policy or its enabled boolean, **When** a caller calls `Inspect`, **Then** the corresponding property is `storage.Unknown`.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `Store.Inspect`, `triState`)

- **AC-49**: **Given** the reservation prefix contains only keys not ending in `.json`, **When** a caller calls `Inspect`, **Then** those keys are not queried for blob properties and versioning is `storage.Unknown`.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `Store.versioning`)

- **AC-50**: **Given** a listed reservation JSON blob disappears before its properties are read, **When** Azure returns `BlobNotFound` for that read, **Then** `Inspect` reports versioning as `storage.Unknown` without failing.
  - Evidence: UNTESTED (behavior in `storage/azblob/azblob.go` `Store.versioning`)

- **AC-51**: **Given** authentication settings pass the backend's source-selection checks but credential construction rejects them, **When** `AuthConfig.Credential` returns, **Then** it returns a contextual error preserving the underlying credential-construction error and does not match `azblob.ErrInvalidArgument`.
  - Evidence: UNTESTED (behavior in `storage/azblob/auth.go` `AuthConfig.Credential`)

- **AC-52**: **Given** a caller selects a container, **When** the backend is constructed or used for document operations, **Then** it does not create or delete the container; provisioning the container is the caller's responsibility.
  - Evidence: UNTESTED (behavior documented in `storage/azblob/azblob.go` `Config.Container` and implemented by `New` and the `Store` document operations)

- **AC-53**: **Given** a definitive Azure error response without a recognized `BlobNotFound`, `ConditionNotMet` or `BlobAlreadyExists` code, **When** a storage operation returns an error, **Then** HTTP 412 matches `storage.ErrPreconditionFailed` even without an Azure error code, and HTTP 403 matches `storage.ErrAccessDenied` even with another code such as `AuthenticationFailed`.
  - Evidence: UNTESTED (behavior in `storage/azblob/errors.go` `mapError`)

- **AC-54**: **Given** an untraced transport (defined under Backend configuration) whose failed attempts report only DNS errors or connection-refused errors, **When** a conditional create, replacement or delete fails, **Then** the returned error does not match `storage.ErrUnknownOutcome`.
  - Evidence: UNTESTED (behavior in `storage/azblob/errors.go` `outcomePolicy.Do`)

- **AC-55**: **Given** a conditional create, replacement or delete attempt receives HTTP 408, or uses a traced transport (defined under Backend configuration) that fails after request headers have been written, **When** the overall operation fails, **Then** the returned error matches `storage.ErrUnknownOutcome`.
  - Evidence: UNTESTED (behavior in `storage/azblob/errors.go` `outcomePolicy.Do`, `Store.write`)

- **AC-56**: **Given** an HNS-enabled account and an otherwise valid backend configuration, **When** a caller constructs the backend and calls `Inspect` with permission to read account information, **Then** the backend does not reject the account for HNS and reports `HierarchicalNamespace` as `storage.Yes` so `ipam doctor` can flag it (see BASE-0015 cli-doctor).
  - Evidence: `storage/azblob/inspect_test.go` `TestInspectAzureAccount/hierarchical namespace`

## Non-functional requirements *(mandatory)*

- **NFR-1**: The default Azure client configuration allows at most **10 retries** after an initial request attempt. Source: `storage/azblob/azblob.go` `clientOptions`; evidence: `storage/azblob/azblob_test.go` `TestRetryOptionsMatchThePlan`.
- **NFR-2**: The default configured retry delay is **2 seconds**. Source: `storage/azblob/azblob.go` `clientOptions`; evidence: `storage/azblob/azblob_test.go` `TestRetryOptionsMatchThePlan`.
- **NFR-3**: The default configured maximum retry delay is **30 seconds**. Source: `storage/azblob/azblob.go` `clientOptions`; evidence: `storage/azblob/azblob_test.go` `TestRetryOptionsMatchThePlan`.
- **NFR-4**: The default configured timeout per request attempt is **60 seconds**. Source: `storage/azblob/azblob.go` `clientOptions` (`TryTimeout: time.Minute`); no direct assertion of this value was found in the capability's tests.
- **NFR-5**: Backend document access uses **0 blob leases**. Source: `storage/azblob/doc.go`; conditional requests provide concurrency control rather than lease acquisition or renewal.

These are request configuration limits, not an end-to-end operation latency guarantee.

## Key entities *(include if the feature involves data)*

- **Backend configuration**: An existing container, its account URL, an optional virtual-directory prefix, a token credential and an optional HTTP transport. A **traced transport** means `Config.Transport` is nil, or is an `*http.Client` whose `Transport` is nil or an `*http.Transport`. An **untraced transport** is any other `policy.Transporter`, including an `*http.Client` whose `Transport` is another `http.RoundTripper`. The backend does not create or delete containers.
- **Blob document**: Caller-supplied bytes addressed by a key relative to the backend prefix.
- **Document version**: Azure's ETag, returned verbatim as `storage.Version`; the shared version and conditional-operation contract belongs to BASE-0010 storage-contract.
- **Authentication configuration**: Tenant and client identifiers; optional client-ID, secret, certificate and federated-token file paths; inline secret, certificate or token material; certificate password; OIDC request settings; optional source-selection booleans. Secret values are not reproduced in this specification.
- **Protection report**: `storage.Info` with versioning, soft delete and hierarchical namespace expressed as `storage.Yes`, `storage.No` or `storage.Unknown`; the shared report type belongs to BASE-0010 storage-contract.

Primary authentication environment variables are:

| Settings | Variables |
| --- | --- |
| Tenant and client | `ARM_TENANT_ID`, `ARM_CLIENT_ID`, `ARM_CLIENT_ID_FILE_PATH` |
| Client secret | `ARM_CLIENT_SECRET`, `ARM_CLIENT_SECRET_FILE_PATH` |
| Client certificate | `ARM_CLIENT_CERTIFICATE`, `ARM_CLIENT_CERTIFICATE_PATH`, `ARM_CLIENT_CERTIFICATE_PASSWORD` |
| OIDC token and token file | `ARM_OIDC_TOKEN`, `ARM_OIDC_TOKEN_FILE_PATH` |
| OIDC request | `ARM_OIDC_REQUEST_TOKEN`, `ARM_OIDC_REQUEST_URL`, `ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID` |
| Source flags | `ARM_USE_OIDC`, `ARM_USE_MSI`, `ARM_USE_CLI`, `ARM_USE_AKS_WORKLOAD_IDENTITY` |
| AKS-injected identifiers | `AZURE_CLIENT_ID`, `AZURE_TENANT_ID` |

AC-3 defines alternate environment names and their precedence. Emulator shared-key configuration comes from `AZURE_STORAGE_CONNECTION_STRING`, not from a key field in `Config`.

## Out of scope *(mandatory)*

- Generic storage semantics, shared error definitions, access-propagation context helpers, the in-memory backend and the reusable backend contract suite: see BASE-0010 storage-contract. This spec records the Azure-specific requests, mappings and inspection results.
- `Open` and `OpenStorage` URL parsing, URL-level rejection rules and their secret-safe errors: see BASE-0009 backend-url. Direct `azblob.New` configuration is a separate surface described here.
- Local filesystem storage: see BASE-0011 local-backend. S3 access, credentials and conditional requests: see BASE-0012 s3-backend.
- Container provisioning, role assignments, identity provisioning and changes to account versioning or retention policies. `Inspect` only reports observations.
- Azure Data Lake Gen2 hierarchical-namespace accounts are documented as unsupported. The separate `ipam doctor` CLI diagnostic reports a known HNS account as a failing check, but this backend does not reject it; see BASE-0015 cli-doctor.
- Defining the external authentication library's source ordering or guaranteeing that supplied credentials can acquire a token. The backend configures the supported sources and fetches tokens on use rather than during construction.
- CLI command formatting, diagnostic exit codes, IPAM pool and allocation behavior, emulator startup and probe tooling.

## Open questions *(mandatory, empty before G2)*

- **OQ-1**: Direct `azblob.New` errors can echo the supplied account URL or credentials through scheme/host validation of plain-HTTP or hostless URLs before SAS-query or user-information checks, wrapped URL parse errors, and emulator connection-string endpoint mismatch errors. Sources: `storage/azblob/azblob.go` `validateAccountURL`, `newClients`. Maintainer answer (2026-11-03): this is a bug; error text must not echo the account URL or credentials. A Bug citing this spec is to be raised after merge, and this entry is then removed.
- **OQ-2**: A successful create or replacement response without an ETag currently returns an error that does not match `storage.ErrUnknownOutcome`. Source: `storage/azblob/azblob.go` `Store.upload`; no covering test was found. Maintainer answer (2026-11-03): this is a bug. A Bug citing this spec is to be raised after merge, and this entry is then removed.

