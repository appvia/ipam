---
name: story-plan-authoring
description: Use when writing, revising or reviewing the Implementation plan of a Story or Bug issue. This is the single source of the Story plan template, the todo grammar that plan-lint enforces, and the difficulty and executor rules.
---

# Story plan authoring

A Story says what to build. Its plan says how, step by step, so the implementer follows a checklist. The plan lives in the issue under `## Implementation plan`, between `<!-- plan:start -->` and `<!-- plan:end -->`.

## Files

- `references/story-plan-template.md`: the template. Copy it; don't reorder headings.
- `references/difficulty-rubric.md`: how to rate each todo and pick the Executor.
- `references/bug-plans.md`: extra rules when the issue type is Bug.

## Rules plan-lint checks

- Headings (`##` or `###`), in this order: TL;DR, Scope, References, Verification strategy, Todos, Final verification, Success criteria.
- Todos are `- [ ] N. title` at column zero, numbered 1, 2, 3 with no gaps, 3 to 8 of them.
- Each todo has these fields: `Closes:`, `What to do:`, `Must NOT do:`, `References:`, `Acceptance:`, `QA happy:`, `QA failure:`, `Commit:`, and
  `Executor: implementer-(low|high) · difficulty: (low|medium|high) · skills: <list>`.
- Every todo names the same plan-level agent (see the rubric). Difficulties may differ.
- Every skill named must exist in the org skills index. The TL;DR `Skills:` line is the union of the todo skills.
- `F1.` to `F4.` are all present under Final verification.
- Scope has IS and GAP rows, and every GAP is closed by some todo.
- TL;DR has `Effort: Quick`, `Short` or `Medium`. Large or XL means the Story needs splitting: add `needs:split` and stop.

## How to plan

Explore the code before asking anything. Cite a real path for every pattern you tell the implementer to copy. Make every decision in the plan, so the implementer has nothing left to ask. Write the TL;DR last, so it describes the plan you wrote and not the one you meant to write. Before handing off, check the structure against the rules above.

## Data rule

Never put real data, client identifiers or credentials in any issue, spec, plan, test fixture, commit or log. Use made-up names (`tenant-a`, `user@example.com`) and obviously fake secrets (`dummy-token`).
