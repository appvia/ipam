---
name: spec-kit
description: Use when writing or revising a feature spec, clarification log, technical plan or task list with GitHub Spec Kit v1.2.5 and the Appvia presets, under specs/<id>-<slug>/ in a spec repo.
---

# Spec Kit (v1.2.5)

Spec Kit turns a requirement into four files, one step at a time. Each step has its upstream instructions in `references/vendor/` (see `SOURCE.md`). Read the matching file before you run a step.

| Step | Output | Upstream instructions |
| --- | --- | --- |
| specify | `spec.md`: user stories, numbered acceptance criteria (AC-n), NFRs with numbers | `references/vendor/speckit-specify.md` |
| clarify | `## Clarifications` in `spec.md`; open questions become `needs:clarification` | `references/vendor/speckit-clarify.md` |
| plan | `plan.md`, `research.md`, `data-model.md`, `contracts/` | `references/vendor/speckit-plan.md` |
| tasks | `tasks.md`: ordered tasks, each tracing to an AC | `references/vendor/speckit-tasks.md` |

## Install (humans and CI only)

```
uvx --from git+https://github.com/github/spec-kit.git@v1.2.5 specify init --here --integration copilot --script sh
```

Pin the tag. Never install from `main`.

## Appvia presets

- One folder per requirement: `specs/<REQ-id>-<slug>/`.
- Every AC is testable and numbered `AC-1`, `AC-2`, ... Numbers never change once G2 approves the spec; add new ones at the end.
- NFRs carry a number and a unit (`p95 < 300 ms`), never "fast" or "secure".
- Don't invent requirements. Anything you can't source from the issue or the constitution goes in Clarifications as a question.
- `plan.md` cites the ADRs and constitution articles it relies on.
- A spec PR only touches `specs/**`. A human approves it (gate G2); agents never merge it.

## Data rule

Never put real data, client identifiers or credentials in any issue, spec, plan, test fixture, commit or log. Use made-up names (`tenant-a`, `user@example.com`) and obviously fake secrets (`dummy-token`).
