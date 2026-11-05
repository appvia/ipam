# Feature Specification: Explicit consent for unverified backends

**Requirement**: REQ-000070 ([source](https://github.com/appvia/ipam/issues/70))
**Spec path**: `specs/REQ-000070-conditional-write-backends/spec.md`
**Created**: 2026-11-05
**Status**: Draft - clarification required before G2
**Input**: Approved `brief.md`, `clarifications.md`, and the source Requirement

## User journeys *(mandatory)*

### Journey 1 - Avoid an unverified store by default (Priority: P1)

As an operator, I select a backend and am stopped before ordinary operations
if its endpoint is outside the verified support boundary. The refusal explains
how to make an informed opt-in without disclosing my configuration. Previously
supported verified backends continue to work without an opt-in.

**Why this priority**: Silently accepting a store that ignores conditional
writes can lose updates or allocate the same address twice.

### Journey 2 - Explicitly accept an unverified backend (Priority: P1)

As an operator or library caller, I explicitly accept the risk of an unverified
endpoint. Command-line use warns me once, while library callers can obtain the
classification and present their own warning. The opt-in never bypasses URL
validation or changes the conditional-write requirements.

**Why this priority**: Existing custom-endpoint users need a deliberate way to
restore access without implying that their backend has been verified.

### Journey 3 - Diagnose and understand the support boundary (Priority: P2)

As an operator, I see the classification in doctor alongside available
read-only diagnostic results, even when ordinary opening would be refused.
Documentation explains the support boundary, the override, the risks and the
limits of diagnostics.

**Why this priority**: Refusal must not prevent diagnosis or imply that a
successful diagnostic proves all conditional operations are enforced.

## Functional requirements

- **FR-1 - Classification**: URL-based library opening and command-line backend
  selection must apply the following support boundary after existing URL
  validation. Classification is a policy decision, not a live safety test.

  | Valid backend selection | Classification |
  | --- | --- |
  | `s3://` with no `endpoint=` override | Verified |
  | `s3://` with an endpoint host matching `*.amazonaws.com` or `*.amazonaws.com.cn` | Verified |
  | Account-form `azblob://<account>.blob.core.windows.net/...` | Verified |
  | `file://` or `mem://` | Verified |
  | `s3://` with an endpoint on any other host | Unverified |

  The classification depends on the selected endpoint host, not a provider name
  embedded elsewhere in the URL. Existing authentication and configuration
  requirements remain in effect; verified does not mean reachable or authorised.
- **FR-2 - Default refusal**: Both library URL-opening entry points and ordinary
  backend-using commands must refuse unverified backends without explicit opt-in.
  Library callers must distinguish this refusal as a dedicated error category
  without parsing its message. Exact public names and option shapes are design
  decisions. Doctor has the reporting exception in FR-6, not ordinary access.
- **FR-3 - Explicit consent**: Library callers must be able to opt in when
  opening either storage or an IPAM client. Operators must be able to opt in with
  `--allow-unverified-backend` or `IPAM_ALLOW_UNVERIFIED_BACKEND=true`.
  An explicitly supplied flag value takes precedence over the environment
  setting, including an explicit false flag. An unset or false setting does not
  grant consent. Consent allows use; it does not reclassify the backend.
- **FR-4 - Diagnostics**: An allowed unverified command-line invocation must
  print one support-risk warning line to stderr and then run normally. Refusal
  and warning text must name the applicable opt-in, link to the supported-backends
  documentation, explain the conditional-write risk and omit private backend
  configuration. Ordinary command output remains on its existing output stream.
- **FR-5 - Library visibility**: Library callers must be able to obtain the
  classification without granting consent, so dependent applications can show
  their own warning and expose a future opt-in. The exact interface is deferred
  to design; the provider change itself is not part of this Requirement.
- **FR-6 - Doctor**: Doctor must report a classification check in both its table
  and structured output: PASS for verified, WARN for explicitly allowed
  unverified, and FAIL for unverified without consent. A classification FAIL
  must not itself stop other read-only checks. Existing aggregation applies:
  any failed check produces a failed report and exit status 6; warnings alone
  do not fail the command. Write-probe behavior without consent is Q1 below.
- **FR-7 - Validation and compatibility**: Consent must never bypass existing
  URL syntax validation. Verified backends retain existing opening and operation
  behavior, except for the additional doctor classification result. Classification
  and opening must introduce no endpoint probes, network requests or diagnostic
  writes. Direct backend construction retains its existing no-network behavior.
- **FR-8 - Consistent support policy**: All covered entry points must agree on
  the verified set. Maintainers may expand the support boundary after a provider
  passes the storage contract suite; operators cannot turn an unverified
  classification into verified merely by setting the consent option.
- **FR-9 - Operator documentation**: A "Supported backends" section in the
  README must explain the verified set, default refusal, both command-line
  opt-ins, library consent, and operator responsibility for evaluating other
  endpoints, including use of the storage contract suite. Backend selection
  documentation, endpoint configuration reference documentation, refusal text
  and warning text must point to this same section.
- **FR-10 - Safety explanation**: Documentation must state that conditional
  creation rejects an existing object, and conditional replacement and deletion
  reject a version mismatch. Ignoring these conditions can corrupt pool and
  reservation documents under concurrent use. Amazon S3 is the reference target
  and the only verified S3 service. Doctor cannot prove conditional deletion,
  and its existing write probe can change data on a non-enforcing backend.
- **FR-11 - Migration**: The changelog must identify default refusal as a
  breaking change for unverified custom endpoints and give the explicit
  opt-ins that restore prior access without claiming to make a store safe.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a valid `s3://` selection with no endpoint override,
  **When** either library URL-opening entry point or a backend-using command
  classifies it, **Then** it is verified and needs no unverified-backend opt-in.
- **AC-2**: **Given** valid S3 selections with endpoint hosts matching each of
  `*.amazonaws.com` and `*.amazonaws.com.cn`, **When** each covered entry point
  classifies them, **Then** both are verified without opt-in.
- **AC-3**: **Given** valid account-form Azure Blob, local-file and memory
  selections, **When** each covered entry point opens them with its existing
  prerequisites satisfied, **Then** each is verified and preserves existing
  behavior without opt-in.
- **AC-4**: **Given** a valid S3 selection with an endpoint on the fictional host
  `store.example.test`, **When** each covered entry point classifies it,
  **Then** it is unverified irrespective of bucket, prefix or addressing style.
- **AC-5**: **Given** an unverified selection and no library opt-in, **When** a
  caller tries each library URL-opening entry point, **Then** neither returns a
  usable store or client and both return the dedicated, programmatically
  distinguishable unverified-backend error category.
- **AC-6**: **Given** an unverified selection supplied through either `--backend`
  or `IPAM_BACKEND` and no opt-in, **When** an operator runs an ordinary
  backend-using command, **Then** it exits unsuccessfully before performing the
  requested store operation and explains the opt-in and documentation location.
- **AC-7**: **Given** an unverified selection with existing opening prerequisites
  satisfied, **When** a library caller explicitly opts in through either
  URL-opening entry point, **Then** it obtains a usable store or client and the
  classification remains unverified.
- **AC-8**: **Given** verified and unverified selections without opt-in,
  **When** a library caller requests their classifications, **Then** it can
  distinguish both outcomes without first successfully opening the unverified
  backend or parsing a diagnostic message.
- **AC-9**: **Given** a reachable unverified backend and an otherwise successful
  command, **When** an operator supplies `--allow-unverified-backend` with the
  environment setting absent or false, **Then** the command proceeds normally
  and emits exactly one support-risk warning line to stderr.
- **AC-10**: **Given** the same conditions with no consent flag supplied,
  **When** `IPAM_ALLOW_UNVERIFIED_BACKEND=true` is set, **Then** the command
  proceeds normally and emits exactly one support-risk warning line to stderr.
- **AC-11**: **Given** an unverified backend and
  `IPAM_ALLOW_UNVERIFIED_BACKEND=true`, **When** the operator explicitly supplies
  `--allow-unverified-backend=false`, **Then** ordinary access is refused and no
  requested store operation is performed.
- **AC-12**: **Given** an allowed unverified invocation that opens the backend
  more than once or requests structured output, **When** the command runs,
  **Then** it emits only one support-risk warning line to stderr, identifies
  that conditional creation, replacement and deletion must be enforced, names
  the opt-in, links to the support documentation and leaves stdout in its
  existing requested format.
- **AC-13**: **Given** a verified backend, **When** doctor runs, **Then** its
  classification check is PASS in table and structured output, while other
  checks independently determine the overall result as before.
- **AC-14**: **Given** an unverified backend and explicit consent, **When**
  doctor runs, **Then** its classification check is WARN in both output formats,
  its classification remains unverified, and with no failed checks the report
  result is warn and the exit status is 0.
- **AC-15**: **Given** an unverified backend without consent, **When** doctor
  runs, **Then** its classification check is FAIL in both output formats,
  the report result is fail and the exit status is 6.
- **AC-16**: **Given** an unverified backend without consent whose existing
  read-only checks can otherwise complete, **When** doctor reports classification
  FAIL, **Then** those checks still run and their results are included rather
  than being suppressed solely because classification failed.
- **AC-17**: **Given** verified and unverified valid selections with and without
  consent, **When** classification or either library URL-opening entry point
  runs, **Then** classification and opening issue zero network requests and
  perform zero diagnostic writes; any later ordinary or doctor operations are
  separate from opening.
- **AC-18**: **Given** a newly encountered valid non-Amazon S3 endpoint,
  **When** it is classified repeatedly through library and command-line entry
  points with and without consent, **Then** all report unverified and none
  promotes it based on consent or previous successful operations.
- **AC-19**: **Given** an operator reading the README's Supported backends
  section, **When** they compare it with FR-1, **Then** it lists the same support
  boundary, explains default refusal and both command-line opt-ins, describes
  library consent, and recommends checking other endpoints with the storage
  contract suite before accepting the risk.
- **AC-20**: **Given** an operator reading that section, **When** they review
  its safety guidance, **Then** it describes all three conditional-operation
  requirements in FR-10, the concurrent-corruption risk, the reference target,
  doctor's inability to prove conditional deletion and its write-probe risk.
- **AC-21**: **Given** the backend selection table, endpoint configuration
  reference, refusal text and support-risk warning, **When** an operator follows
  each support link, **Then** each reaches the same Supported backends section.
- **AC-22**: **Given** an operator upgrading from a release that allowed custom
  endpoints by default, **When** they read the changelog entry, **Then** it
  identifies the breaking default-refusal change and explains how both
  command-line opt-ins and library consent restore access to unverified stores.

### Edge cases

- **AC-23**: **Given** a backend URL rejected by existing syntax validation,
  **When** a caller supplies any unverified-backend opt-in, **Then** it still
  receives the existing invalid-argument category rather than an opened backend;
  this includes prohibited credentials, fragments, paths, query parameters and
  malformed endpoint values covered by BASE-0009.
- **AC-24**: **Given** fictional private-looking endpoint, bucket, prefix and
  credential markers in accepted or rejected configuration fixtures, **When**
  refusal, warning or classification-check text is produced, **Then** no marker
  or configured URL is echoed; the support documentation link remains present
  in unverified-backend refusal and warning text.
- **AC-25**: **Given** an endpoint outside the verified host suffixes whose
  hostname or path merely contains `amazonaws.com` or `amazonaws.com.cn`,
  **When** it is classified, **Then** it remains unverified; examples include
  `amazonaws.com.example.test` and `amazonaws.com.cn.example.test`.
- **AC-26**: **Given** an unverified backend with both consent settings absent
  or with only `IPAM_ALLOW_UNVERIFIED_BACKEND=false`, **When** an ordinary
  backend-using command runs, **Then** it refuses access as in AC-6.
- **AC-27**: **Given** a verified selection but missing credentials, unavailable
  storage or another existing operation failure, **When** the relevant opening
  or operation is attempted, **Then** the existing error behavior is preserved;
  classification is not presented as evidence of connectivity or permission.

## Non-functional requirements *(mandatory)*

- **NFR-1**: Classification and opening add exactly 0 network requests per open
  and 0 diagnostic storage writes per open (AC-17).
- **NFR-2**: Unverified selections receive 0 successful ordinary opens without
  explicit consent across 100% of covered entry points (AC-5, AC-6, AC-11, AC-26).
- **NFR-3**: Allowed unverified command-line use emits exactly 1 support-risk
  warning line per invocation, not per backend open (AC-9, AC-10, AC-12).
- **NFR-4**: Refusal, warning and classification diagnostics disclose 0 supplied
  endpoint URLs, bucket names, prefixes or credential values per diagnostic
  (AC-24; constitution I and V).
- **NFR-5**: At implementation verification, 100% of acceptance criteria must
  map to at least 1 named automated test per criterion; fixtures contain 0 real
  customer records or credentials and tests make 0 requests to real third-party
  services (constitution II and V).
- **NFR-6**: Changed user-facing documentation and diagnostic presentation must
  satisfy 100% of applicable WCAG 2.2 level AA criteria; classification statuses
  remain readable as text rather than depending only on colour (constitution IV).

## Key entities *(include if the feature involves data)*

- **Backend classification**: Verified or unverified support-policy result for
  a valid backend selection; not a result of a live enforcement test.
- **Explicit consent**: A caller's opening option or an operator's effective
  command-line setting allowing use without changing classification.
- **Classification check**: A doctor result that combines classification and
  effective consent into PASS, WARN or FAIL.

## Out of scope *(mandatory)*

- Runtime proof, endpoint probes, scratch objects or cleanup during opening.
- An ordinary read-only operating mode for refused backends; doctor's diagnostic
  exception does not enable normal pool or allocation commands.
- Repairing the existing doctor write probe or proving conditional deletion.
- Changes to the separate provider; only enabling library capabilities are here.
- Detecting non-enforcing Azure-compatible emulators or expanding their support;
  the existing loopback emulator path remains test-only.
- Changing URL grammar, credentials, storage conditions, allocation behavior,
  persisted document formats, or the verified support list beyond FR-1.
- Choosing implementation technologies, public option names or internal design.

## Open questions *(mandatory, empty before G2)*

1. **Q1 - Doctor write probes without consent**: When doctor reports an
   unverified backend as FAIL because no opt-in was supplied, should its existing
   write-probe checks be skipped entirely, or may they still run? The Requirement
   explicitly preserves other read-only checks but does not settle write probes;
   the Requirement author must answer before G2.
