# Difficulty rubric and executor mapping

Rate each todo on its own:

| Difficulty | When |
| --- | --- |
| low | One module, an existing pattern to copy, no contract change |
| medium | Several modules, or a new pattern the codebase doesn't have yet |
| high | A contract, schema or security-relevant change |

When two rows fit, pick the higher one.

## Executor

Each todo names the executor for its own difficulty. The build orchestrator hands each todo to the executor it names, so a plan can mix them:

- low: `implementer-low`
- medium or high: `implementer-high`

For example, in the same plan:

```
Executor: implementer-low · difficulty: low · skills: typescript-node
Executor: implementer-high · difficulty: medium · skills: typescript-node
```

The plan-level agent is `implementer-high` if any todo names it, otherwise `implementer-low`.

## Size

- 3 to 8 todos. More than 8 means the Story is too big: add `needs:split`.
- Effort is Quick, Short or Medium. Large or XL also adds `needs:split`.
- Implementation and its test belong in one todo, except the Bug rule in `bug-plans.md`.
