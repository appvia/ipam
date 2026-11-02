# Difficulty rubric and executor mapping

Rate each todo on its own:

| Difficulty | When |
| --- | --- |
| low | One module, an existing pattern to copy, no contract change |
| medium | Several modules, or a new pattern the codebase doesn't have yet |
| high | A contract, schema or security-relevant change |

When two rows fit, pick the higher one.

## Executor

The plan has one agent, and it's derived from the highest todo difficulty:

- any todo is medium or high: `implementer-high`
- every todo is low: `implementer-low`

Every todo's `Executor:` line names that plan-level agent, even a low todo in a high plan. For example:

```
Executor: implementer-high · difficulty: low · skills: typescript-node
```

## Size

- 3 to 8 todos. More than 8 means the Story is too big: add `needs:split`.
- Effort is Quick, Short or Medium. Large or XL also adds `needs:split`.
- Implementation and its test belong in one todo, except the Bug rule in `bug-plans.md`.
