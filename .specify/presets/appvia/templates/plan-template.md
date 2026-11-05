# Technical Design: [FEATURE NAME]

**Spec**: `specs/[REQ-NNNNNN-slug]/spec.md`
**Created**: [DATE]
**Status**: Draft
**Proposed risk**: [low | medium | high]

<!--
  Stage G3. The solution architect writes this file with data-model.md, contracts/,
  decisions/ADR-*.md and, for risk:high, threat-model.md. A tech lead approves it.
  This is the Spec Kit plan for the whole Requirement, not a Story plan.
-->

## Summary

[The approach in one paragraph, and which acceptance criteria it satisfies.]

## Technical context

**Languages and versions**: [exact pinned versions, e.g. TypeScript on Node.js 24.x, Terraform 1.x]
**Stack decision**: `decisions/ADR-[NNNN]-stack.md` [the stack, the reason, and the org skill or explicit build, test and lint commands]
**Repositories touched**: [owner/repo list; each Story later lands in exactly one]
**Storage**: [data stores and their regions]
**Testing**: [unit, integration and end-to-end tooling per repo]
**Constraints**: [NFR ids this design must meet]

## Constitution check

<!-- Gate: must pass before G3. Name each principle and how this design meets it. -->

| Principle | How the design complies | Exception (needs approver sign-off) |
| --- | --- | --- |
| I. Security baseline | | |
| II. Test standards | | |
| III. Technology choice | | |
| IV. Accessibility | | |
| V. Data handling | | |

## Components

| Component | Repo | Responsibility | Acceptance criteria |
| --- | --- | --- | --- |
| [name] | [owner/repo] | [what it does] | AC-1, AC-2 |

## Contracts

- `contracts/[name].yaml`: [API or event schema and its consumers]

## Data model

See `data-model.md`.

## Decisions

- `decisions/ADR-0001-[title].md`: [decision in one line]

## Rollout

[Feature flags, migration order, backwards compatibility, and how to roll back.]

## Risks

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| [risk] | [low/medium/high] | [low/medium/high] | [mitigation] |
