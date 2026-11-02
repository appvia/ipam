---
name: ac-test-mapping
description: Use when proving a PR meets its acceptance criteria. Maps each AC-n to named tests and CI checks, and reports the F1 to F4 final verification rows in the PR description.
---

# AC to test mapping

## The mapping table

Put this table in the PR description, one row per acceptance criterion in scope:

| AC | Test(s) | CI check | Status |
| --- | --- | --- | --- |
| AC-3 | `AC-3: rejects invites from non-admins` (test/tenants/invite.test.ts) | `test` | pass |

- Name tests after the AC (`it("AC-3: ...")`), so a search for `AC-3` finds them.
- The CI check is the status check name that runs the test, as GitHub shows it on the PR.
- An AC with no test is `unmapped`. Say so in the table; don't hide it.
- Use real CI results only. Never mark a row `pass` from a local run alone.

## Final verification (F1 to F4)

Tick each row only with evidence, and link that evidence:

- **F1 Plan compliance:** every todo done as written, nothing extra. List any file changed outside the plan's todos.
- **F2 Code quality:** lint, types and code review clean. Link the CI run.
- **F3 Real QA:** each todo's QA happy and QA failure scenario run. Link the test names or run logs.
- **F4 Spec fidelity:** each IS row proven, 1:1, by a row in the mapping table.

If a row can't be ticked, leave it unticked and say why in one line.

## Data rule

Never put real data, client identifiers or credentials in any issue, spec, plan, test fixture, commit or log. Use made-up names (`tenant-a`, `user@example.com`) and obviously fake secrets (`dummy-token`).
