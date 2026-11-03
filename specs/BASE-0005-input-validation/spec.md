# Feature Specification: Input validation

**Requirement**: Baseline (no Requirement issue)
**Spec path**: specs/BASE-0005-input-validation/spec.md
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Supply valid pool addresses and metadata (Priority: P1)

As a library caller preparing a pool, I supply a pool identifier, address prefixes, and optional metadata. The library checks the identifier and address set, removes address host bits, and puts valid pool prefixes into a consistent order. I can distinguish invalid identifiers from invalid address data.

**Why this priority**: Pool inputs must be valid before the pool workflows described in BASE-0001 pool-lifecycle can use them.

### Journey 2 - Prepare and recognise allocation identities (Priority: P2)

As a library caller preparing an allocation, I use `NewAllocationID()` to obtain a lowercase UUIDv7 identifier. When the library checks allocation identities, it distinguishes canonical UUIDs and the reserved carve-out identity format from malformed identifiers.

**Why this priority**: Stable, valid identities support the allocation workflows described in BASE-0003 allocations without requiring callers to construct UUIDs themselves.

### Journey 3 - Detect inconsistent allocation records (Priority: P2)

As a library caller using `ListAllocations(ctx, poolID)` to read a pool's reservations document, I need inconsistent pool identities, out-of-pool allocation prefixes, overlapping allocations, and requests that do not match their recorded allocation to be rejected.

**Why this priority**: Invalid reservation data must not be treated as a valid allocation set. Document encoding belongs to BASE-0007 document-format; this specification covers the semantic checks in the assigned validation code.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a proposed pool identifier, **When** a caller passes it to `CreatePool`, **Then** it accepts exactly identifiers matching `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`: 1 to 63 ASCII characters, lowercase letters or digits at both ends, and only lowercase letters, digits, or hyphens between them; other identifiers fail with an error matching `ErrInvalidArgument`.
  - Evidence: UNTESTED (behaviour in `canonical.go` `validatePoolID`)

- **AC-2**: **Given** a caller needs an allocation identifier, **When** the caller invokes `NewAllocationID()`, **Then** the result is a lowercase UUID of version 7 that allocation identity validation accepts.
  - Evidence: `canonical_test.go` `TestCanonicalIDs`; `TestCanonicalIDs/accepts generated UUID`

- **AC-3**: **Given** an IPv4 CIDR containing host bits, such as `10.0.0.5/16`, **When** a caller supplies the prefix to `CreatePool`, **Then** it returns the corresponding network prefix, such as `10.0.0.0/16`.
  - Evidence: `canonical_test.go` `TestCanonicalPrefix/masks IPv4 host bits`

- **AC-4**: **Given** an IPv4-mapped IPv6 CIDR whose prefix length is at least 96, **When** a caller supplies the prefix to `CreatePool`, **Then** it produces an IPv4 CIDR with the prefix length reduced by 96 and host bits cleared; for example, `::ffff:10.0.0.0/104` becomes `10.0.0.0/8`.
  - Evidence: `canonical_test.go` `TestCanonicalPrefix/converts mapped IPv4`

- **AC-5**: **Given** an IPv4 `/0` CIDR or an IPv6 `/128` CIDR, **When** a caller supplies the prefix to `CreatePool`, **Then** it accepts the prefix, clearing IPv4 host bits for `/0` and preserving the full IPv6 host address for `/128`.
  - Evidence: `canonical_test.go` `TestCanonicalPrefix/accepts IPv4 zero prefix`; `TestCanonicalPrefix/accepts IPv6 host prefix`

- **AC-6**: **Given** a valid pool identifier and disjoint IPv4 CIDRs supplied out of order or with host bits, **When** a caller creates the pool through `CreatePool`, **Then** it returns masked CIDRs in ascending network-address order and sets the family to `4`.
  - Evidence: `canonical_test.go` `TestCanonicalPool/sorts disjoint prefixes`

- **AC-7**: **Given** adjacent but non-overlapping pool prefixes, such as `10.0.0.0/25` and `10.0.0.128/25`, **When** a caller supplies the prefixes to `CreatePool`, **Then** it accepts both prefixes and retains them as separate entries.
  - Evidence: `canonical_test.go` `TestCanonicalPool/allows adjacent prefixes`

- **AC-8**: **Given** an allocation identifier that is a valid UUID in canonical lowercase, hyphenated form, including a UUID whose version is not 7, **When** a caller supplies the identifier to `GetAllocation`, **Then** it accepts the identifier without requiring UUID version 7.
  - Evidence: UNTESTED (behaviour in `canonical.go` `validateAllocationID`)

- **AC-9**: **Given** a stored carve-out identifier in the form `pool:<child>:<incarnation>`, where the child identifier satisfies AC-1 and the incarnation is exactly 32 lowercase hexadecimal characters, **When** a caller supplies the identifier to `GetAllocation` or `Release`, or reads the stored carve-out through `ListAllocations`, **Then** identity validation accepts the reserved identifier. Public `Allocate` and `UpdateAllocationMetadata` reject reserved IDs with `ErrInvalidArgument`; see BASE-0003 allocations AC-21.
  - Evidence: `canonical_test.go` `TestCanonicalIDs/accepts incarnation keyed child pool reservation`

- **AC-10**: Withdrawn (covered by BASE-0001 pool-lifecycle AC-4)

- **AC-11**: **Given** a valid pool document and a consistent reservations document containing a canonical, in-pool allocation whose recorded next-prefix request matches its CIDR, **When** a caller reads the records through `ListAllocations(ctx, poolID)`, **Then** record validation succeeds.
  - Evidence: `validate_test.go` `TestValidateAcceptsConsistentDocuments`

### Edge cases

- **AC-12**: **Given** an allocation identifier expressed as an arbitrary name rather than a UUID, or an uppercase rendering of a UUID containing letters, **When** a caller supplies the identifier to `GetAllocation`, **Then** it rejects the identifier with an error matching `ErrInvalidArgument`.
  - Evidence: `canonical_test.go` `TestCanonicalIDs/rejects caller chosen name`; `TestCanonicalIDs/rejects uppercase UUID`

- **AC-13**: **Given** a carve-out identifier missing its incarnation, containing an invalid child identifier, or containing a short, long, uppercase, or non-hexadecimal incarnation, **When** a caller supplies the identifier to `GetAllocation`, **Then** it rejects the identifier with an error matching `ErrInvalidArgument`.
  - Evidence: `canonical_test.go` `TestCanonicalIDs/rejects bare child pool reservation`; `TestCanonicalIDs/rejects malformed child pool`; `TestCanonicalIDs/rejects short incarnation`; `TestCanonicalIDs/rejects long incarnation`; `TestCanonicalIDs/rejects uppercase incarnation`; `TestCanonicalIDs/rejects non-hex incarnation`

- **AC-14**: **Given** an invalid prefix value, including an out-of-range prefix length, **When** a caller supplies the invalid prefix value to `CreatePool`, **Then** it rejects the input with an error matching `ErrInvalidCIDR`. Stored CIDR-text rejection belongs to BASE-0007 document-format AC-36.
  - Evidence: UNTESTED (behaviour in `canonical.go` `canonicalNetPrefix`)

- **AC-15**: **Given** an IPv4-mapped IPv6 CIDR with a prefix length shorter than 96, **When** a caller supplies that CIDR to `CreatePool`, **Then** it rejects the input with an error matching `ErrInvalidCIDR`. Stored zoned-CIDR rejection belongs to BASE-0007 document-format AC-36.
  - Evidence: `canonical_test.go` `TestCanonicalPrefix/rejects mapped prefix shorter than 96`

- **AC-16**: **Given** a pool with no CIDRs, **When** a caller supplies it to `CreatePool`, **Then** it rejects the pool with an error matching `ErrInvalidCIDR`.
  - Evidence: `canonical_test.go` `TestCanonicalPool/rejects empty pool`

- **AC-17**: **Given** a pool containing both IPv4 and IPv6 CIDRs, **When** a caller supplies the CIDRs to `CreatePool`, **Then** it rejects the pool with an error matching `ErrInvalidCIDR`.
  - Evidence: `canonical_test.go` `TestCanonicalPool/rejects mixed families`

- **AC-18**: **Given** a pool containing overlapping CIDRs or duplicate CIDRs, **When** a caller supplies the CIDRs to `CreatePool`, **Then** it rejects the pool with an error matching `ErrInvalidCIDR`.
  - Evidence: `canonical_test.go` `TestCanonicalPool/rejects overlap`; `TestCanonicalPool/rejects duplicate`

- **AC-19**: Withdrawn (covered by BASE-0007 document-format AC-36)

- **AC-20**: **Given** an otherwise valid pool specification containing only disjoint IPv6 CIDRs, **When** a caller supplies the specification to `CreatePool`, **Then** it accepts, masks and sorts the CIDRs, and the returned pool has family `6`. Stored-pool normalization is tracked separately in BASE-0007 document-format OQ-2.
  - Evidence: UNTESTED (behaviour in `canonical.go` `canonicalPool`)

- **AC-21**: Withdrawn (covered by BASE-0007 document-format AC-24)

- **AC-22**: **Given** an otherwise valid reservations document names a pool different from the pool being read, **When** a caller reads it through `ListAllocations(ctx, poolID)`, **Then** the call fails with an error matching `ErrInvalidArgument`.
  - Evidence: UNTESTED at the `ListAllocations` level (the document envelope check reached through `document.go` `decodeReservationsDocument` belongs to BASE-0007 document-format; `validate_test.go` `TestValidateRejectsInvalidReservations/reservations pool ID mismatch` covers only the private in-memory validation check)

- **AC-23**: Withdrawn (the allocation map-key/identifier mismatch is a private in-memory state that callers cannot supply through the public API or stored allocation format).

- **AC-24**: Withdrawn (covered by BASE-0007 document-format AC-27)

- **AC-25**: **Given** a stored canonical allocation CIDR belongs to a different address family from its pool or is not wholly contained in any one pool CIDR, **When** a caller reads the records through `ListAllocations(ctx, poolID)`, **Then** the call fails with an error matching `ErrInvalidCIDR`.
  - Evidence: `validate_test.go` `TestValidateRejectsInvalidReservations/allocation outside pool`; `TestValidateRejectsInvalidReservations/allocation family mismatch`

- **AC-26**: **Given** individually valid stored allocation records have overlapping CIDRs, **When** a caller reads the records through `ListAllocations(ctx, poolID)`, **Then** the call fails with an error matching `ErrInvalidCIDR`.
  - Evidence: `validate_test.go` `TestValidateRejectsInvalidReservations/allocations overlap`

- **AC-27**: **Given** a stored next-prefix request passes document-boundary checks but has a prefix length different from its allocated CIDR, **When** a caller reads the records through `ListAllocations(ctx, poolID)`, **Then** the call fails with an error matching `ErrInvalidArgument`. Document-boundary request checks belong to BASE-0007 document-format AC-28.
  - Evidence: UNTESTED (behaviour in `validate.go` `validateRequest`)

- **AC-28**: **Given** a stored explicit request passes document-boundary checks, **When** a caller reads the records through `ListAllocations(ctx, poolID)`, **Then** a request CIDR different from the allocated CIDR causes the call to fail with an error matching `ErrInvalidCIDR`, while a matching request CIDR passes the request check. Document-boundary request checks belong to BASE-0007 document-format AC-29.
  - Evidence: UNTESTED (behaviour in `validate.go` `validateRequest`)

- **AC-29**: Withdrawn (covered by BASE-0007 document-format AC-30)

- **AC-30**: **Given** a supplied allocation identifier uses a compact, braced, or URN UUID spelling instead of canonical lowercase hyphenated form, or a reserved `pool:` identifier has more than three colon-separated components, **When** a caller supplies the identifier to `GetAllocation`, **Then** it rejects the identifier with an error matching `ErrInvalidArgument`.
  - Evidence: UNTESTED (behaviour in `canonical.go` `validateAllocationID` and `parseReservedAllocationID`)

- **AC-31**: **Given** a valid new pool specification whose labels are `nil`, **When** a caller successfully creates the pool through `CreatePool`, **Then** the returned pool has a non-nil empty labels map.
  - Evidence: UNTESTED (behaviour in `canonical.go` `cloneLabels` and `canonicalPool`; `pool.go` `CreatePool`)

- **AC-32**: **Given** a valid new pool specification with a supplied labels map, **When** a caller successfully creates the pool through `CreatePool` and then changes either the supplied map or the returned pool's labels map, **Then** the other map retains its entries unchanged.
  - Evidence: UNTESTED (behaviour in `canonical.go` `cloneLabels` and `canonicalPool`; `pool.go` `CreatePool`)

## Non-functional requirements *(mandatory)*

- **NFR-1**: Pool identifiers contain at least 1 and at most 63 ASCII characters. Source: `canonical.go` `poolIDPattern`.
- **NFR-2**: An incarnation contains exactly 32 lowercase hexadecimal characters. Source: `validate.go` `validIncarnation`, used by `canonical.go` `parseReservedAllocationID` and by record validation.
- **NFR-3**: IPv4-mapped IPv6 CIDRs require at least 96 prefix bits before conversion to IPv4. Source: `canonical.go` `canonicalNetPrefix`.

## Key entities *(include if the feature involves data)*

- **Pool input**: A pool identifier, a nonempty collection of CIDRs, optional description and labels, and an optional parent reference. The address family is derived from the CIDRs and returned on the pool (`4` for IPv4 or `6` for IPv6).
- **CIDR**: An address and prefix length. Canonical pool CIDRs have host bits cleared and use IPv4 notation for IPv4-mapped addresses.
- **Allocation identity**: A canonical lowercase UUID, normally generated as UUIDv7 by `NewAllocationID()`, or a carve-out identifier validated as in AC-9 (see BASE-0007 document-format Key entities).
- **Allocation record**: An allocation identifier, its pool identifier, its allocated canonical CIDR, and its original next-prefix or explicit request.
- **Reservations document**: See BASE-0007 document-format Key entities.
- **Incarnation**: A 32-character lowercase hexadecimal identity associated with a pool lifetime. Its generation and lifecycle meaning belong to BASE-0001 pool-lifecycle.

## Out of scope *(mandatory)*

- Pool creation, retrieval, listing, updates, deletion, parent-child relationships, and incarnation pairing and recovery belong to BASE-0001 pool-lifecycle. This includes checking that a parent's carve-out identifies the correct child and handling unequal document incarnations; `validate_test.go` `TestValidateRejectsInvalidReservations/incarnation mismatch` covers that neighbouring behaviour.
- Allocation strategies, accepted policy values, cooldown modes, and cooldown duration limits belong to BASE-0002 allocation-policy, including the policy validation functions located in `validate.go`.
- Choosing an allocation, request construction at the allocation API boundary, idempotency, release, and allocation metadata updates belong to BASE-0003 allocations.
- Retry behaviour and client options belong to BASE-0004 concurrency-and-retry.
- Public error types, wrapping, reason strings, and message construction belong to BASE-0006 error-contract. Error identities are named here only to describe each validation outcome.
- Storage keys, JSON schemas, decoding, and schema-version compatibility belong to BASE-0007 document-format.
- Backend URL parsing belongs to BASE-0009 backend-url; CLI parsing and diagnostics belong to BASE-0014 cli.
- These rules describe checks used by library workflows, not a separately exported general-purpose validation API. No new validation rules or metadata restrictions are introduced by this baseline.

## Open questions *(mandatory, empty before G2)*

- **OQ-1**: The assigned canonicalisation and validation code currently imposes no description or label constraints, despite metadata rules being named in the capability description. BASE-0001 pool-lifecycle AC-4 records preservation of supplied values, not a guarantee of unrestricted metadata. Maintainer answer (2026-11-03, baseline-spec run): metadata should be limited; a Bug or Story citing this spec is to be raised after merge, and this entry is then removed.
