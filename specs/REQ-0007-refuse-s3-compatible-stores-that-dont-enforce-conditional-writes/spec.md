# Feature Specification: Refuse stores that ignore conditional writes

**Requirement**: REQ-0007 (<https://github.com/appvia/ipam/issues/7>)
**Spec path**: `specs/REQ-0007-refuse-s3-compatible-stores-that-dont-enforce-conditional-writes/spec.md`
**Created**: 2026-11-05
**Status**: Draft - clarification required before G2
**Input**: Approved [brief](brief.md) and [clarifications](clarifications.md)

## User journeys *(mandatory)*

### Journey 1 - Reject unsafe storage before use (Priority: P1)

As a platform operator using the command-line or infrastructure automation
client, I want opening a store to establish that conditional writes are enforced
before it becomes usable, so concurrent changes cannot silently allocate the
same address range because the store ignores write conditions.

**Why this priority**: Silent acceptance of an unsafe store risks conflicting
allocations that are discovered only after networks overlap.

### Journey 2 - Continue using a conforming store (Priority: P1)

As a platform operator, I want a conforming store to remain usable after one
check per backend open, without altering my existing pools or allocations.
Creating a temporary check object is acceptable.

**Why this priority**: The safety check must preserve existing data and avoid
repeating checks during normal work with an already-open backend.

### Journey 3 - Understand an unsuccessful check (Priority: P2)

As a platform operator, I want to distinguish a store that ignores write
conditions from a check that could not finish, so I know whether to select a
conforming store or address an access or availability problem.

**Why this priority**: A failed check must not silently permit unsafe work or
misrepresent an unavailable store as proven nonconforming.

## Functional requirements

- **FR-1**: Opening a backend within this Requirement's scope MUST check its
  conditional-write behavior before making it available for client operations.
  The check applies to both the reference store and alternative compatible
  stores, not only to explicitly overridden store addresses. See AC-1 to AC-3.
- **FR-2**: The check MUST establish both write conditions: creating an object
  only if it does not exist, and replacing an object only if its version still
  matches the caller's expected version. Ignoring either condition MUST cause
  opening to fail. See AC-2 and AC-3.
- **FR-3**: A temporary check object MAY be created. Check activity MUST NOT
  overwrite, remove or otherwise change pre-existing objects, including on a
  nonconforming store or during concurrent opens. See AC-4 and AC-5.
- **FR-4**: A successful check MUST permit normal client operations. Each new
  backend open MUST check again; operations using an already-open backend MUST
  NOT trigger additional opening checks. See AC-1 and AC-6.
- **FR-5**: An unsuccessful or inconclusive check MUST leave the backend
  unavailable for client operations. The caller MUST receive an explanation
  distinguishing observed non-enforcement from an inability to complete the
  check, without revealing credentials. See AC-7 to AC-9.
- **FR-6**: Both command-line and infrastructure automation callers MUST receive
  the refusal before their requested operation changes application data.
  See AC-10.

Scope follows the approved brief and the supported-store rule in
[BASE-0012](../BASE-0012-s3-backend/spec.md). This adds automatic opening checks;
it does not certify every aspect of a store's behavior. The direct-construction
boundary remains open in Q2 below.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a conforming store that enforces both write conditions and
  allows check activity, **When** a caller opens its backend and performs an
  otherwise-valid pool or allocation operation, **Then** opening succeeds and
  the requested operation retains its existing observable result.
- **AC-2**: **Given** a store that permits creation to replace an existing object
  despite the create-only-if-absent condition, **When** a caller opens its
  backend, **Then** opening fails, no usable backend is returned, and the error
  identifies unsupported conditional creation as the reason.
- **AC-3**: **Given** a store that enforces create-only-if-absent but accepts a
  replacement whose expected version does not match the stored version,
  **When** a caller opens its backend, **Then** opening fails, no usable backend
  is returned, and the error identifies unsupported version-conditional
  replacement as the reason.
- **AC-4**: **Given** a store containing existing pools, allocations and unrelated
  objects, **When** an opening check completes or fails, including because the
  store ignores either write condition, **Then** all pre-existing objects retain
  their contents and versions, none are deleted, and the check has not created
  or changed any pool or allocation.
- **AC-5**: **Given** two callers opening the same configured store concurrently,
  **When** their check activity overlaps on a conforming store, **Then** both
  opens succeed without either check altering the other's temporary data or any
  pre-existing object.
- **AC-6**: **Given** a backend whose opening check succeeded, **When** a caller
  performs multiple operations through it and subsequently opens another
  backend for the same store, **Then** the first backend's operations cause
  zero additional opening checks and the second open performs its own check.

### Edge cases

- **AC-7**: **Given** a check cannot establish enforcement because access is
  denied or communication fails, **When** opening returns, **Then** it reports
  that verification could not complete, returns no usable backend, and makes
  no claim that conditional writes were proven unsupported or supported.
- **AC-8**: **Given** an opening check is cancelled or reaches the caller's
  deadline before verification completes, **When** opening returns, **Then** it
  reports cancellation or deadline expiry and does not permit the requested
  application operation to change data.
- **AC-9**: **Given** check activity fails while credentials are configured,
  **When** the caller receives the failure and associated diagnostic output,
  **Then** neither contains credential values.
- **AC-10**: **Given** an otherwise-valid data-changing request from either a
  command-line caller or an infrastructure automation caller against a store
  that ignores either write condition, **When** the caller opens that backend
  for the request, **Then** it receives the opening failure before any pool or
  allocation is created, changed or removed, without requiring a separate
  diagnostic command.
- **AC-11**: **Given** the same store is presented once as the reference store
  and once through an explicitly configured alternative address, **When** a
  caller opens each configuration, **Then** both undergo conditional-write
  verification and are accepted or refused according to the observed behavior,
  not solely the address selection.

## Non-functional requirements *(mandatory)*

- **NFR-1 - Check frequency**: Exactly **1 opening check per successful backend
  open**, with **0 additional opening checks per subsequent client operation**
  using that backend. A check may involve multiple storage operations; this
  limit does not mean one storage request. Covered by AC-6.
- **NFR-2 - Existing-data integrity**: **0 pre-existing objects modified or
  deleted per opening check**, including failed checks and concurrent opens.
  Covered by AC-4 and AC-5.
- **NFR-3 - Confidentiality**: **0 credential values disclosed per check** in
  returned errors or diagnostic output. Covered by AC-9.
- **NFR-4 - Test data**: **0 real personal records, customer identifiers,
  credentials or production values per acceptance-test fixture**. Evidence
  uses synthetic data only, as required by the constitution.

### Success criteria

- Each independently simulated violation of the two write conditions is
  refused before **1 application-data change** can occur (AC-2, AC-3, AC-10).
- A conforming store supports existing valid operations with **1 check per
  backend open** (AC-1, AC-6).
- Existing data has **0 changed or deleted objects** after successful, failed
  and overlapping checks (AC-4, AC-5).

### Verification expectations

Every AC requires a named automated test at implementation, with the
implementation pull request mapping AC identifiers to test names. Controlled
store responses must demonstrate independent enforcement and non-enforcement of
each condition, failed checks and concurrent opens. Tests use synthetic data,
deterministic coordination and no real third-party service calls, following the
constitution. This specification does not claim those tests already exist.

## Key entities

- **Configured store**: The operator-selected storage location and namespace
  containing existing application objects; no new external processor is added.
  Its permitted region is subject to Q3.
- **Backend open**: A caller's attempt to obtain a usable connection to the
  configured store. Each open requires a fresh check.
- **Opening check**: Verification of both conditional-write behaviors before
  application operations are permitted.
- **Temporary check object**: Synthetic, non-application data used only for the
  check; it must not replace or be mistaken for an existing object.

## Out of scope *(mandatory)*

- Proving conditional deletion across every storage vendor.
- Certifying third-party stores or publishing supported vendor/version lists.
- Replacing the operator's responsibility to validate a chosen store against
  the existing storage contract.
- Changing the separate diagnostic command's write probe.
- Repairing existing conflicting allocations or historical data corruption.
- Rechecking enforcement continuously after a backend has opened.
- New bypass controls that permit use after a failed opening check.
- Selecting technologies, request sequences, object naming schemes or code
  structure; those belong to the design stage.

## Open questions *(mandatory, empty before G2)*

1. **Q1 - Temporary-object lifecycle (Requirement owner)**: The clarification
   permits creating a temporary check object but does not define its removal.
   Must successful opening require its removal, and should failed or uncertain
   cleanup refuse opening or permit it with an explicit warning? What residual
   data and operator cleanup obligations are acceptable after interruption?
2. **Q2 - Direct-construction boundary (Requirement owner)**: Does "each time
   backend opened" include direct backend construction by library callers?
   BASE-0012 AC-4 currently promises construction without network calls. Confirm
   whether that promise changes or the check applies at a distinct opening
   boundary, including the protection expected for direct library callers.
3. **Q3 - Data residency (Constitution owner and Requirement owner)**: The
   constitution's UK/EU-only policy is marked "TO CONFIRM by owner". Which
   operator-selected store regions are permitted for the temporary check data,
   and is region eligibility an existing deployment prerequisite or something
   this opening check must enforce? No region or additional processor is
   selected by this specification.
