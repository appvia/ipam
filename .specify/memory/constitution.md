# Appvia Delivery Constitution

> **DRAFT** - not yet ratified. The platform team owns this file. Changes land through a pull request to `appvia/agentic-delivery` and reach product repos through repo-sync.

**Version**: 0.2.0-draft | **Ratified**: pending | **Last amended**: 2026-11-05

This constitution sets the non-negotiables for every Requirement, spec, plan and Story delivered through the spec-driven pipeline. Agents and humans read it before writing a brief, spec, design or Story plan. A plan that breaks a rule here must say so in its Constitution Check and get the gate approver's explicit sign-off.

## Core principles

### I. Security baseline

- Least privilege everywhere: identities, tokens, IAM roles and network paths get only what the change needs.
- No secrets in source, issues, specs, logs or pull requests. Secrets live in a managed store (GitHub secrets or AWS Secrets Manager) and are rotated.
- Every dependency is pinned to an exact version. GitHub Actions are pinned by full commit SHA with a version comment.
- Inputs are validated at system boundaries. Untrusted input never reaches a shell, a query or a template unescaped.
- Authentication and authorisation changes, new external integrations and anything touching personal data are `risk:high` and need a `security-managers` review at G3.
- Known critical or high vulnerabilities block a merge unless a recorded exception exists.

### II. Test standards

- Every acceptance criterion (`AC-n`) is proven by at least one named automated test, and the PR maps each AC to its test.
- A Bug fix starts with a failing regression test for the violated AC. The fix follows in a later commit.
- Tests are deterministic: no fixed sleeps, no reliance on wall-clock timing, no network calls to real third-party services.
- CI runs the full test suite on every pull request. A red build is never merged.
- Tests may not be skipped, deleted or weakened to make a build pass.

### III. Technology choice

- Any language, framework or platform may be used.
- The product's stack is chosen in the design stage and recorded, with the reason, in an ADR under the spec (`specs/<REQ>/decisions/`). It is approved at G3 with the design.
- Every stack must have, in the repo: an exact-pinned toolchain (for example `mise.toml`, `.nvmrc` or `.terraform-version`), a lockfile where the ecosystem has one, and CI that builds, tests and lints on every pull request. The pipeline's pr-verify and F4 checks read those CI results.
- When an org skill exists for the stack (for example `typescript-node` or `terraform-module`), plans name it. When none exists, the plan states the build, test and lint commands explicitly, and a new skill can be proposed to `appvia/agent-skills`.
- Infrastructure as code is never applied by agents.

### IV. Accessibility

- User-facing interfaces meet **WCAG 2.2 level AA**.
- Accessibility is part of a Story's acceptance criteria, not a later pass. Automated checks run in CI where the stack supports them.

### V. Data handling

- No real data, client identifiers or credentials in issues, specs, tests or fixtures; production data never leaves production unmasked.
- Fixtures use synthetic data that can't be traced to a person or client.
- Personal data is collected only when a spec states the purpose, retention period and lawful basis.

## Delivery rules

- Specs follow GitHub Spec Kit with the Appvia presets: brief, spec, plan and tasks. Nobody invents another format.
- Acceptance criteria are numbered `AC-n` and written as Given/When/Then. Non-functional requirements carry a number and a unit.
- One Story is one repo, one agent session and one pull request, at size M or smaller.
- Agents write only through safe outputs or draft pull requests. They never merge, approve or apply gate labels.

## Governance

- This constitution overrides local conventions when they conflict.
- An amendment is a pull request that states the change, the reason and the migration for in-flight work. The platform team and at least one product owner approve it.
- Versions follow semantic versioning: MAJOR removes or redefines a principle, MINOR adds one, PATCH clarifies wording.
