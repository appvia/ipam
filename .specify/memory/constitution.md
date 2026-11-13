# Appvia Delivery Constitution

> **DRAFT** - not yet ratified. The platform team owns this file. Changes land through a pull request to `appvia/agentic-delivery` and reach product repos through repo-sync.

**Version**: 0.3.1-draft | **Ratified**: pending | **Last amended**: 2026-11-13

This constitution sets the non-negotiables for every Requirement, spec, plan and Story delivered through the spec-driven pipeline. Agents and humans read it before writing a brief, spec, design or Story. A design that breaks a rule here must say so in its Constitution Check and get the gate approver's explicit sign-off.

## Core principles

Each principle bullet carries a stable id, `C-<roman>.<n>` (roman numeral of the heading, then the bullet's 1-based position), that Bugs and other records can cite. Ids are stable: never renumber, and new bullets take the next free number under their heading.

### I. Security baseline

- **C-I.1** Least privilege everywhere: identities, tokens, IAM roles and network paths get only what the change needs.
- **C-I.2** No secrets in source, issues, specs, logs or pull requests. Secrets live in a managed store (GitHub secrets or AWS Secrets Manager) and are rotated.
- **C-I.3** Every dependency is pinned to an exact version. GitHub Actions are pinned by full commit SHA with a version comment.
- **C-I.4** Inputs are validated at system boundaries. Untrusted input never reaches a shell, a query or a template unescaped.
- **C-I.5** Authentication and authorisation changes, new external integrations and anything touching personal data are `risk:high` and need a `security-managers` review at G3.
- **C-I.6** Known critical or high vulnerabilities block a merge unless a recorded exception exists.

### II. Test standards

- **C-II.1** Every acceptance criterion (`AC-n`) is proven by at least one named automated test, and the PR maps each AC to its test.
- **C-II.2** A Bug fix starts with a failing regression test for the violated AC, and the fix follows in a later commit. A Bug that cites a constitution rule (`C-n.m`) instead starts with an automated check (CI job, lint or test) that fails while that rule is broken, and the fix follows in a later commit.
- **C-II.3** Tests are deterministic: no fixed sleeps, no reliance on wall-clock timing, no network calls to real third-party services.
- **C-II.4** CI runs the full test suite on every pull request. A red build is never merged.
- **C-II.5** Tests may not be skipped, deleted or weakened to make a build pass.

### III. Technology choice

- **C-III.1** Any language, framework or platform may be used.
- **C-III.2** The product's stack is chosen in the design stage and recorded, with the reason, in an ADR under the spec (`specs/<REQ>/decisions/`). It is approved at G3 with the design.
- **C-III.3** Every language, SDK and tool the repo uses (compilers, runtimes, linters, formatters, generators, CLIs) is declared in `mise.toml` with an exact version and locked in a committed `mise.lock` for every CI platform. Tools come from mise backends that verify downloads; the `asdf` and `vfox` plugin backends are not used.
- **C-III.4** CI installs those tools only through mise from `mise.toml` and `mise.lock` in locked mode (for example `jdx/mise-action` with `MISE_LOCKED=1`), and pins no tool version anywhere else; a lint or setup action uses the binary mise installed. CI builds, tests and lints on every pull request. The pipeline's pr-verify and F4 checks read those CI results.
- **C-III.5** Language package dependencies keep their ecosystem lockfile (`go.sum`, `package-lock.json`, `.terraform.lock.hcl`). Anything mise cannot install is an exception recorded in the design's Constitution Check and approved at G3.
- **C-III.6** When an org skill exists for the stack (for example `typescript-node` or `terraform-module`), the design names it. When none exists, the design states the build, test and lint commands explicitly, and a new skill can be proposed to `appvia/agent-skills`.
- **C-III.7** Infrastructure as code is never applied by agents.

### IV. Accessibility

- **C-IV.1** User-facing interfaces meet **WCAG 2.2 level AA**.
- **C-IV.2** Accessibility is part of a Story's acceptance criteria, not a later pass. Automated checks run in CI where the stack supports them.

### V. Data handling

- **C-V.1** No real data, client identifiers or credentials in issues, specs, tests or fixtures; production data never leaves production unmasked.
- **C-V.2** Fixtures use synthetic data that can't be traced to a person or client.
- **C-V.3** Personal data is collected only when a spec states the purpose, retention period and lawful basis.

## Delivery rules

- Specs follow GitHub Spec Kit with the Appvia presets: brief, spec, plan and tasks. Nobody invents another format.
- Acceptance criteria are numbered `AC-n` and written as Given/When/Then. Non-functional requirements carry a number and a unit.
- One Story is one repo, one agent session and one pull request, at size M or smaller.
- Agents write only through safe outputs or draft pull requests. They never merge, approve or apply gate labels.

## Governance

- This constitution overrides local conventions when they conflict.
- An amendment is a pull request that states the change, the reason and the migration for in-flight work. The platform team and at least one product owner approve it.
- Versions follow semantic versioning: MAJOR removes or redefines a principle, MINOR adds one, PATCH clarifies wording.
