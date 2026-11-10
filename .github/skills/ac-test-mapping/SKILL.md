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

- **F1 Story compliance:** the diff delivers the Story's What to build within its Constraints, and nothing extra. List and justify every changed file outside the Story's Hints.
- **F2 Code quality:** lint, types and code review clean. Link the CI run.
- **F3 Test evidence:** each Test expectation in the Story (for a Bug, the reproduction) has its test, with a happy path and a failure path, and CI evidence. Link the test names or run logs.
- **F4 Spec fidelity:** every acceptance criterion maps to a passing test in the mapping table, and the diff does not drift from the spec or the Story.

If a row can't be ticked, leave it unticked and say why in one line.

## Data rule

Never put real data, client identifiers or credentials in any issue, spec, plan, test fixture, commit or log. Use made-up names (`tenant-a`, `user@example.com`) and obviously fake secrets (`dummy-token`).
