# Feature Specification: Backend URL selection and validation

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0009-backend-url/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Select storage with a URL (Priority: P1)

As a library caller, I supply one backend URL to `OpenStorage` to obtain storage, or to `Open` to obtain an IPAM client. The URL selects memory, a local directory, an S3 bucket and optional prefix, or an Azure Blob container and optional prefix. Cloud credentials are configured separately from the backend URL.

**Why this priority**: This is the entry point for callers that select storage through configuration rather than constructing a backend directly.

### Journey 2 - Use local development storage across platforms (Priority: P2)

As a library caller running tests or local tools, I use `mem://` or a `file:///absolute/path` URL. On Windows, I can name a drive directory with `file:///C:/data`; on other systems, a drive-like path remains rooted under `/`.

**Why this priority**: Local and memory URLs let the same entry points be used without cloud storage.

### Journey 3 - Diagnose rejected configuration without disclosing secrets (Priority: P2)

As a caller handling configuration errors, I identify invalid arguments with `errors.Is`. Rejected URLs produce reasons without echoing credentials. An accidental doubled path separator produces a correction hint, while a traversal error does not produce that unrelated hint.

**Why this priority**: Callers need useful errors even when the rejected configuration contains sensitive values.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** `mem://`, **When** a caller invokes `OpenStorage`, **Then** it returns an in-memory store that can create and read an object with its returned version.
  - Evidence: `open_test.go` `TestOpenMemURLIsUsableStore`.

- **AC-2**: **Given** a file URL naming a directory under an existing temporary directory, **When** a caller invokes `OpenStorage`, **Then** it returns a local store that can create and read an object with its returned version.
  - Evidence: `open_test.go` `TestOpenFileURLGivesWorkingLocalStore`.

- **AC-3**: **Given** `mem://`, **When** a caller invokes `Open`, **Then** it returns a non-nil client without an error.
  - Evidence: `open_test.go` `TestOpenMemURLIsUsableStore`.

- **AC-4**: **Given** a backend URL that opens successfully and client options, **When** a caller invokes `Open`, **Then** the client is constructed over that backend with the supplied options.
  - Evidence: UNTESTED (behaviour in `open.go` `Open`).

- **AC-5**: **Given** valid S3 configuration, **When** a caller supplies `s3://bkt`, `s3://bkt/`, or `s3://bkt/a/b/`, **Then** the selected bucket is `bkt` and the respective object prefix is empty, empty, or `a/b`.
  - Evidence: `open_test.go` `TestOpenParsesTargets` / `s3 bucket only`, `s3 trailing slash`, `s3 nested prefix`.

- **AC-6**: **Given** an S3 URL with `region=eu-west-1`, `endpoint=https://s3.example.test:9000`, and `path_style=true`, **When** the URL is opened, **Then** those settings are supplied to the S3 backend; `path_style=false` selects non-path-style addressing.
  - Evidence: `open_test.go` `TestOpenParsesTargets` / `s3 all parameters`, `s3 explicit path_style false`.

- **AC-7**: **Given** S3 credentials, a region, and a configured HTTP service endpoint, **When** a caller invokes `OpenStorage`, **Then** it constructs an S3 store without sending a request to that endpoint.
  - Evidence: `open_test.go` `TestOpenS3BuildsStoreWithoutRequests`.

- **AC-8**: **Given** an Azure account-form URL such as `azblob://acct.blob.core.windows.net/cnt/p/q` and a configured credential source, **When** the URL is opened, **Then** it selects account URL `https://acct.blob.core.windows.net`, container `cnt`, and prefix `p/q`; account authentication is defined by BASE-0013 azblob-backend.
  - Evidence: `open_test.go` `TestOpenParsesTargets` / `azblob account form`; `TestOpenAzureAccountFormBuildsWithoutNetwork`.

- **AC-9**: **Given** a container-form URL such as `azblob://cnt/p` and a supported loopback emulator connection string in `AZURE_STORAGE_CONNECTION_STRING`, **When** a caller invokes `OpenStorage`, **Then** it returns an Azure Blob store; a bare `azblob://cnt` selects container `cnt` and an empty prefix.
  - Evidence: `open_test.go` `TestOpenAzureContainerFormAcceptsLoopbackEmulator`; `TestOpenParsesTargets` / `azblob container form`.

- **AC-10**: **Given** an Azure account-form URL and no available credential source with CLI authentication disabled, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenAzureAccountFormWithoutCredentialSourceIsRejected`.

- **AC-11**: **Given** an Azure container-form URL and no connection string, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenAzureContainerFormWithoutConnectionStringIsRejected`.

- **AC-12**: **Given** any error from constructing the S3 backend, the Azure backend, or an Azure credential, **When** `OpenStorage` returns the failure, **Then** `errors.Is` matches `ipam.ErrInvalidArgument` while the original cause remains in the error chain. The backend's explanation is retained after `ipam: invalid argument: `, with the `azblob: invalid argument: ` prefix folded to `azblob:`; both sentinels still match with `errors.Is` when the cause carries `azblob.ErrInvalidArgument`. Azure credential errors that do not match `azblob.ErrInvalidArgument` also carry the `azblob credential:` prefix. In particular, a missing S3 region retains the region-required explanation, and a rejected non-loopback Azure connection string retains `azblob.ErrInvalidArgument`.
  - Evidence: UNTESTED for the general constructor and credential-error cases, including the credential prefix and Azure message folding (behaviour in `open.go` `OpenStorage`, `openAzure`, `backendArgumentError`); tested examples: `open_test.go` `TestOpenS3ConfigurationErrorsAreInvalidArgument` / `missing region`; `TestOpenAzureContainerFormRejectsNonLoopbackConnectionString`.

- **AC-13**: **Given** a valid file URL path, **When** its directory is selected, **Then** a Windows drive path such as `/C:/Users/x` becomes `C:/Users/x` on Windows and stays `/C:/Users/x` elsewhere; ordinary paths stay rooted, and a bare `/C:` is not converted into a drive directory.
  - Evidence: `open_test.go` `TestFileDir` / `posix path`, `posix drive-like path stays rooted`, `windows drive path`, `windows lower-case drive`, `windows path without a drive`, `windows bare drive`.

### Edge cases

- **AC-14**: **Given** an empty or whitespace-only URL, surrounding whitespace, a raw control character, an absent scheme, an unsupported scheme, or an opaque target such as `s3:bkt/p`, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `empty`, `whitespace only`, `leading space`, `trailing newline`, `raw NUL byte`, `no scheme`, `unknown scheme`, `opaque form`.

- **AC-15**: **Given** a backend URL carrying user information or a fragment, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`, including for file URLs and cloud URLs.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 user info`, `s3 fragment`, `azblob account user info`, `file user info`, `file fragment`.

- **AC-16**: **Given** rejected URLs containing credentials in user information, unknown query parameters, malformed query values, or fragments, or a rejected Azure connection string containing an account key, **When** the caller reads the returned error text, **Then** those secret values are not echoed.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 user info`, `s3 secret parameter`, `s3 secret parameter mixed case`, `s3 secret as parameter name`, `s3 malformed query`, `s3 fragment`; `TestOpenAzureContainerFormRejectsSecretQueryWithEmulator`; `TestOpenAzureContainerFormRejectsNonLoopbackConnectionString`.

- **AC-17**: **Given** a memory URL with a host, a nonempty path, or a nonempty query, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `mem host`, `mem path`, `mem query`.

- **AC-18**: **Given** a file URL with a host, a relative or opaque path, or only the filesystem root, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `file with host`, `file relative` (host rejection: `file://tmp/x`), `file opaque` (relative/opaque rejection: `file:tmp/x`); `TestOpenParserRejectsWithoutBackendHelp` / `file root`.

- **AC-19**: **Given** a file URL with a nonempty query, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `file query`.

- **AC-20**: **Given** an S3 URL whose bucket host is empty, uppercase, non-ASCII, includes a port, or is an IP literal, **When** a caller invokes `OpenStorage`, **Then** URL validation fails with `ErrInvalidArgument` before backend configuration can mask the error.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 empty bucket`, `s3 bucket uppercase`, `s3 unicode bucket`, `s3 bucket with port`, `s3 IPv6 host`; `TestOpenS3IPStyleBucketIsRejectedByParser`.

- **AC-21**: **Given** an S3 URL with an unknown query key, a repeated query parameter, or malformed query encoding, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`; the only accepted keys are `region`, `endpoint`, and `path_style`, each with one value.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 unknown parameter`, `s3 duplicate parameter`, `s3 malformed query`; `TestOpenParsesTargets` / `s3 all parameters`.

- **AC-22**: **Given** an S3 URL with an explicit region, **When** the region is empty or does not consist of two lowercase ASCII letters followed by one or more hyphen-separated lowercase letter groups and a final hyphen-separated decimal number, **Then** URL validation fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 region empty`, `s3 region invalid`; `TestOpenParsesTargets` / `s3 all parameters`.

- **AC-23**: **Given** an S3 URL with `path_style`, **When** its value is anything other than the literal `true` or `false`, including an empty value, **Then** URL validation fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 path_style not boolean`, `s3 path_style empty`; `TestOpenParsesTargets` / `s3 all parameters`, `s3 explicit path_style false`.

- **AC-24**: **Given** an S3 endpoint value without an HTTP or HTTPS scheme or host, or containing user information, a nonempty query, or a fragment, **When** a caller invokes `OpenStorage`, **Then** URL validation fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 endpoint scheme`, `s3 endpoint without scheme`, `s3 endpoint without host`, `s3 endpoint user info`, `s3 endpoint query`, `s3 endpoint fragment`.

- **AC-25**: **Given** an Azure URL host containing a dot, **When** it is not exactly a lowercase ASCII alphanumeric account name of 3 to 24 characters followed by `.blob.core.windows.net`, **Then** URL validation fails with `ErrInvalidArgument`; foreign hosts and suffix-spoofing hosts are not accepted.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `azblob account uppercase`, `azblob account suffix spoof`, `azblob account foreign host`, `azblob account name too short`, `azblob account name too long`; `TestOpenParsesTargets` / `azblob account 3 chars`, `azblob account 24 chars`.

- **AC-26**: **Given** either Azure URL form, **When** the container is missing, has fewer than 3 or more than 63 characters, contains anything other than lowercase ASCII letters, digits, and single interior hyphens, or starts or ends with a hyphen, **Then** URL validation fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `azblob account missing container`, `azblob container too short`, `azblob container 64 chars`, `azblob container uppercase`, `azblob container leading hyphen`, `azblob container trailing hyphen`, `azblob container double hyphen`, `azblob account container too short`, `azblob account container 64 chars`, `azblob container invalid`; `TestOpenParsesTargets` / `azblob container 63 chars`, `azblob container interior hyphen`; UNTESTED for the container-form missing-container case (behaviour in `open.go` `parseAzblob`, `checkAzureNames`).

- **AC-27**: **Given** either Azure URL form with any query, including a bare `?` or SAS parameters, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument` even if an otherwise usable credential source or emulator connection string is configured.
  - Evidence: `open_test.go` `TestOpenAzureContainerFormRejectsEmptyQueryWithEmulator`, `TestOpenAzureContainerFormRejectsSecretQueryWithEmulator`, `TestOpenAzureAccountFormRejectsQueryWithCredentialSource`.

- **AC-28**: **Given** a doubled path separator anywhere in a nonempty directory, prefix, or Azure account-form container path, including at its end, such as `file:///tmp//data`, `s3://bkt/a//`, or `azblob://acct.blob.core.windows.net/cnt//`, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`. The empty-prefix bug recorded in appvia/ipam#34 is excluded from this criterion.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `file double slash`, `s3 double slash`, `azblob double slash`; `open_message_test.go` `TestOpenEmptySegmentMessageIsActionable` / `file trailing slash variable`, `s3 double slash`, `azblob double slash`; UNTESTED for a doubled separator at the end (behaviour in `open.go` `blobPath`).

- **AC-29**: **Given** a backend path containing a `.` or `..` segment, including percent-encoded traversal, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument` rather than resolving the traversal.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 traversal`, `s3 percent-encoded traversal`, `file traversal`, `file percent-encoded traversal`; `TestOpenParserRejectsWithoutBackendHelp` / `s3 dot segment`; `open_message_test.go` `TestOpenDotSegmentMessageHasNoTrailingSlashHint`.

- **AC-30**: **Given** a file, S3, or Azure URL path containing a backslash, raw or percent-encoded, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument`.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 backslash in prefix`; UNTESTED for file/Azure paths and percent-encoded backslashes (behaviour in `open.go` `parseOpenURL`, `blobPath`). Percent-encoded NUL rejection remains covered by AC-46.

- **AC-31**: **Given** an S3 or Azure prefix exceeding 512 bytes, **When** a caller supplies the URL, **Then** URL validation fails with `ErrInvalidArgument`, including for both Azure URL forms.
  - Evidence: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 prefix too long`; `TestOpenParserRejectsWithoutBackendHelp` / `azblob prefix too long`, `azblob account prefix too long`.

- **AC-32**: **Given** otherwise valid S3 or Azure configuration and a valid prefix of exactly 512 decoded bytes, **When** the caller opens the URL, **Then** the prefix length is accepted by URL validation.
  - Evidence: UNTESTED (behaviour in `open.go` `parseS3`, `checkAzureNames`).

- **AC-33**: **Given** a URL rejected for an empty path segment, **When** the caller reads its error, **Then** the explanation identifies `empty segment ('//')`, explains that a trailing slash in a variable such as `$TMPDIR` can cause it, suggests `${TMPDIR%/}`, and does not echo the host or path.
  - Evidence: `open_message_test.go` `TestOpenEmptySegmentMessageIsActionable` / `file trailing slash variable`, `s3 double slash`, `azblob double slash`.

- **AC-34**: **Given** a URL rejected for a dot or dot-dot segment, **When** the caller reads its error, **Then** it does not carry the unrelated `$TMPDIR` trailing-slash hint.
  - Evidence: `open_message_test.go` `TestOpenDotSegmentMessageHasNoTrailingSlashHint`.

- **AC-35**: **Given** a file or memory URL ending with a bare `?`, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument` even though the query has no contents.
  - Evidence: UNTESTED (behaviour in `open.go` `parseFile`, `parseMem`).

- **AC-36**: **Given** a syntactically valid file URL whose local backend constructor fails, **When** a caller invokes `OpenStorage`, **Then** the failure preserves the constructor error's identity rather than being classified automatically as `ipam.ErrInvalidArgument`. This criterion excludes error-text behavior, including the path-echo bug recorded in appvia/ipam#36.
  - Evidence: UNTESTED (behaviour in `open.go` `OpenStorage`).

- **AC-37**: **Given** a backend URL that fails to open, **When** a caller invokes `Open` rather than `OpenStorage`, **Then** it returns no client and propagates the opening error unchanged.
  - Evidence: UNTESTED (behaviour in `open.go` `Open`).

- **AC-38**: **Given** an otherwise valid file, S3, or Azure URL path containing percent-encoded characters, **When** the URL is opened, **Then** escaped characters have their decoded meaning in the selected directory or object prefix; an encoded `/` acts as a path separator, so `s3://bkt/a%2Fb` selects prefix `a/b`.
  - Evidence: UNTESTED (behaviour in `open.go` `parseOpenURL`, `blobPath`).

- **AC-39**: **Given** otherwise valid S3 configuration, **When** an otherwise valid HTTP or HTTPS endpoint includes a path and no query marker, **Then** the endpoint path passes URL validation.
  - Evidence: UNTESTED (behaviour in `open.go` `validateEndpoint`).

- **AC-40**: **Given** otherwise valid S3 configuration, **When** the backend URL ends in a bare `?`, **Then** URL validation accepts it as having no S3 query settings.
  - Evidence: UNTESTED (behaviour in `open.go` `parseS3`).

- **AC-41**: Withdrawn (maintainer: current behaviour is a bug; see appvia/ipam#33)

- **AC-42**: Withdrawn (maintainer: current behaviour is a bug; see appvia/ipam#34)

- **AC-43**: **Given** a supported scheme written in any letter case, such as `MEM://`, `File:///tmp/data` or `S3://bkt`, **When** a caller invokes `OpenStorage`, **Then** the URL is handled exactly as its lowercase-scheme form, because URI schemes are case-insensitive (RFC 3986 section 3.1); host, path and query rules are unchanged.
  - Evidence: UNTESTED (behaviour in `open.go` `parseOpenURL`, which switches on `url.URL.Scheme`; Go's `net/url` lowercases the scheme while parsing)

- **AC-44**: **Given** a URL that cannot be parsed, such as `s3://bkt/%zz` with an invalid percent escape in its path, **When** a caller invokes `OpenStorage`, **Then** the call fails with `ErrInvalidArgument` and the explanation `URL is malformed`.
  - Evidence: UNTESTED (behaviour in `open.go` `parseOpenURL`).

- **AC-45**: Withdrawn (merged raw and percent-encoded backslash rejection into AC-30)

- **AC-46**: **Given** a file, S3, or Azure URL path containing a percent-encoded ASCII control byte below 32 or equal to 127, **When** a caller invokes `OpenStorage`, **Then** the decoded control byte causes rejection with `ErrInvalidArgument`.
  - Evidence: UNTESTED for the full control-byte range and file/Azure paths (behaviour in `open.go` `parseOpenURL`, `blobPath`, `hasControl`); tested example: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 NUL in prefix`.

- **AC-47**: **Given** an otherwise valid file, S3, or Azure URL with an optional trailing slash, **When** the URL is opened, **Then** the trailing slash is not part of the selected directory or object prefix.
  - Evidence: UNTESTED for file and Azure paths (behaviour in `open.go` `blobPath`, `parseFile`, `parseS3`, `parseAzblob`); tested S3 examples: `open_test.go` `TestOpenParsesTargets` / `s3 trailing slash`, `s3 nested prefix`.

- **AC-48**: **Given** either an Azure account-form or container-form URL whose host carries a port, including `azblob://cnt:10000/p`, **When** a caller invokes `OpenStorage`, **Then** URL validation fails with `ErrInvalidArgument` and the explanation `azblob host must not carry a port`.
  - Evidence: UNTESTED for container-form ports and the explanation (behaviour in `open.go` `parseAzblob`); tested account-form rejection: `open_test.go` `TestOpenRejectsInvalidURLs` / `azblob account with port`.

- **AC-49**: **Given** any URL rejected by URL validation, **When** the caller reads the error, **Then** its text has the form `ipam: invalid argument: <reason>`, where the reason is fixed and may name the backend type (`s3`, `azblob`, `file`, `mem`) but copies no text from the URL's host, bucket, container, path, or query. Backend-construction failures are excluded, including the file-backend path-echo bug in appvia/ipam#36; AC-12 and AC-36 cover those failures.
  - Evidence: UNTESTED for the general fixed-reason and no-URL-echo guarantee (behaviour in `open.go` `invalidURL` and the URL validators); tested secret-marker omissions: `open_test.go` `TestOpenRejectsInvalidURLs` / `s3 user info`, `s3 secret parameter`, `s3 malformed query`, `s3 fragment`; tested empty-segment host/path omissions: `open_message_test.go` `TestOpenEmptySegmentMessageIsActionable` / `file trailing slash variable`, `s3 double slash`, `azblob double slash`.

## Non-functional requirements *(mandatory)*

- **NFR-1**: Each S3 or Azure object prefix is at most 512 bytes after URL decoding and removal of the optional outer path separators. The Azure account-form limit excludes the container name. Source: `open.go` `maxPrefixBytes`, `parseS3`, `parseAzblob`, `checkAzureNames`.
- **NFR-2**: An S3 bucket host has 3 to 63 ASCII characters, starts and ends with a lowercase letter or digit, and otherwise contains lowercase letters, digits, dots, or hyphens; IP literals are rejected separately. This is the URL boundary's check, not a guarantee of acceptance by the S3 service. Source: `open.go` `s3BucketPattern`, `parseS3`.
- **NFR-3**: An Azure account label has 3 to 24 lowercase ASCII letters or digits. Source: `open.go` `azureAccountPattern`.
- **NFR-4**: An Azure container name has 3 to 63 ASCII characters. Source: `open.go` `azureContainerPattern`, `checkAzureNames`.

## Key entities *(include if the feature involves data)*

- **Backend URL**: Caller-supplied selection of a scheme, target, optional path, and scheme-specific query settings. Supported documented schemes are `mem`, `file`, `s3`, and `azblob`.
- **Storage target**: A local directory, S3 bucket and optional prefix, Azure account/container and optional prefix, or an in-memory store.
- **S3 URL settings**: Optional region, HTTP/HTTPS endpoint, and explicit path-style addressing selection.
- **Opening error**: An error identifying malformed URL input or a backend construction failure, with `ErrInvalidArgument` classification for URL failures and all S3, Azure backend, and Azure credential-construction failures.

## Out of scope *(mandatory)*

- Storage operations, conditional writes, versions, list semantics, and the in-memory backend's full contract: see BASE-0010 storage-contract.
- Local filesystem persistence, locking, atomic writes, and local error details: see BASE-0011 local-backend.
- S3 request behavior, credential discovery and region fallback, service-side naming restrictions, CAS, and retry behavior: see BASE-0012 s3-backend.
- Azure credential-chain rules, connection-string validation beyond its effect on opening, loopback restrictions, HNS rejection, and Blob request semantics: see BASE-0013 azblob-backend.
- CLI flags, backend selection precedence, command output, stderr, and exit codes: see BASE-0014 cli.
- IPAM operations performed after `Open` returns a client, and the behavior of individual client options.
- A successful open does not establish that the caller can subsequently read or write remote objects. No availability, latency, throughput, or retry target is introduced by this URL capability.

## Open questions *(mandatory, empty before G2)*

- None. Questions raised during the baseline run were answered by the maintainer on 2026-11-03 and moved to issues: appvia/ipam#33, appvia/ipam#34, appvia/ipam#35, appvia/ipam#36.
