# Implementation validation guide

These commands are for future implementation, not evidence that product changes
have been made. Prerequisites: G3 approval including DQ-1/DQ-2, the repository's
pinned Go 1.27.3 and golangci-lint 2.14.0, and synthetic test fixtures. No AWS
credentials or live endpoint are needed for deterministic tests.

From the `appvia/ipam` root:

```sh
go test ./storage/s3 ./cmd/ipam . -race -count=1 -timeout 20m
make build
make test
make lint
```

Add the named tests from `plan.md` before claiming the feature complete. Capture
fake transport request paths/methods and user-object snapshots, not only returned
errors. For unsafe-store cases, every public mutation must produce **zero**
user-data PUT/DELETE requests. For explicit no-write doctor, every request must
be a read/list, including when a previous cleanup record exists. Run the latter
through the actual root opener path rather than only an injected storage stub.

Run the two-opening and cleanup cases with channel barriers and the race
detector. Assert separate keys, reports and proof counts; no fixed sleeps.
Inject transport errors after send as well as before send, exact 412 vs 409,
malformed responses and missing ETags. None may silently become proof.

Inspect both table and JSON output against `contracts/diagnostics.md`. Test
complete-empty, live residue, retained history/markers, denied version listing,
partial pagination, cleanup denial and explicit no-write. Confirm exit 6 on
demonstrated enforcement failure and exit 0 with explicit WARN on inconclusive
read-only diagnosis; ordinary gated writes exit 1. Existing non-S3 tests and
unknown-outcome tests must remain intact.

Existing emulator integration is `make test-integration` in an environment
provisioned by the repository's integration CI. Emulators do not prove
conditional DELETE; preserve the current explicit limitation. Do not attempt
to start containers in an environment without container support.

NFR-1 additionally needs controlled operator-run qualification on an isolated,
synthetic-only reference-store scope. Compare matched zero-probe control and
verified opening elapsed times, with at least 100 cold and 100 reused-transport
openings. Record sanitised aggregate elapsed values, observed maximum and
verification outcomes only. Acceptance requires every measured added sequence
at most 1 second **and** successful verification on the enforcing reference
store; a timeout that merely fails closed is not success. No live-service calls
belong in deterministic CI or this design-stage run.
