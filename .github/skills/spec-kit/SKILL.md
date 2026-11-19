---
name: spec-kit
description: Use when writing or revising a feature spec, clarification log, technical plan, analysis or task list with GitHub Spec Kit v1.3.2 and the Appvia presets, under specs/<id>-<slug>/ in a spec repo.
---

# Spec Kit (v1.3.2)

Spec Kit turns a requirement into spec, plan and task files, one step at a time. Each step has its upstream instructions in `references/vendor/speckit-<step>.md` (see `SOURCE.md`). Read the matching file before you run a step, then apply "Pipeline runs" below on top of it.

| Step id | Pipeline stage | Output |
| --- | --- | --- |
| `speckit-clarify` | intake and brief (`requirement-intake`, `clarify-loop`), question format only, not its 5-question loop | answers in `clarifications.md`, guesses in `brief.md` |
| `speckit-specify` | spec (`spec-draft`) | `spec.md` |
| `speckit-plan` | design (`design-draft`) | `plan.md`, `research.md`, `data-model.md`, `contracts/` |
| `speckit-analyze` | design, after `plan.md`, detection passes A-D and F only | findings become `ASM-n` or `(proposed)` spec deltas |
| `speckit-tasks` | slice (`story-slicing`) | `tasks.md`, ADR-0004 tasks contract and the stage prompt win over the vendored format |

## Pipeline runs (takes precedence over references/vendor)

- Never run `{SCRIPT}`, `.specify/scripts/*` or hooks.
- The feature directory is `target/specs/REQ-NNNNNN-<slug>/`, resolved by the workflow.
- Read templates from `target/.specify/presets/appvia/templates/<name>.md`. The Appvia preset replaces the core template, so that file is the whole template.
- Never write `.specify/feature.json`, `checklists/` or anything outside `specs/**`.
- Never wait for chat answers: the stage prompt's safe outputs replace them.
- For `speckit-analyze`, skip the prerequisite script, pass E (task coverage) and the remediation question.
- Wherever vendored text says ERROR, abort, stop or wait for the user, apply the Informed guesses or Blocker rule below instead.
- Vendored clarify criteria only identify topics to mention. They never override the Appvia classification below as an assumption or blocker.
- Slicing is unchanged: in `story-slicing`, the Informed-guess, Blocker and Disagreement bullets do not apply.

## Install (humans and CI only)

```
uvx --from git+https://github.com/github/spec-kit.git@v1.3.2 specify init --here --integration copilot --script sh
```

Pin the tag. Never install from `main`.

## Appvia presets

- One folder per requirement: `specs/<REQ-id>-<slug>/`, where `<REQ-id>` is `REQ-` and the Requirement issue number zero-padded to 6 digits (issue 7 is `REQ-000007`).
- Every AC is testable and numbered `AC-1`, `AC-2`, ... Numbers never change once G2 approves the spec; add new ones at the end.
- NFRs carry a number and a unit (`p95 < 300 ms`), never "fast" or "secure".
- Scope: these rules apply to intake, brief, spec and design.
- Informed guesses: when the inputs leave a detail open, choose the answer you would recommend, apply it, and record it as `ASM-n` (numbered from 1 per file, never renumbered) under `## Assumptions` with a one-line reason. List every `ASM-n` under `## Decisions to confirm` in the PR body. Reviewers confirm by merging the PR and correct with Request changes.
- Assumption rule: use an assumption for an implementation choice or ordinary product detail inside the stated business outcome when the Requirement, existing product conventions or constitution provide a safe default. Examples are selecting an existing interface, choosing output shape, rounding or order, and setting bounded retention for non-personal operational data. Do not use an assumption to add or remove a business outcome, user capability or out-of-scope item.
- Blocker rule: ask instead of assuming only when no safe, defaultable answer exists; alternatives materially change the stated outcome, a user capability or the out-of-scope set; or the choice creates or changes personal-data, credential, trust/access-boundary or regulated-data exposure. Examples are true scope or policy choices and exporting personal data. A recommendation alone does not make an item a blocker. At most 3 open blockers per artifact. The artifact is still written using the recommendation, and each genuine blocker gets `needs:clarification`. Merge does not settle a blocker: the author answers on the issue while the PR is open, or a product owner or tech lead removes `needs:clarification` to accept the recommendation.
- Intake question format: use the plain heading `Blocking questions` followed by items `B1` through `B3`, and the plain heading `Will assume unless corrected` followed by items `A1` through `An`. Use `No blocking questions` when there are no blockers. For every blocker item, include the question, one "Why it matters" sentence, and `**Recommended:** <answer> - <reason>`.
- Never invent scope: an assumption fills a detail inside the stated business outcome; a choice that changes that outcome, a user capability or the out-of-scope set is a blocker.
- Disagreement with an earlier artifact is not a stop: apply your recommended reading. In spec, log it as an `ASM-n` naming the brief line. In design, edit `spec.md` minimally and list each edit under `## Spec delta` in the PR body with the token `(proposed)`, before and after text, and the reason. Never renumber AC or FR ids; new ids go at the end.
- Clarifications: `clarifications.md` records answers with who answered (role) and the answer URL (issue comment, or PR review comment for brief and spec). Design reviewer decisions stay in `## Spec delta`.
- `plan.md` cites the ADRs and constitution articles it relies on.
- In `tasks.md`, every Story and Enabler block sets `executor` (never a Feature block), one of `implementer-low` or `implementer-high`. Choose `implementer-low` only for XS or S work confined to one module, with no new public contract and no concurrency or security logic; otherwise `implementer-high`.
- A spec PR only touches `specs/**`. A human approves it (gate G2); agents never merge it.

## Data rule

Never put real data, client identifiers or credentials in any issue, spec, plan, test fixture, commit or log. Use made-up names (`tenant-a`, `user@example.com`) and obviously fake secrets (`dummy-token`).
