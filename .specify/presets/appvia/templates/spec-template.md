# Feature Specification: [FEATURE NAME]

**Requirement**: [REQ-NNNNNN] ([owner/repo]#[issue])
**Spec path**: `specs/[REQ-NNNNNN-slug]/spec.md`
**Created**: [DATE]
**Status**: Draft
**Input**: Approved brief `specs/[REQ-NNNNNN-slug]/brief.md` and `clarifications.md`

## User journeys *(mandatory)*

<!--
  One journey per user goal, ordered by importance (P1 first).
  Each journey must be testable on its own.
-->

### Journey 1 - [Brief title] (Priority: P1)

[Describe the journey in plain language.]

**Why this priority**: [The value it delivers]

## Acceptance criteria *(mandatory)*

<!--
  Number every criterion AC-1, AC-2 ... and never renumber after G2.
  Stories, plans, tests and Bugs cite these ids, so each must be testable on its own.
-->

- **AC-1**: **Given** [initial state], **When** [action], **Then** [expected outcome]
- **AC-2**: **Given** [initial state], **When** [action], **Then** [expected outcome]

### Edge cases

- **AC-3**: **Given** [boundary condition], **When** [action], **Then** [expected outcome]

## Non-functional requirements *(mandatory)*

<!--
  Every NFR has a number and a unit. "Fast" or "secure" is not an NFR.
-->

- **NFR-1**: [Quality] [comparator] [number] [unit], e.g. "p95 API latency is at most 300 ms at 50 requests per second"
- **NFR-2**: [Accessibility, availability or security limit with a number and unit]

## Key entities *(include if the feature involves data)*

- **[Entity]**: [What it represents and its key attributes, without implementation]

## Out of scope *(mandatory)*

- [What this spec will not deliver]

## Assumptions *(mandatory; may be empty)*

<!--
  Informed-guess rule: when a detail is missing but has a reasonable default, record it here
  as an assumption instead of asking. Merging the PR confirms every assumption.
-->

- **ASM-1**: [assumed answer] - [reason] (confirm in review)

## Open questions *(blockers only, at most 3; empty before G2)*

- [NEEDS CLARIFICATION: question - Recommended: answer - reason]
