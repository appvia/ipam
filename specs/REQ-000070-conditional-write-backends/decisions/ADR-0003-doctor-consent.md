# ADR-0003: Separate diagnostic construction from consent and probe execution

**Status**: Proposed for G3
**Trace**: AC-6, AC-12..AC-16, AC-20, AC-24, AC-27, AC-28; NFR-3/NFR-7;
constitution I/II/IV/V.

## Context

`cmd/ipam/doctor.go` calls the raw storage opener, builds a report, and runs
`probeWrite` per discovered or expectation-named pool. Its backend reachability
FAIL currently stops subsequent checks; `finish` aggregates FAIL before WARN.
`doctor_test.go` already records storage writes, and `doctor_wire_test.go`
pins the intentionally wrong ETag used by the existing probe.

The approved clarification requires an unverified no-consent doctor to report
classification FAIL while continuing available read-only checks and issuing
zero write probes, even with write-permission expectations. Ordinary access
must remain refused. A successful existing probe cannot prove conditional
deletion and may change data on a non-enforcing store.

## Decision

Add a separate `backend-classification` row before available diagnostics. Keep
operator consent distinct from the internal storage-opening permission used
only by doctor to construct a diagnostic handle. Pass a separate derived
`probeEnabled` value into diagnostic orchestration. A classified unverified
no-consent backend has FAIL regardless of successful construction or reads.

When probes are disabled, do not enter `probeWrite` at all. Emit existing-schema
WARN rows explicitly saying the probe was skipped for missing consent; include
expectation-only pools, and a global row when no pool can be enumerated. Never
report a skip as permission success. Continue backend/protection/fragmentation
checks where their inputs are available, and preserve actual read failures.
Internal diagnostic consent must not escape into ordinary commands, warning
semantics or classification.

Retain the existing output schema, textual statuses, FAIL/WARN aggregation and
exit 6 for failed reports. Allowed/verified probes retain their existing version,
request semantics and order. Changed help must acknowledge the existing probe's
risk rather than advertise an unconditional no-data-change guarantee.

## Alternatives considered

- Ordinary `OpenStorage` refusal would prevent required read-only diagnosis
  (AC-16).
- Treating doctor as operator opt-in would permit probes without consent and
  misrepresent classification (AC-15, AC-28).
- Calling the probe and relying on denial is still a write attempt and violates
  the zero-write requirement (AC-28/NFR-7).
- A public read-only client or storage wrapper adds a new ordinary access mode
  expressly outside scope; the existing diagnostic orchestration is sufficient.
- A new SKIP status changes the existing schema needlessly; WARN plus an explicit
  detail preserves output/aggregation patterns (AC-13..AC-16, AC-28).

## Consequences

Doctor can diagnose a refused store without claiming it is supported or granting
normal access. The internal exception is security-sensitive and must be tested
with counts of all create/put/delete calls, not only successful writes. Reports
are additive; consumers matching exact check inventories must account for the
classification row and documented skips. Probe repair and conditional-delete
proof remain separate work. No new public access mode or permission is created.
