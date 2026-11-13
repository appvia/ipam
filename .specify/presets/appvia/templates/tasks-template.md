# Tasks: [FEATURE NAME]

**Spec**: `specs/[REQ-NNNNNN-slug]/spec.md`
**Design**: `specs/[REQ-NNNNNN-slug]/plan.md`
**Requirement risk**: [low | medium | high]

<!--
  Tasks contract (ADR-0004). The story-slicer writes this file in the slice PR.
  After the PR merges, materialise-stories reads ONLY the fenced yaml blocks below
  and creates one issue per block: Features in the spec repo, Stories and Enablers
  in the code repo named by `repo`. Prose outside the blocks is ignored.

  Rules (scripts/sync/tasks-contract.ts enforces them):
  - One ```yaml block per Feature, Story or Enabler, with exactly these keys:
    kind, title, parent, repo, area, size, autonomy, risk, spec_path,
    what_to_build, acceptance, constraints, test_expectations, hints, depends_on
  - executor (optional key): implementer-low | implementer-high. Set it on every
    Story and Enabler, never on a Feature. implementer-low only for XS or S work
    confined to one module, with no new public contract and no concurrency or
    security logic; otherwise implementer-high.
  - kind: feature | story | enabler. Titles are unique in the file.
  - parent: a Feature's parent is the Requirement id (REQ-NNNNNN); a Story's or
    Enabler's parent is the title of a Feature block in this file.
  - repo: owner/name. One repo per Story; a change to two repos is two Stories.
  - size: XS | S | M | L | XL; Stories and Enablers are M or smaller.
  - risk: low | medium | high, never lower than the Requirement's risk.
  - autonomy: Agent-led | Paired | Human-only; risk high is never Agent-led.
  - acceptance: AC-n criteria from spec.md; required for Stories and Enablers.
  - depends_on: titles of other Stories or Enablers in this file.
  - At most 25 blocks per slice.
  Each Story and Enabler key feeds one section of the Story issue:
  spec_path and acceptance ids -> Spec, what_to_build -> What to build,
  acceptance -> Acceptance criteria, constraints -> Constraints,
  test_expectations -> Test expectations, hints -> Hints, depends_on -> GitHub Blocked by links (not a body section).
  Replace the example blocks below; keep the shape.
-->

## Feature: Tenant admins manage their own users

```yaml
kind: feature
title: Tenant admins manage their own users
parent: REQ-000042
repo: appvia/example-specs
area: tenants
size: L
autonomy: Agent-led
risk: medium
spec_path: specs/REQ-000042-tenant-self-service/spec.md
what_to_build: Tenant admins can invite, list and remove users in their own tenant without a support ticket.
acceptance:
  - AC-1
  - AC-2
  - AC-3
constraints: []
test_expectations: []
hints: []
depends_on: []
```

### Enabler: Add a tenant role to the user model

```yaml
kind: enabler
title: Add a tenant role to the user model
parent: Tenant admins manage their own users
repo: appvia/example-api
area: tenants
executor: implementer-low
size: S
autonomy: Agent-led
risk: medium
spec_path: specs/REQ-000042-tenant-self-service/spec.md
what_to_build: Users carry a tenant role of admin or member, defaulting to member for existing users.
acceptance:
  - "AC-3: Given an existing user, when the migration runs, then their role is member"
constraints:
  - Migration is backwards compatible; no downtime
test_expectations:
  - Integration test runs the migration on a seeded synthetic database
hints:
  - src/users/model.ts
  - decisions/ADR-0003-tenant-roles.md
depends_on: []
```

### Story: Only tenant admins can invite users

```yaml
kind: story
title: Only tenant admins can invite users
parent: Tenant admins manage their own users
repo: appvia/example-api
area: tenants
executor: implementer-high
size: M
autonomy: Agent-led
risk: medium
spec_path: specs/REQ-000042-tenant-self-service/spec.md
what_to_build: A tenant admin can invite a user by email; a member who tries gets a clear refusal and nothing is stored.
acceptance:
  - "AC-1: Given a tenant admin, when they invite a user, then the user is listed as invited"
  - "AC-2: Given a tenant member, when they invite a user, then the request is refused with 403"
constraints:
  - Honour contracts/tenant.yaml; no response shape changes
  - "NFR-1: p95 invite latency at most 300 ms"
test_expectations:
  - Unit tests for the role check
  - API test per AC
hints:
  - src/tenants/invite.ts
  - src/auth/roles.ts
depends_on:
  - Add a tenant role to the user model
```
