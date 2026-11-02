# Bug plans

## Definition of ready

A Bug issue is ready to plan when it has:

- steps to reproduce on a named version or commit
- expected and actual behaviour
- the acceptance criterion (AC-n) or spec section the behaviour breaks
- a severity

If any is missing, add `needs:clarification` and ask for it. Don't guess.

## Regression test first

Todo 1 of a Bug plan adds a failing regression test, and nothing else. Its `Acceptance:` names the test and the AC it proves, for example:

```
Acceptance: `AC-3: rejects invites from non-admins` in test/tenants/invite.test.ts fails on main
```

The fix comes in todo 2 or later, and its acceptance is that same test passing. plan-lint fails a Bug plan whose first todo is the fix.

Bug reports often hold copied logs. Strip any real data, client identifiers or credentials before quoting them.
