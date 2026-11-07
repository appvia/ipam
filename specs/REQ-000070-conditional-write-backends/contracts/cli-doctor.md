# Contract: CLI consent and doctor reporting

**Source**: [spec.md](../spec.md), AC-6, AC-9..AC-16, AC-21, AC-24, AC-26..AC-28.
**Existing surfaces**: `cmd/ipam/main.go`, `commands.go`, `doctor.go`, `output.go`.

## Consent resolution

Add persistent boolean flag `--allow-unverified-backend`, default false, and
document `IPAM_ALLOW_UNVERIFIED_BACKEND`. Resolve it when a command needs a
backend, using the injected environment getter.

| Explicit flag | Environment | Effective consent |
| --- | --- | --- |
| true | Any, including false or malformed | true |
| false | Any, including true or malformed | false |
| Not supplied | Absent/empty or false | false |
| Not supplied | true | true |
| Not supplied | Invalid boolean | Redacted usage error, exit 2; no operation |

Use `strconv.ParseBool` for nonempty environment values; accepted spellings
follow its standard boolean grammar. Never include a malformed value in the
usage error. Cobra's flag-supplied state, not the boolean's value, decides
precedence. Backend selection stays `--backend` over `IPAM_BACKEND`. Neither
help nor version needs classification, credentials, environment consent parsing
or an opener. Invalid backend URLs keep existing usage errors with either
consent value. (AC-6, AC-9..AC-11, AC-23, AC-26.)

Ordinary unverified backend-using commands without consent return the dedicated
library refusal category and exit 1 via the existing generic error mapping.
They perform no requested store operation. Existing exit codes 0..6 and mapping
order do not change. Consent options must reach both injected opener seams,
and the CLI preparation helper must also reject before a fake opener can grant
ordinary access. (AC-6, AC-11, AC-27.)

## One support-risk warning

Before an allowed unverified invocation performs its first operation, emit
exactly one newline-terminated support-risk warning on stderr, following the
existing `ipam: warning:` convention. Track only this warning with a boolean
on the invocation's `cli`, not package/global state or per backend instance.
If writing the warning fails, stop with the writer error; do not silently run.

The static line names the flag and environment opt-in, identifies conditional
creation/replacement/deletion and their headers, explains that enforcement
cannot be checked by opening and ignoring it risks concurrent corruption, and
links to `https://github.com/appvia/ipam#supported-backends`.
It contains no endpoint, bucket, prefix or credentials. Library code remains
silent. Structured stdout still contains only its existing requested output;
unrelated warnings or genuine operation errors are not suppressed. Verified
selections and unverified refusals emit zero support opt-in warnings.
(AC-9, AC-10, AC-12, AC-21, AC-24; NFR-3/NFR-4.)

## Doctor classification result

Add a first row named `backend-classification`, without pool or statistics.
Reuse the current `checkResult` JSON shape, table columns and status constants.
Classification is explicitly included as a static word in `detail`, not
deduced by parsing an endpoint. No new top-level JSON field is required.

| Classification | Operator consent | Row status | Static detail meaning | Probe enabled |
| --- | --- | --- | --- | --- |
| verified | Either | PASS | Verified support policy, not a live enforcement/connectivity/permission result | Yes, existing behavior |
| unverified | true | WARN | Unverified; explicit consent permits use but does not prove enforcement | Yes, existing behavior |
| unverified | false | FAIL | Unverified; no explicit consent; ordinary use refused and write probes skipped | No |

The unverified detail names opt-ins and the common documentation URL without
echoing configuration. Allowed unverified doctor also receives the one stderr
support warning; a refused doctor does not pretend the operator opted in.
(AC-13..AC-15, AC-24.)

For example, a fictional unverified store with no operator consent and no
discoverable pools has this report shape; real independent read-only results
also appear when available:

```json
{
  "result": "fail",
  "checks": [
    {
      "check": "backend-classification",
      "status": "FAIL",
      "detail": "unverified; no explicit consent; ordinary use refused. Use --allow-unverified-backend or IPAM_ALLOW_UNVERIFIED_BACKEND=true after evaluating conditional-write safety. See https://github.com/appvia/ipam#supported-backends"
    },
    {
      "check": "write-probe",
      "status": "WARN",
      "detail": "write probes skipped: backend is unverified and no opt-in was supplied"
    }
  ]
}
```

This is an illustrative subset of rows, not a fabricated successful backend
check. Table mode represents the same check/status/detail values with the
existing `CHECK POOL STATUS DETAIL` columns and `RESULT: fail` footer.

## Diagnostic-only construction and skips

Doctor keeps operator consent immutable while deliberately constructing storage
with a derived internal consent context for the diagnostic exception. Discard
that derived context after construction; use the original context for checks,
and never pass the derived context to an ordinary command. Validate first;
invalid input still fails with the existing invalid-argument/usage category.
For unverified no-consent selections, constructor failures after classification
are redacted backend FAIL rows accompanying the policy FAIL; render available
results and return exit 6. Do not turn a constructor error into a usable store.
Verified construction failures retain existing behavior. (AC-15, AC-16, AC-23,
AC-24, AC-27.)

Pass `probeEnabled` separately to diagnostic orchestration. When false, never
invoke `probeWrite`, `CreateIfAbsent`, `PutIfMatch` or `DeleteIfMatch`, even for
`--expect-write` or `--expect-no-write` pools. The existing union of discovered
and expectation-named pools remains sorted. Emit a `write-probe` WARN skip row
for each pool with the static reason above; if none can be enumerated, emit one
global skip row. Existing pool identifiers may appear in the existing pool
column, never as private backend configuration. A skip does not establish any
permission expectation and must not be PASS. (AC-28; NFR-7.)

Available `checkBackend`, `checkProtection` and `checkFragmentation` checks run
under their existing rules. Classification FAIL alone does not trigger the
backend reachability short-circuit. Actual unreadability can still prevent
dependent checks, with explicit existing errors; it is not hidden by success
defaults. When probes are enabled their existing request/version/error behavior
is untouched. (AC-13, AC-16, AC-27, AC-28.)

## Aggregation and safety language

Keep `doctorReport.finish`: any FAIL -> result `fail`, exit 6; otherwise any
WARN -> result `warn`, exit 0; otherwise `pass`, exit 0. Classification is not
an attestation of connection, permission, TLS, CAS enforcement or a successful
probe. A no-consent skip cannot replace the classification FAIL. (AC-13..AC-16,
AC-27, AC-28.)

Changed help and README must acknowledge that the existing write probe can
change data on a non-enforcing backend and cannot prove conditional deletion.
Do not add scratch-object probes, conditional-delete probes, cleanup or a
public ordinary read-only mode. Textual statuses and skip reasons work without
colour and remain readable in both formats. (AC-20; NFR-6; scope constraints.)
