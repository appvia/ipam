# Writing specs

Specs follow GitHub Spec Kit with the Appvia presets in `.specify/presets/appvia/`. Don't invent another format.

- Each Requirement has one folder, `specs/REQ-NNNNNN-<slug>/`, holding `brief.md`, `clarifications.md`, `spec.md`, `plan.md`, `tasks.md` and, where needed, `research.md`, `data-model.md`, `contracts/`, `decisions/ADR-NNNN-*.md` and `threat-model.md`.
- `brief.md` stays in business language: problem, outcome metric, users, constraints, out of scope.
- `spec.md` numbers every acceptance criterion `AC-1`, `AC-2` and so on, in Given/When/Then form. Never renumber after G2, because Stories, plans, tests and Bugs cite these ids.
- Every non-functional requirement has a number and a unit.
- `spec.md` always has "Out of scope" and "Open questions" sections. Open questions must be empty before G2.
- Record every clarification in `clarifications.md` with who answered it.
- `tasks.md` follows the tasks contract (ADR-0004 in `appvia/agentic-delivery`): one fenced yaml block per Feature, Story or Enabler. Issues are created from it only after the slice PR merges.
- No real data, client identifiers or credentials anywhere under `specs/`.
