## Implementation plan

<!-- plan:start -->

### TL;DR
What you get · Approach · What it will NOT do
Effort: Quick | Short | Medium · Risk: low | medium | high
Skills: typescript-node, ac-test-mapping

### Scope
| IS | From the spec | GAP today |
| --- | --- | --- |
| IS-1 | AC-3: only admins can invite | GAP-1: no role check in src/tenants/invite.ts |

Must have: ...
Must NOT have: no new dependencies; no change to contracts/tenant.yaml

### References
- Pattern to copy: src/users/create.ts (validation and error shape)
- Contract: specs/REQ-0042-tenant-self-service/contracts/tenant.yaml
- Decision: ADR-0003
- Tests to mirror: test/users/create.test.ts

### Verification strategy
Commands to run, and the test type that proves each IS row.

### Todos
- [ ] 1. Add a failing test for admin-only invites
  Closes: GAP-1 (IS-1)
  What to do: add `AC-3: rejects invites from non-admins` to test/tenants/invite.test.ts
  Must NOT do: change src/ in this todo
  References: test/users/create.test.ts
  Acceptance: `npm test -- invite` fails on the new test only
  QA happy: the new test exists and fails for the right reason. Evidence: test output
  QA failure: no other test changes state. Evidence: test summary
  Commit: test(tenants): cover admin-only invites (#123)
  Executor: implementer-high · difficulty: low · skills: typescript-node, ac-test-mapping
- [ ] 2. Restrict invitations to tenant admins
  Closes: GAP-1 (IS-1)
  What to do: check the caller's role in src/tenants/invite.ts using src/auth/roles.ts
  Must NOT do: change the invite response shape
  References: src/tenants/invite.ts, src/auth/roles.ts
  Acceptance: `npm test -- invite` passes
  QA happy: admin invites a user; 201; user is listed. Evidence: test name
  QA failure: member invites a user; 403; nothing stored. Evidence: test name
  Commit: feat(tenants): restrict invites to admins (#123)
  Executor: implementer-high · difficulty: medium · skills: typescript-node
- [ ] 3. Map AC-3 to its test and CI check
  Closes: GAP-1 (IS-1)
  What to do: add the AC-3 row to the PR's AC mapping table
  Must NOT do: edit the spec
  References: ac-test-mapping skill
  Acceptance: every AC in scope has a test name and a CI check
  QA happy: table lists AC-3 with its test. Evidence: PR body
  QA failure: an AC with no test is flagged as unmapped. Evidence: PR body
  Commit: docs(tenants): map AC-3 to tests (#123)
  Executor: implementer-high · difficulty: low · skills: ac-test-mapping

### Final verification
- [ ] F1. Plan compliance: every todo done as written, nothing extra
- [ ] F2. Code quality: lint, types and CI are clean
- [ ] F3. Real QA: run every QA scenario and record the evidence
- [ ] F4. Spec fidelity: each IS row proven, 1:1

### Success criteria
| IS | Todo | Test | Evidence |
| --- | --- | --- | --- |
| IS-1 | 1, 2 | AC-3: rejects invites from non-admins | CI run link |

<!-- plan:end -->
