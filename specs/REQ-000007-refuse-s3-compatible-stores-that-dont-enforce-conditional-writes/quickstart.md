# Validation guide: S3 opening verification

This is the Spec Kit Phase 1 validation guide for a future implementation, not
evidence that the proposed APIs or tests exist today. The design-only redraft
does not execute product writes or acceptance tests. Implementation remains
blocked on the AC-4 decision in [research.md](research.md) and G3 review.

## Prerequisites and setup

- Obtain owner resolution of the AC-4/NFR-2 integrity assumption and required
  security review before implementing. Confirm deployment residency or record
  an approved constitutional exception; no region enforcement is added here.
- Work from the `appvia/ipam` repository root with Go 1.27.3 and golangci-lint
  2.14.0, matching `mise.toml` and `go.mod`. Reuse `go.sum`; add no dependencies.
- Use only synthetic fixtures, local fake HTTP servers and existing test
  harnesses. No live credentials, cloud bucket, provider repository or container
  runtime is needed for this acceptance matrix. Do not start emulator scripts.
- Implement the plan's named tests with stateful request recording, injected
  retry/entropy seams and channel barriers, not fixed sleeps or live services.
  See [plan.md](plan.md#verification-plan) for the complete AC-to-test mapping.

## Confirm the tests actually exist

After implementation, list the matching tests before running the targeted suite:

```bash
go test ./storage/s3 ./cmd/ipam . -list 'Test(Open|ConcurrentOpens|Construction|CLIRefuses|AutomationOpen|OpeningDiagnostics|Probe)'
```

Compare the output with **every named test** in the plan's AC-1 through AC-14
rows and its additional protocol regressions. All names must appear, in the
appropriate package; no missing row is accepted. A zero-test success or a
`[no tests to run]` result proves nothing. If the names are absent, stop and
report missing implementation evidence rather than marking an AC passed.

## Run the focused acceptance matrix

```bash
go test ./storage/s3 ./cmd/ipam . -run 'Test(Open|ConcurrentOpens|Construction|CLIRefuses|AutomationOpen|OpeningDiagnostics|Probe)' -count=1 -race -v
```

Expected outcomes are semantic assertions, not merely a successful command:

| Scenario | Required evidence | Trace |
| --- | --- | --- |
| Conforming default/custom endpoint paths | Verified open; unchanged valid operation results; exactly one logical check per explicit open and zero for subsequent operations. Default routing uses an injected local transport, not AWS. | AC-1, AC-6, AC-11 |
| Create-only condition ignored | Nil handle; unsupported-creation error; zero application writes. | AC-2, AC-10 |
| Version-match condition ignored independently | Nil handle; unsupported-replacement error; zero application writes. | AC-3, AC-10 |
| Existing objects, forced name collision and overlapping opens | Snapshot bytes and versions unchanged; collision candidate never mutated/deleted; barrier-overlapped conforming opens succeed independently. This does not prove the unresolved arbitrary-writer case. | AC-4, AC-5; NFR-2 |
| Access denial, communication error, 409, malformed response or unknown write outcome | Inconclusive error and nil handle, never false proof of support/non-support. Positive controls reject reject-all behavior. | AC-7 |
| Cancel/deadline at protocol boundaries | Nil handle; retained context sentinel identity; zero application writes; cleanup obeys its separate five-second bound. | AC-8 |
| Diagnostic reflection | Synthetic credential markers absent from error formatting/chains, warnings, logs, CLI stderr and stdout. | AC-9; NFR-3, NFR-4 |
| CLI and automation requested mutation | Both independently ignored conditions refuse at opening before pool/allocation writes; direct construction cannot bypass readiness. | AC-10 |
| Cleanup failure after successful verification | Usable handle, nil error and one warning; otherwise-successful CLI remains exit 0 with unchanged machine-readable stdout. | AC-12 |
| Verification and cleanup both fail | Nil handle; original verification classification retained; one separate warning. | AC-13 |
| Constructor and explicit open | Constructor and unopened operations make zero requests; explicit open verifies both conditions before publishing a handle. | AC-14 |

The wire sequence and result classes are defined in
[conditional-write-check.md](contracts/conditional-write-check.md) and
[opening-api.md](contracts/opening-api.md). The scratch payload and state
invariants are in [data-model.md](data-model.md); do not duplicate or weaken
them in the test harness. Count logical checks separately from SDK retries.
Bounded-body and cleanup-ownership regressions must also appear and pass.

## Preserve existing behavior

After the focused tests pass, run the existing repository commands:

```bash
make build
make test
make lint
```

Retain existing storage contract, SDK uncertain-outcome, URL validation,
CLI exit-code and lifecycle assertions. Adapt constructor setup where explicit
opening is now required; do not remove assertions to make the new boundary pass.
Check existing cross-platform CI as required by Constitution II/III. Existing
emulator integration jobs remain separate; this guide does not run Docker,
request live access or certify a vendor/version.

## Record implementation evidence

Map each AC to its executed named test and observed assertion in the future
implementation PR. Keep proposed tests distinct from actual passing results.
Document scratch-permission and direct-construction migration impacts, warning
delivery and the point-in-time scope. Do not report the AC-4 absolute guarantee
as proven by random-key tests, nor claim conditional DELETE certification,
physical erasure of retained versions or cleanup after process interruption.
