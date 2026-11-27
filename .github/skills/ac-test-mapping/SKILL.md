---
name: ac-test-mapping
description: Use when proving a PR meets its acceptance criteria. Maps each AC-n to its proof (a named test, a CI check or review) and its CI result, and reports the F1 to F4 final verification rows in the PR description.
---

# AC to proof mapping

## Choose the proof

Every acceptance criterion has one proof, of one of three kinds (constitution C-II.1):

- **test:** an AC about behaviour (what code does) needs a named automated test that a CI check ran. A behaviour AC can only have this proof.
- **check:** an AC about a document's structure (it exists, has the required sections, its paths and links resolve, it validates against a schema) is proven by a CI check that already runs or one you add, such as a linter, a path or link checker or a schema validator.
- **review:** an AC about what a spec, ADR, runbook or other prose says is proven by the verifier reading the document against the AC. Don't write a test for it.

A Story that only changes documentation can need no new tests at all. Say so under F3 rather than inventing one.

## Tests that prove nothing

Tests prove the product, not themselves (constitution C-II.6). Never write either of these:

- **A test that asserts on prose.** It searches a document for phrases, headings or wording, which proves nothing about whether the text is right and breaks on rewording. A test may read a document only when code or tooling consumes it (a schema, generated output, an executed example) or to check its structure, never to match its wording. Prove what prose says by review.
- **A test that exercises itself.** It proves an AC by running behaviour defined inside the test, such as a reference implementation of a contract the product does not implement yet, and asserting on that. A test exercises the repository's own code. Fakes and test doubles for the product's dependencies are fine.

pr-verify fails F2 when a diff adds either kind, and judges the AC by the proof it should have had, usually review.

## The mapping table

Put this table in the PR description, one row per acceptance criterion in scope:

| AC | Proof | Test, check or document | CI check | Status |
| --- | --- | --- | --- | --- |
| AC-3 | test | `AC-3: rejects invites from non-admins` (test/tenants/invite.test.ts) | `test` | pass |
| AC-4 | check | `markdownlint-cli2 "docs/**/*.md"` | `lint` | pass |
| AC-5 | review | `docs/runbook.md` | `qa-verifier` | pending review |

- Name tests after the AC (`it("AC-3: ...")`), so a search for `AC-3` finds them.
- For `test` and `check`, the CI check is the status check name that ran the test or check, as GitHub shows it on the PR.
- For `review`, name the document and use `qa-verifier` as the CI check. The verifier reads the document against the AC; it is met only if the text says what the AC asks, completely and without contradicting the spec or other documents.
- An AC with no proof is `unmapped`. Say so in the table; don't hide it.
- Use real CI results only. Never mark a row `pass` from a local run alone.

The pr-verify verdict line records the same mapping: each `mapped` entry carries `"proof":"test|check|review"`, and a missing `proof` means `test`.

## Final verification (F1 to F4)

Tick each row only with evidence, and link that evidence:

- **F1 Story compliance:** the diff delivers the Story's What to build within its Constraints, and nothing extra. List and justify every changed file outside the Story's Hints.
- **F2 Code quality:** lint, types and code review clean, and every new test can actually fail and exercises the repository's own code rather than prose or behaviour written inside the test. Link the CI run.
- **F3 Test evidence:** each Test expectation in the Story (for a Bug, the reproduction) has its test, with a happy path and a failure path, and CI evidence. Link the test names or run logs. A Test expectation that names only review or an existing check needs no new test.
- **F4 Spec fidelity:** every acceptance criterion maps to its proof in the mapping table, and the diff does not drift from the spec or the Story.

If a row can't be ticked, leave it unticked and say why in one line.

## Data rule

Never put real data, client identifiers or credentials in any issue, spec, plan, test fixture, commit or log. Use made-up names (`tenant-a`, `user@example.com`) and obviously fake secrets (`dummy-token`).
