# Feature Specification: Stored document format

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0007-document-format/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Reopen persisted pool and allocation data (Priority: P1)

As a library caller, I want stored pool documents and reservations documents to retain their identities, CIDRs, requests and metadata when another client reads them, so that persisted address assignments remain usable.

**Why this priority**: Every pool and allocation operation depends on interpreting previously stored data.

### Journey 2 - Retain compatible documents when using policy features (Priority: P2)

As a library caller using allocation policies, I want the persisted format to represent strategy, cooldown and release records while retaining the original format for documents that do not need those features.

**Why this priority**: Policy features must coexist with existing stored documents. Policy behaviour itself belongs to BASE-0002 allocation-policy.

### Journey 3 - Recognise incompatible or malformed stored data (Priority: P2)

As a library caller or storage operator, I want a document with an unsupported version or invalid contents to fail recognisably rather than be interpreted as a different document or as valid reservations.

**Why this priority**: Distinguishable failures let callers identify incompatible data without silently accepting corrupt state.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a pool with ID `tenant-a`, **When** its pool document and reservations document are persisted, **Then** the documented object keys are `pools/tenant-a.json` and `reservations/tenant-a.json`, respectively.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorHealthyBackendPassesWithoutChangingData` (reads `reservations/p.json` after creating pool `p`); `cmd/ipam/pool_policy_test.go` `TestPoolListWarnsAboutUnsupportedPools` (reads a document written at `pools/future.json`); key construction in `pool.go` `poolKey` and `reservationsKey`.

- **AC-2**: **Given** a valid version 1 pool document, **When** it is read and serialized again, **Then** its `schema_version`, `kind`, `pool_id`, `incarnation`, `family`, `cidrs`, `parent`, `description` and `labels` are retained, including a parent's `pool_id` and `allocation_id` when present.
  - Evidence: `document_test.go` `TestCodecRootDocumentsRoundTrip` (library pool encode/decode and CIDR preservation); `internal/document/codec_test.go` `TestCodecGoldenFilesRoundTripByteIdentically/pool v1` and `TestCodecPoolRoundTrip` (full field retention at the internal codec boundary).

- **AC-3**: **Given** a valid version 1 reservations document containing ordinary allocations and carve-outs, **When** it is read and serialized again, **Then** it retains `schema_version`, `kind`, `pool_id`, `incarnation` and the allocation-ID-keyed `allocations` object, with each entry's `request`, `cidr`, `description`, `labels` and `created_at`; requests retain their `kind` and their applicable `prefix_length` or `cidr`.
  - Evidence: `document_test.go` `TestCodecRootDocumentsRoundTrip` (library reservations encode/decode and consistency validation); `internal/document/codec_test.go` `TestCodecGoldenFilesRoundTripByteIdentically/reservations v1` and `TestCodecReservationsRoundTrip` (full field retention at the internal codec boundary).

- **AC-4**: **Given** an allocation identified by a valid UUIDv7, **When** its reservations document is serialized and read back, **Then** the allocation is returned under the same ID and no additional allocation is introduced.
  - Evidence: `document_test.go` `TestCodecRootReservationsRoundTripUUIDv7AllocationID`.

- **AC-5**: **Given** a pool using `lowest` or `best-fit`, **When** its pool document is serialized, **Then** `lowest` uses version 1 without a `strategy` field and `best-fit` uses version 2 with `"strategy": "best-fit"`; reading a version 1 pool supplies `lowest` as its strategy.
  - Evidence: `policy_test.go` `TestPolicyDefaultCreateStoresBestFitV2PoolAndV1Reservations` and `TestPolicyCreateWithCooldownStoresV2Reservations`; `updatepolicy_test.go` `TestUpdatePoolPolicyUpgradeAndDowngradeRewriteDocuments` (also asserts no `strategy` after downgrade and reads the version 1 pool as `lowest`).

- **AC-6**: **Given** reservations with cooldown enabled or disabled and with or without release records, **When** they are serialized, **Then** the reservations document uses version 1 exactly when cooldown is disabled and there are no release records, and otherwise uses version 2 with `cooldown` and `released`; a version 2 document with disabled cooldown writes `"cooldown": null`. This choice is independent of the pool document's version.
  - Evidence: `policy_test.go` `TestPolicyDefaultCreateStoresBestFitV2PoolAndV1Reservations`, `TestPolicyCreateWithCooldownStoresV2Reservations` and `TestPolicyReservationsReleaseRecordsRoundTrip`; `updatepolicy_test.go` `TestUpdatePoolPolicyUpgradeAndDowngradeRewriteDocuments`.

- **AC-7**: **Given** valid version 2 documents, **When** they are read, **Then** the pool's stored `strategy`, the reservations' cooldown `mode` and integer `seconds`, and the `released` CIDR-to-expiry entries are available without being discarded.
  - Evidence: `internal/document/codec_v2_test.go` `TestCodecV2DecodesFields` (internal codec boundary; asserts `best-fit`, `strict`, 86400 seconds and a release expiry).

- **AC-8**: **Given** stored version 2 reservations containing `"allocations": {}`, `"cooldown": null` and `"released": {}`, **When** a caller reads them, **Then** reading succeeds with cooldown disabled and no release records, and the next write of that reservations document is version 1 without `cooldown` or `released`.
  - Evidence: UNTESTED at the library level (behaviour in `document.go` `decodeCooldownState` and `encodeReservationsDocument`).

- **AC-9**: **Given** any of the checked-in pool or reservations golden documents for versions 1 and 2, **When** it is decoded and re-encoded without changes, **Then** the bytes are identical; version 1 output does not gain `strategy`, `cooldown` or `released` fields.
  - Evidence: `policy_test.go` `TestPolicyExplicitLowestCreateStoresV1Bytes` (library-written version 1 bytes equal the pre-policy format for both documents); `internal/document/codec_test.go` `TestCodecGoldenFilesRoundTripByteIdentically` (all five subtests, internal codec boundary); `internal/document/codec_v2_test.go` `TestCodecV1BytesUnchangedByV2Fields` (internal codec boundary).

- **AC-10**: **Given** a pool or allocation whose labels are nil, **When** its document is serialized, **Then** its `labels` value is an empty JSON object rather than `null`.
  - Evidence: `internal/document/codec_test.go` `TestCodecNilLabelsEncodeAsObjects` (internal codec boundary).

- **AC-11**: **Given** a stored pool with `"labels": null` or a stored allocation with no `labels` field, **When** the document is read, **Then** the corresponding labels are a non-nil empty map.
  - Evidence: `internal/document/codec_test.go` `TestCodecDecodeNormalizesNullAndAbsentLabels` (internal codec boundary).

- **AC-12**: **Given** pool and reservations data to persist, **When** their documents are serialized, **Then** neither document includes backend `etag`, `owner` or `key` fields, and the pool document does not contain an `allocations` field.
  - Evidence: `internal/document/codec_test.go` `TestCodecNilLabelsEncodeAsObjects` (internal codec boundary; asserts forbidden fields are absent from both outputs).

### Edge cases

- **AC-13**: **Given** a pool document declaring an integer schema version other than 1 or 2, **When** a caller reads that pool document, **Then** reading fails with an error identifiable as `ipam.ErrUnsupportedSchema`, rather than treating the document as version 1.
  - Evidence: `document_test.go` `TestCodecRootMapsUnsupportedSchema` (version 3).

- **AC-14**: **Given** a reservations document declaring an integer schema version other than 1 or 2, **When** a caller reads those reservations, **Then** reading fails with an error identifiable as `ipam.ErrUnsupportedSchema`.
  - Evidence: `client_test.go` `TestAllocateRejectsUnsupportedSchemaWithoutWrites` (version 3).

- **AC-15**: **Given** either document with truncated JSON, the wrong `kind`, or a `pool_id` that differs from the requested pool, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`.
  - Evidence: `document_test.go` `TestCodecRootMapsInvalidDocumentToInvalidArgument` (the six pool and reservations subtests).

- **AC-16**: **Given** a pool document with no `schema_version`, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument` instead of assigning a default version.
  - Evidence: `internal/document/codec_test.go` `TestCodecRejectsInvalidEnvelope/missing schema` asserts internal `document.ErrInvalidDocument`; the public mapping in `document.go` `mapDocumentError` is shared with AC-15.

- **AC-17**: **Given** a version 1 pool document containing `strategy`, or a version 2 pool document with an absent or empty `strategy`, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument` rather than ignoring the version mismatch or supplying a strategy.
  - Evidence: `internal/document/codec_test.go` `TestCodecRejectsInvalidEnvelope/v1 pool with strategy`, `TestCodecRejectsInvalidEnvelope/v2 pool without strategy` and `TestCodecRejectsInvalidEnvelope/v2 pool with empty strategy` assert internal `document.ErrInvalidDocument`; the public mapping in `document.go` `mapDocumentError` is shared with AC-15.

- **AC-18**: **Given** a version 1 reservations document containing `cooldown` or `released`, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`, even when the extra value is `null` for cooldown or `{}` for released.
  - Evidence: `internal/document/codec_test.go` `TestCodecRejectsInvalidEnvelope/v1 reservations with cooldown` and `TestCodecRejectsInvalidEnvelope/v1 reservations with released` assert internal `document.ErrInvalidDocument`; the public mapping in `document.go` `mapDocumentError` is shared with AC-15.

- **AC-19**: **Given** version 2 reservations with `"allocations": null`, or with `released` absent or `null`, **When** a caller reads them, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument` because those collections must be JSON objects.
  - Evidence: `internal/document/codec_test.go` `TestCodecRejectsInvalidEnvelope/v2 reservations with null allocations`, `TestCodecRejectsInvalidEnvelope/v2 reservations without released` and `TestCodecRejectsInvalidEnvelope/v2 reservations with null released` assert internal `document.ErrInvalidDocument`; the public mapping in `document.go` `mapDocumentError` is shared with AC-15.

- **AC-20**: **Given** a version 1 reservations document with absent or null `allocations`, or a version 2 reservations document with absent `allocations`, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`.
  - Evidence: UNTESTED (behaviour in `internal/document/codec.go` `DecodeReservations` and `document.go` `mapDocumentError`).

- **AC-21**: **Given** otherwise valid version 2 reservations with a `released` object but no `cooldown` field, **When** a caller reads them, **Then** cooldown is disabled, as it is for an explicit JSON `null`.
  - Evidence: UNTESTED (behaviour in `internal/document/codec.go` `DecodeReservations` and `document.go` `decodeCooldownState`).

- **AC-22**: **Given** an otherwise valid document containing an unrecognised JSON field other than the version-forbidden fields in AC-17 and AC-18, **When** it is decoded and re-encoded, **Then** that field does not cause rejection and is not retained in the output.
  - Evidence: UNTESTED (behaviour in `internal/document/codec.go` `DecodePool`, `DecodeReservations`, `EncodePool` and `EncodeReservations`).

- **AC-23**: **Given** a version 2 pool whose non-empty `strategy` is neither `lowest` nor `best-fit`, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`; a non-empty string alone is not sufficient for library use. Serialization likewise rejects a strategy other than `lowest` or `best-fit` with `ipam.ErrInvalidArgument`.
  - Evidence: `policy_test.go` `TestPolicyGetPoolRejectsUnknownStoredStrategy` (read rejection); serialization rejection is UNTESTED (behaviour in `document.go` `encodePoolDocument`).

- **AC-24**: **Given** a pool or reservations document with an absent or invalid incarnation rather than the documented 32-character lowercase hexadecimal value, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`; serialization likewise rejects an invalid incarnation with `ipam.ErrInvalidArgument`.
  - Evidence: UNTESTED (incarnation checks in `document.go` `decodePoolDocument`, `decodeReservationsDocument`, `encodePoolDocument` and `encodeReservationsDocument`; the supplied `AGENTS.md` documents the 16-byte hexadecimal representation; its validator is outside the assigned code paths).

- **AC-25**: **Given** a carve-out ID of the form `pool:<child-id>:<incarnation>`, **When** it is serialized and read, **Then** a valid ID is accepted, while serialization rejects a missing incarnation, a 16-character incarnation, uppercase hexadecimal, or a non-hexadecimal character with `ipam.ErrInvalidArgument`.
  - Evidence: `document_test.go` `TestCodecRootReservedAllocationIDValidation` (valid round trip and the four invalid-ID subtests).

- **AC-26**: **Given** an allocation whose map key differs from its ID, **When** its reservations are serialized, **Then** serialization fails with an error identifiable as `ipam.ErrInvalidArgument`; when reading reservations, a stored allocation key that is neither a canonical UUID nor a valid `pool:<child-id>:<incarnation>` ID fails with `ipam.ErrInvalidArgument` (rules in BASE-0005 input-validation).
  - Evidence: UNTESTED (behaviour in `document.go` `encodeReservationsDocument` and `decodeReservationsDocument`).

- **AC-27**: **Given** an allocation whose stored assigned CIDR is malformed or is not in canonical form, **When** its reservations are read, **Then** reading fails with an error identifiable as `ipam.ErrInvalidCIDR`; serialization likewise rejects an invalid or non-canonical assigned CIDR.
  - Evidence: UNTESTED (behaviour in `document.go` `decodeReservationsDocument` and `encodeReservationsDocument`; canonical CIDR rules belong to BASE-0005 input-validation).

- **AC-28**: **Given** a stored `next` allocation request, **When** its reservations are read, **Then** prefix lengths from 0 through 128 bits inclusive are accepted at the document boundary if no request CIDR is supplied, while a negative prefix length, a prefix length greater than 128, or a non-empty request CIDR fails with `ipam.ErrInvalidArgument`. Family-specific allocation validity is separate.
  - Evidence: UNTESTED (behaviour in `document.go` `requestFromDocument`; equivalent serialization checks are in `requestToDocument`).

- **AC-29**: **Given** a stored `explicit` allocation request with a malformed or non-canonical request CIDR, or a non-zero `prefix_length`, **When** its reservations are read, **Then** reading fails with an error identifiable as `ipam.ErrInvalidCIDR` (including the non-zero `prefix_length` case); serialization enforces the corresponding conditions.
  - Evidence: UNTESTED (behaviour in `document.go` `requestFromDocument` and `requestToDocument`).

- **AC-30**: **Given** a stored allocation request whose `kind` is neither `next` nor `explicit`, **When** its reservations are read, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`; serialization also rejects an unknown request kind.
  - Evidence: UNTESTED (behaviour in `document.go` `requestFromDocument` and `requestToDocument`).

- **AC-31**: **Given** version 2 reservations with a malformed or non-canonical release-record CIDR, or an expiry that cannot be parsed as an RFC3339Nano timestamp, **When** a caller reads them, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`; serialization rejects invalid or non-canonical release CIDRs and zero-valued expiry times.
  - Evidence: `policy_test.go` `TestPolicyGetPoolRejectsInvalidStoredCooldownState/non-canonical key` and `TestPolicyGetPoolRejectsInvalidStoredCooldownState/bad expiry` (read rejection); `policy_test.go` `TestPolicyReservationsReleaseRecordsRoundTrip` (non-canonical CIDR write rejection). Malformed CIDR and zero-valued expiry rejection remain UNTESTED (`document.go` `decodeCooldownState` and `encodeCooldownState`); the read/write treatment of a parseable zero timestamp is an Open question.

- **AC-32**: **Given** valid release-record expiry times, **When** reservations are read or serialized, **Then** those expiry times are normalised to UTC, and serialized expiry strings use RFC3339Nano formatting.
  - Evidence: `policy_test.go` `TestPolicyReservationsReleaseRecordsRoundTrip` (RFC3339Nano formatting and round trip of a UTC expiry). Conversion from a non-UTC offset remains UNTESTED (behaviour in `document.go` `decodeCooldownState` and `encodeCooldownState`).

- **AC-33**: **Given** a non-null stored cooldown with an empty or unsupported mode, or seconds outside the policy's supported duration range, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`. The supported modes and duration range belong to BASE-0002 allocation-policy.
  - Evidence: `policy_test.go` `TestPolicyGetPoolRejectsInvalidStoredCooldownState/unknown mode`, `TestPolicyGetPoolRejectsInvalidStoredCooldownState/empty mode`, `TestPolicyGetPoolRejectsInvalidStoredCooldownState/zero seconds` and `TestPolicyGetPoolRejectsInvalidStoredCooldownState/seconds too large`.

- **AC-34**: **Given** a valid `next` request for a prefix length of 0, **When** its reservations are serialized and read back, **Then** the request omits the zero-valued `prefix_length` and empty `cidr` JSON fields and reads back with prefix length 0.
  - Evidence: UNTESTED (wire fields in `internal/document/types.go` `Request`; conversion in `document.go` `requestToDocument` and `requestFromDocument`).

- **AC-35**: **Given** a stored document with `"schema_version": null`, a string or fractional `schema_version`, a numeric `labels` value, or an allocation's invalid `created_at` timestamp string, **When** a caller reads that document, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`, rather than treating the malformed value as an unsupported integer version.
  - Evidence: UNTESTED (JSON decoding in `internal/document/codec.go` `decodeEnvelope`, `DecodePool` and `DecodeReservations`, and public mapping in `document.go` `mapDocumentError`).

- **AC-36**: **Given** a stored pool document with an empty CIDR list, an unparseable or zoned CIDR, mixed address families, overlapping CIDRs, or a non-zero `family` that does not match its CIDRs, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidCIDR`.
  - Evidence: UNTESTED (behaviour in `document.go` `decodePoolDocument` and `canonical.go` `canonicalPrefix` and `canonicalPool`).

- **AC-37**: **Given** a stored pool document with an invalid parent `pool_id`, or a parent `allocation_id` that does not have the form `pool:<this-pool-id>:<valid-incarnation>`, **When** a caller reads it, **Then** reading fails with an error identifiable as `ipam.ErrInvalidArgument`.
  - Evidence: UNTESTED (behaviour in `document.go` `decodePoolDocument` and `canonical.go` `canonicalPool`).

- **AC-38**: **Given** a valid version 2 pool document with `"strategy": "lowest"`, **When** a caller reads it, **Then** its strategy is `lowest`; when that pool document is next serialized, it uses version 1 without a `strategy` field, even without a policy change.
  - Evidence: UNTESTED (behaviour in `document.go` `decodePoolDocument` and `encodePoolDocument`).

- **AC-39**: **Given** a stored pool or allocation has valid JSON metadata outside the caller-write limits in BASE-0005 input-validation AC-33 and AC-34, **When** the corresponding record is read, **Then** the description and label entries are retained and are not rejected for exceeding those write limits.
  - Evidence: `metadata_test.go` `TestMetadataLimitsDoNotAffectReads` (`GetPool`, `ListPools`, `GetAllocation` and `ListAllocations`).

## Non-functional requirements *(mandatory)*

- **NFR-1**: Stored-document compatibility is limited to 2 numeric schema versions, 1 and 2. Source: `internal/document/types.go` `SchemaVersionV1` and `SchemaVersionV2`; `internal/document/codec.go` `validateEnvelope`.
- **NFR-2**: Serialized JSON uses 2 spaces per indentation level and ends with 1 newline character. Source: `internal/document/codec.go` `encode`; byte-level compatibility is checked by `TestCodecGoldenFilesRoundTripByteIdentically`.
- **NFR-3**: Persisted cooldown duration has 1-second resolution and is represented as an integer number of seconds. Source: `internal/document/types.go` `Cooldown.Seconds`; `document.go` `encodeCooldownState` and `decodeCooldownState`.

## Key entities *(include if the feature involves data)*

- **Pool document**: The stored identity, incarnation, address family, CIDR list, optional parent reference, description and labels for a pool, at `pools/<pool-id>.json`. `kind` is `pool`. Version 2 additionally carries `strategy`; version 1 represents `lowest`. A missing parent is serialized as JSON `null`.
- **Reservations document**: The stored pool identity and incarnation with an `allocations` object keyed by allocation ID, at `reservations/<pool-id>.json`. `kind` is `reservations`. Version 2 additionally carries cooldown and release records.
- **Stored allocation**: A reservation's request, assigned CIDR, description, labels and creation timestamp. Its ID is the containing object's key rather than a duplicated `id` member, and its pool ID comes from the reservations document. The request has `kind` plus applicable `prefix_length` or `cidr`; empty request fields are omitted.
- **Carve-out**: An allocation in a parent pool that backs a child pool, identified by `pool:<child>:<incarnation>`, where `<child>` is the child's pool ID and `<incarnation>` identifies that child's lifetime.
- **Parent reference**: A parent `pool_id` and the `allocation_id` of the child's carve-out in that parent. Creation, pairing and cleanup semantics belong to BASE-0001 pool-lifecycle.
- **Incarnation**: The stored identity of one lifetime of a pool. Both document forms carry it. Whether two documents with different incarnations form a usable pool belongs to BASE-0001 pool-lifecycle.
- **Cooldown and release records**: A nullable cooldown object containing `mode` and integer `seconds`, and a `released` object mapping canonical CIDRs to UTC expiry strings. The format does not itself decide whether an expired record is pruned or an address is eligible for reuse; see BASE-0002 allocation-policy.

## Out of scope *(mandatory)*

- Pool creation, updates, deletion, matching document incarnations, incomplete pairs, child carve-outs and residue handling: see BASE-0001 pool-lifecycle.
- Allocation strategy choices, cooldown duration limits, expiry decisions, clock behaviour and pruning release records: see BASE-0002 allocation-policy.
- Allocation selection, containment, overlap, exhaustion, idempotency, release and metadata operations: see BASE-0003 allocations.
- CAS, retries, unknown-outcome recovery and contention: see BASE-0004 concurrency-and-retry.
- General pool ID, allocation ID, CIDR and caller-supplied metadata validation rules: see BASE-0005 input-validation. This spec covers ID and CIDR validation at the stored-document boundary; metadata read compatibility is specified in AC-39, while BASE-0005 AC-36 defines the corresponding library read behavior.
- Error type structure, operation context and general error text: see BASE-0006 error-contract. This spec identifies the document conditions producing those errors.
- Object-storage transport, ETags, listing semantics and backend durability: see BASE-0010 storage-contract and BASE-0011 through BASE-0013 for backend-specific behaviour.
- A public document-codec API, a bulk schema migration command, or preservation of unrecognised JSON fields. Versions are selected when each document is serialized; this capability does not upgrade all documents together.

## Open questions *(mandatory, empty before G2)*

- None. Questions raised during the baseline run were answered by the maintainer on 2026-11-03 and moved to issues: appvia/ipam#31, appvia/ipam#32.
