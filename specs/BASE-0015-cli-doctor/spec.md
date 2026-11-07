# Feature Specification: CLI backend doctor

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0015-cli-doctor/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## Proposed Requirement overlay

[REQ-000070](../REQ-000070-conditional-write-backends/spec.md) adds a classification
check: PASS for verified, WARN for allowed unverified, FAIL for unverified without
consent. This check is included in both existing output formats and participates
in the existing result and exit-status aggregation. Classification FAIL does not
itself suppress other read-only checks (Requirement AC-13 through AC-16). Thus
existing exact check inventories below gain this result. For unverified backends
without consent, all existing write probes are skipped, including when explicit
write-permission expectations are supplied; the operator receives a message
explaining the missing consent, read-only checks continue, and classification
FAIL still yields a failed report and exit status 6 (Requirement AC-28). With
consent, existing probe behavior is unchanged. No repair of the existing probe
or proof of conditional deletion is included. Existing AC identifiers and
baseline evidence are retained.

## User journeys *(mandatory)*

### Journey 1 - Check a backend before using it (Priority: P1)

As an IPAM operator, I run `ipam doctor` against my configured backend to check reachability, recoverability, hierarchical-namespace compatibility and per-pool write access. I read the default table or request JSON for automation, and distinguish failed checks from warnings through the report and exit code.

**Why this priority**: An operator needs to discover unsupported or unsafe backend settings before relying on the backend for allocations.

### Journey 2 - Verify an identity's pool grants (Priority: P2)

As an access administrator, I repeat `--expect-write POOL` and `--expect-no-write POOL` to describe the grants an identity should have. Doctor checks those expectations against existing reservations documents without creating or deleting diagnostic objects.

**Why this priority**: The administrator can verify both an intended grant and an intended denial without allocating address space.

### Journey 3 - Investigate fragmented free space (Priority: P3)

As a pool operator, I inspect each pool's fragmentation row to distinguish free addresses from usable blocks at the automatically selected reference prefix length. I see active cooling ranges separately and can consume the statistics in JSON.

**Why this priority**: Free address counts alone do not explain why a reference-sized request may not fit. Statistics definitions and reference selection belong to [BASE-0008 pool-stats](../BASE-0008-pool-stats/spec.md).

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** an otherwise healthy backend, a readable and writable pool whose fragmentation statistics do not trigger a warning, and conditional writes that are honoured, **When** an operator runs `doctor --expect-write POOL -o json`, **Then** the report has result `pass`, the backend, data-protection, hierarchical-namespace and pool write-probe checks are `PASS`, the exit code is `0`, and the reservations bytes and version remain unchanged.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorHealthyBackendPassesWithoutChangingData`; `cmd/ipam/doctor_integration_test.go` `TestDoctorAzurite`, `TestDoctorS3Emulator` additionally assert unchanged cloud object versions and passing write probes.

- **AC-2**: **Given** otherwise non-failing checks and a readable pool whose actual write permission matches `--expect-write POOL` or `--expect-no-write POOL`, **When** doctor probes that pool, **Then** its write-probe check is `PASS` with detail `write allowed; conditional writes honoured` for expected write access or `write denied` for expected write denial, and the command exits `0`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorWriteExpectations/writable and write expected`, `TestDoctorWriteExpectations/denied and no write expected`; `cmd/ipam/doctor_wire_test.go` `TestProbeWriteSendsWellFormedETag/azblob/contributor expect-write`, `TestProbeWriteSendsWellFormedETag/s3/reader expect-no-write` verify permission classification without an accepted write.

- **AC-3**: **Given** a readable pool whose actual write permission contradicts `--expect-write POOL` or `--expect-no-write POOL`, **When** doctor probes that pool, **Then** its write-probe check is `FAIL` with detail `write denied, but --expect-write was given` for denied expected access or `write allowed, but --expect-no-write was given` for allowed unexpected access, and the command exits `6`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorWriteExpectations/denied but write expected`, `TestDoctorWriteExpectations/writable but no write expected`; `cmd/ipam/doctor_wire_test.go` `TestProbeWriteSendsWellFormedETag/azblob/reader expect-write`, `TestProbeWriteSendsWellFormedETag/s3/contributor expect-no-write` assert the exact details.

- **AC-4**: **Given** otherwise non-failing checks, readable reservations and an identity denied write access to a pool with no explicit permission expectation, **When** doctor probes that pool, **Then** the write-probe check is `PASS` with detail `write denied` and the command exits `0`; write denial alone is not a failed diagnosis.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorWriteExpectations/denied without expectation`; `cmd/ipam/doctor_wire_test.go` `TestProbeWriteSendsWellFormedETag/s3/reader` asserts the exact detail.

- **AC-5**: **Given** backend inspection reports either versioning or soft delete enabled, **When** doctor checks data protection, **Then** the data-protection check is `PASS`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorDataProtectionAndNamespace/versioning on`, `TestDoctorDataProtectionAndNamespace/soft delete on`.

- **AC-6**: **Given** backend inspection reports both versioning and soft delete disabled, **When** doctor checks data protection, **Then** the data-protection check is `FAIL` and the command exits `6`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorDataProtectionAndNamespace/both off`.

- **AC-7**: **Given** no protection feature is confirmed enabled and at least one protection property is unknown, with no other failed check, **When** doctor checks data protection, **Then** the data-protection check is `WARN` and the command exits `0`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorDataProtectionAndNamespace/unknown`.

- **AC-8**: **Given** otherwise non-failing checks and a backend namespace property reported as enabled, disabled or unknown, **When** doctor checks hierarchical namespace, **Then** that check is respectively `FAIL`, `PASS` or `WARN`; the enabled case exits `6`, and the other cases exit `0`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorDataProtectionAndNamespace/hns on`, `TestDoctorDataProtectionAndNamespace/hns off`, `TestDoctorDataProtectionAndNamespace/hns unknown`.

- **AC-9**: **Given** an otherwise healthy backend that provides no inspection interface, **When** doctor runs, **Then** both data-protection and hierarchical-namespace checks are `WARN`, rather than treating absent inspection support as a failure, and the command exits `0`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorStoreWithoutInspectorWarns`.

- **AC-10**: **Given** a healthy `file://` backend with a readable pool, **When** an operator runs doctor expecting that pool to be writable, **Then** data protection is a `WARN` described as not applicable to file backends, the pool write probe is `PASS`, and the command exits `0`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorFileBackendNotApplicable`.

- **AC-11**: **Given** an operator has not selected JSON output, **When** doctor renders its report, **Then** stdout contains a table identifying checks, statuses and details, including the pool write-probe row.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorTableOutput`.

- **AC-12**: **Given** a healthy pool with an automatically selected reference prefix length, **When** doctor reports fragmentation, **Then** the fragmentation check is `PASS`, its single-line detail includes utilization, free and total addresses, largest free prefix length and reference allocation metrics, and its JSON statistics include counts, reference length, strategy, cooldown and a free-block histogram.
  - Evidence: `cmd/ipam/doctor_fragmentation_test.go` `TestDoctorFragmentationHealthyPoolPasses`.

- **AC-13**: **Given** otherwise non-failing checks and a pool meeting the fragmentation warning boundary in AC-30, **When** doctor reports fragmentation, **Then** JSON shows the fragmentation row as `WARN`, the report result is `warn`, the command exits `0`, and the table ends with `RESULT: warn`.
  - Evidence: `cmd/ipam/doctor_fragmentation_test.go` `TestDoctorFragmentedPoolWarnsWithExitZero`.

- **AC-14**: **Given** a pool whose statistics have no reference prefix length, **When** doctor reports fragmentation, **Then** the check is `PASS`, its detail omits allocatable-capacity and index clauses, and JSON statistics report reference length `0` and an empty fragmentation-index string.
  - Evidence: `cmd/ipam/doctor_fragmentation_test.go` `TestDoctorFragmentationNoReferenceOmitsParts`.

- **AC-15**: **Given** a pool has an active cooling range, **When** doctor reports fragmentation, **Then** the detail includes the active cooling count separately from geometric allocation metrics; those metrics do not subtract cooling ranges even when strict cooldown prevents allocation, and the cooling clause disappears after expiry.
  - Evidence: `cmd/ipam/doctor_cooling_test.go` `TestDoctorFragmentationReportsCoolingSeparately`.

- **AC-16**: **Given** an operator selects `-o json`, **When** doctor renders a report, **Then** stdout follows the report and statistics schema described under Key entities: each check has its name, status and detail, the pool field is absent for global checks, and statistics are present only for a successfully computed fragmentation check.
  - Evidence: UNTESTED (complete schema and omission rules in `cmd/ipam/doctor.go` `checkResult`, `doctorReport`, `fragmentationJSON`, `fragmentationStats`, `printDoctor`).

- **AC-17**: **Given** a successful backend check, discovered pools and repeatable permission expectations, **When** doctor selects and reports pools, **Then** it includes the union of discovered and explicitly named pools, diagnoses each distinct pool once, accepts repeated identical expectations, and emits global checks first, then all write probes in sorted pool-ID order, then all fragmentation checks in the same order.
  - Evidence: UNTESTED (complete selection, deduplication and ordering contract in `cmd/ipam/doctor.go` `writeExpectations`, `diagnose`; fragmentation-after-probes alone is asserted by `cmd/ipam/doctor_fragmentation_test.go` `TestDoctorFragmentationHealthyPoolPasses`).

- **AC-18**: **Given** completed check results and successful report rendering, **When** doctor determines the report outcome, **Then** any `FAIL` takes precedence and yields result `fail` with exit `6`; a `fail` result also prints `ipam: doctor: <N> check(s) failed` to stderr, where N is the number of `FAIL` checks. Otherwise any `WARN` yields result `warn` with exit `0`, and an all-`PASS` report yields result `pass` with exit `0`.
  - Evidence: UNTESTED (full aggregation, failure precedence and exact stderr text in `cmd/ipam/doctor.go` `finish`, `doctorCommand`, `doctorFailedError.Error` and `cmd/ipam/main.go` `runCLI`; pass and warning results have individual scenario coverage, but failure-result aggregation and the exact stderr text are not directly asserted).

### Edge cases

- **AC-19**: **Given** the backend's pool listing contains no recognized pool documents and no pools are named in expectations, **When** doctor checks the backend, **Then** the backend check is `PASS` without requiring a pool-document read, protection and namespace checks still run, and there are no pool write-probe or fragmentation rows.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/doctor.go` `checkBackend`, `diagnose`).

- **AC-20**: **Given** a pool listing contains direct `pools/<id>.json` documents alongside nested keys, empty IDs or non-JSON keys, **When** doctor checks backend reachability, **Then** only direct, non-empty pool document names count as discovered pools, and readability is sampled from the first discovered document rather than verified for every discovered pool.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/doctor.go` `checkBackend`).

- **AC-21**: **Given** listing pools fails through a connectivity error or access denial, **When** doctor runs, **Then** the backend check is `FAIL`, the command exits `6`, and no writes are attempted.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorUnreachableBackendFails/network`, `TestDoctorUnreachableBackendFails/denied`.

- **AC-22**: **Given** the pool listing succeeds but the sampled pool document cannot be read, **When** doctor checks the backend, **Then** the backend check is `FAIL` and the command exits `6`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorUnreadablePoolDocumentFails`.

- **AC-23**: **Given** a selected pool's reservations document is absent or unreadable, including a pool named by `--expect-write`, **When** doctor attempts its write probe, **Then** that probe is skipped with `WARN` and no write to that pool; with no failed checks, the report result is `warn` and the exit code is `0`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorUnreadableReservationsWarnsWithoutWriting` covers unreadable pool `p` and absent expected pool `q`.

- **AC-24**: **Given** a backend accepts the probe's deliberately non-matching conditional write, **When** doctor probes a readable reservations document, **Then** the write-probe check is `FAIL`, its detail identifies ignored conditional writes, the command exits `6`, and stderr reports the failed-check count as specified in AC-18.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorFailsWhenConditionalWritesAreIgnored`; stderr count and exact text are not asserted; see AC-18 (UNTESTED).

- **AC-25**: **Given** a conditional-write probe returns an error other than a version-precondition failure or access denial, **When** doctor classifies that probe, **Then** the write-probe check is `FAIL` and the command exits `6`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorWriteExpectations/probe error`.

- **AC-26**: **Given** backend inspection returns an error, **When** doctor checks protection, **Then** the data-protection check is `FAIL` and the command exits `6`.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorInspectErrorFails`.

- **AC-27**: **Given** backend reachability succeeds but inspection fails, **When** doctor continues its diagnosis, **Then** hierarchical namespace is `WARN` because it could not be determined, and pool write probes and fragmentation checks still run despite the failed data-protection check.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/doctor.go` `checkProtection`, `diagnose`).

- **AC-28**: **Given** a pool's statistics cannot be decoded because of an unsupported schema and no other check fails, **When** doctor reports fragmentation, **Then** that row is `WARN`, describes an unsupported schema without statistics, the report result is `warn`, and the command exits `0`.
  - Evidence: `cmd/ipam/doctor_fragmentation_test.go` `TestDoctorFragmentationUnsupportedSchemaWarns`.

- **AC-29**: **Given** statistics fail because the pool is not found or because of another read error, **When** doctor reports fragmentation, **Then** it emits `WARN` without statistics, identifies the failure only as `not found` or `read error` respectively, and does not include the underlying error text; a statistics failure never makes that row `FAIL`.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/doctor.go` `checkFragmentation`).

- **AC-30**: **Given** fragmentation statistics are available, **When** doctor evaluates the warning boundary, **Then** an index of exactly `0.5` with capacity exactly `4` is sufficient for `WARN`; capacity below `4`, an index below `0.5`, or an unavailable index leaves the fragmentation check `PASS`.
  - Evidence: UNTESTED (exact boundaries in `cmd/ipam/doctor.go` `fragmentationMinCapacity`, `checkFragmentation`; the severe-fragmentation scenario in AC-13 does not exercise these cutoffs).

- **AC-31**: **Given** a pool has no free blocks, **When** doctor formats its available statistics, **Then** the detail uses `none` for largest free block and free-block histogram, while JSON uses an empty `largest_free` string and an empty `free_blocks` array.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/doctor.go` `fragmentationDetail`, `fragmentationStats`).

- **AC-32**: **Given** an operator supplies a positional argument to `doctor`, **When** the command validates its arguments, **Then** it exits with usage code `2` without performing a write probe.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorUsageErrors/extra argument`.

- **AC-33**: **Given** the same pool is named in both `--expect-write` and `--expect-no-write`, **When** doctor validates expectations, **Then** it rejects the conflicting expectations with usage code `2` and performs no writes.
  - Evidence: `cmd/ipam/doctor_test.go` `TestDoctorUsageErrors/pool in both lists`.

- **AC-34**: **Given** an expectation names an empty pool ID, `.`, `..`, or an ID containing a forward slash or backslash, **When** doctor validates expectations, **Then** it rejects the name with usage code `2` before opening or probing the backend.
  - Evidence: UNTESTED (complete invalid-name boundary in `cmd/ipam/doctor.go` `writeExpectations`; `cmd/ipam/doctor_test.go` `TestDoctorUsageErrors/pool with a slash` asserts the slash-containing case).

- **AC-35**: **Given** report output cannot be written, **When** doctor renders JSON or flushes and completes its table, **Then** the command exits `1` with the output error on stderr, even when the report contains `FAIL` checks; the output error takes precedence over the failed-check exit code `6`.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/doctor.go` `printDoctor`, `doctorCommand` and `cmd/ipam/main.go` `runCLI`, `exitCode`; common error handling is owned by BASE-0014 cli).

- **AC-36**: **Given** a pool-listing, sampled pool-read, inspection, non-not-found reservations-read or unexpected probe error occurs, **When** doctor reports that check, **Then** the detail identifies the failed operation and includes the underlying error message; this differs from the error-class-only fragmentation diagnostics.
  - Evidence: UNTESTED (message construction in `cmd/ipam/doctor.go` `checkBackend`, `checkProtection`, `probeWrite`).

- **AC-37**: **Given** the backend check fails while listing pools or reading the sampled pool document, **When** doctor finishes the diagnosis, **Then** the report contains only the `backend` check with status `FAIL` and result `fail`; no data-protection, hierarchical-namespace, write-probe or fragmentation rows are emitted, backend inspection is skipped, and no writes are attempted.
  - Evidence: UNTESTED (the early return in `cmd/ipam/doctor.go` `diagnose`; AC-21 and AC-22 cover the failed backend check and exit code, but their tests do not assert the one-row report or skipped inspection).

## Non-functional requirements *(mandatory)*

- **NFR-1**: Diagnosis issues at most `1` conditional-write probe per selected pool, sent only to that pool's `reservations/<pool>.json` document, never its pool document or any other key; it issues `0` such probes for a pool whose reservations cannot be read, `0` object-creation operations and `0` object-deletion operations. Source: `cmd/ipam/doctor.go` `diagnose`, `probeWrite`; the operation count is asserted by `cmd/ipam/doctor_test.go` `TestDoctorHealthyBackendPassesWithoutChangingData`.
- **NFR-2**: Utilization is formatted to `1` decimal place as a percentage in human-readable detail and `4` decimal places as a fraction in JSON; a present fragmentation index uses `2` decimal places in both formats. Source: `cmd/ipam/doctor.go` `fragmentationDetail`, `fragmentationStats`.
- **NFR-3**: Doctor adds `0` access-propagation retry opt-ins to its backend operations. Source: `cmd/ipam/doctor.go` `doctorCommand`, `diagnose`; `cmd/ipam/doctor_test.go` `TestDoctorHealthyBackendPassesWithoutChangingData` asserts that this opt-in was not enabled. The opt-in's storage semantics belong to [BASE-0010 storage-contract](../BASE-0010-storage-contract/spec.md).

## Key entities *(include if the feature involves data)*

- **Doctor report**: JSON object with lowercase `result` (`pass`, `warn`, `fail`) and a `checks` array. The default table has `CHECK`, `POOL`, `STATUS` and `DETAIL` columns, displays `-` for a global check's pool, and ends with `RESULT: <result>`.
- **Check result**: `check`, optional `pool`, uppercase `status` (`PASS`, `WARN`, `FAIL`), `detail`, and optional `stats`. Check names are `backend`, `data-protection`, `hierarchical-namespace`, `write-probe` and `fragmentation`.
- **Permission expectation**: A pool ID paired with required write permission or required write denial, supplied through repeatable `--expect-write` or `--expect-no-write` flags. An expectation adds its pool to diagnosis; it does not restrict diagnosis to explicitly named pools.
- **Fragmentation statistics**: JSON fields `utilization`, `total`, `free`, `allocatable`, `capacity`, `fragmentation_index`, `largest_free`, `free_blocks`, `reference_length`, `strategy` and `cooldown`. Address and capacity counts are decimal strings, utilization is a fractional string, and the index is a fixed-decimal string. `reference_length` is an integer; each free-block item has integer `prefix_length` and `count`. `largest_free` is the full CIDR, not just its prefix length. Without a reference length, `allocatable`, `capacity` and `fragmentation_index` are empty strings; an unavailable index is also an empty string. Cooldown is `none` or `<mode> <Go duration>`, for example `strict 1h0m0s`. Active cooling counts appear separately in detail. Metric definitions and automatic reference selection belong to BASE-0008 pool-stats.
- **Backend protection properties**: Enabled, disabled or unknown reports for versioning, soft delete and hierarchical namespace. The inspection interface and meanings of these properties belong to BASE-0010 storage-contract.

## Out of scope *(mandatory)*

- Pool-statistics calculations, reference-prefix selection rules and cooldown allocation policy: see [BASE-0008 pool-stats](../BASE-0008-pool-stats/spec.md).
- Backend URL grammar and URL rejection rules: see [BASE-0009 backend-url](../BASE-0009-backend-url/spec.md).
- Backend implementation details, conditional-write guarantees, storage sentinels, inspection and access-propagation retry semantics: see [BASE-0010 storage-contract](../BASE-0010-storage-contract/spec.md).
- Shared backend selection, common output-option validation, general stderr sanitization and exit-code handling, and pool/allocation/version commands: see [BASE-0014 cli](../BASE-0014-cli/spec.md). This spec owns doctor's failed-check exit code `6`.
- Repairing pool documents, enabling versioning or soft delete, changing backend settings, allocating addresses, and creating or deleting diagnostic objects.
- Detecting ignored conditional DELETE operations: doctor does not attempt a delete because that probe could remove data.

## Open questions *(mandatory, empty before G2)*

- None. Questions raised during the baseline run were answered by the maintainer on 2026-11-03 and moved to issues: appvia/ipam#45, appvia/ipam#46.
