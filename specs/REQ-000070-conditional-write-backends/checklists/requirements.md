# Specification Quality Checklist: Explicit consent for unverified backends

**Purpose**: Validate specification completeness before G2
**Created**: 2026-11-05
**Reviewed**: 2026-11-07
**Feature**: [spec.md](../spec.md)

## Content quality

- [x] No implementation design or technology selection; backend names and flags identify existing user-facing contracts.
- [x] Focused on user value and business needs.
- [x] Written for operators and dependent application authors.
- [x] All mandatory Appvia template sections completed.

## Requirement completeness

- [x] No unresolved clarifications remain.
- [x] Requirements are fully unambiguous: doctor skips write probes without consent and explains why.
- [x] Defined requirements are testable through public behavior.
- [x] Measurable outcomes are expressed without prescribing implementation.
- [x] All acceptance scenarios are defined: AC-28 covers write-probe skipping without consent.
- [x] Edge cases include opt-in precedence, invalid input, redaction and misleading hostnames.
- [x] Scope and exclusions are explicit.
- [x] Sources and existing-contract dependencies are identified.

## Feature readiness

- [x] Defined functional requirements trace to numbered Given/When/Then criteria.
- [x] Journeys cover default refusal, deliberate consent and diagnosis.
- [x] NFRs carry numerical quantities and units.
- [x] No implementation details leak into the specification.
- [x] Ready for G2 review; planning still requires human specification approval.

## Traceability and review notes

- FR-1: AC-1 through AC-4, AC-25; FR-2: AC-5, AC-6, AC-26.
- FR-3: AC-7, AC-9 through AC-11, AC-26; FR-4: AC-6, AC-12, AC-24.
- FR-5: AC-8; FR-6: AC-13 through AC-16, AC-28.
- FR-7: AC-3, AC-17, AC-23, AC-27; FR-8: AC-1 through AC-4, AC-18.
- FR-9: AC-19, AC-21; FR-10: AC-20; FR-11: AC-22.
- Brief outcome OM-1: AC-5, AC-6, AC-11, AC-17, AC-26.
- The Requirement author answered Q1 on 2026-11-07: skip existing write probes
  without consent and explain the skip. The answer is recorded in
  [clarifications.md](../clarifications.md); FR-6, AC-28 and NFR-7 reflect it.
- Related baseline overlays preserve existing AC identifiers and evidence while
  distinguishing the proposed support gate from current baseline behavior.
- All 28 acceptance criteria retain sequential identifiers and Given/When/Then
  clauses. All 7 NFRs carry quantities and units; the spec is below 400 lines.
- Required next step: human G2 review of the updated draft; no answers remain
  outstanding and no approval is implied by checklist completion.
