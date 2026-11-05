# Specification Quality Checklist: Explicit consent for unverified backends

**Purpose**: Validate specification completeness before G2
**Created**: 2026-11-05
**Feature**: [spec.md](../spec.md)

## Content quality

- [x] No implementation design or technology selection; backend names and flags identify existing user-facing contracts.
- [x] Focused on user value and business needs.
- [x] Written for operators and dependent application authors.
- [x] All mandatory Appvia template sections completed.

## Requirement completeness

- [ ] No unresolved clarifications remain.
- [ ] Requirements are fully unambiguous: doctor write probes without consent need Q1 answered.
- [x] Defined requirements are testable through public behavior.
- [x] Measurable outcomes are expressed without prescribing implementation.
- [ ] All acceptance scenarios are defined: the write-probe branch awaits Q1.
- [x] Edge cases include opt-in precedence, invalid input, redaction and misleading hostnames.
- [x] Scope and exclusions are explicit.
- [x] Sources and existing-contract dependencies are identified.

## Feature readiness

- [x] Defined functional requirements trace to numbered Given/When/Then criteria.
- [x] Journeys cover default refusal, deliberate consent and diagnosis.
- [x] NFRs carry numerical quantities and units.
- [x] No implementation details leak into the specification.
- [ ] Ready for G2 or planning: Q1 must be answered first.

## Traceability and review notes

- FR-1: AC-1 through AC-4, AC-25; FR-2: AC-5, AC-6, AC-26.
- FR-3: AC-7, AC-9 through AC-11, AC-26; FR-4: AC-6, AC-12, AC-24.
- FR-5: AC-8; FR-6: AC-13 through AC-16, with Q1 outstanding.
- FR-7: AC-3, AC-17, AC-23, AC-27; FR-8: AC-1 through AC-4, AC-18.
- FR-9: AC-19, AC-21; FR-10: AC-20; FR-11: AC-22.
- Brief outcome OM-1: AC-5, AC-6, AC-11, AC-17, AC-26.
- Review found one scope ambiguity, quoted from FR-6: "Write-probe behavior
  without consent is Q1 below." No behavior for that branch has been invented.
- Related baseline overlays preserve existing AC identifiers and evidence while
  distinguishing the proposed support gate from current baseline behavior.
- Required next step: the author answers Q1 on the Requirement issue; update the
  specification with the corresponding acceptance scenario before G2.
