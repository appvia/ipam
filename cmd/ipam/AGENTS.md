# cmd/ipam/

## OVERVIEW
Cobra CLI `ipam` (pool / alloc / doctor / version) over the root library. Distinct domain: process exit-code contract, output formats, a read-only backend doctor (score ~9).

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Exit codes, error printing, `run`/`runCLI` | `main.go` | `run` is the testable entry; `main` only wires signals + `os.Exit` |
| Command tree, flags, backend selection | `commands.go` | `--backend` beats `IPAM_BACKEND`; `-o json\|table` (default table) |
| JSON/table rendering | `output.go` | snake_case JSON structs (`poolJSON`, `allocationJSON`) |
| Doctor checks | `doctor.go` | `diagnose`, `checkBackend`, `checkProtection`, `probeWrite` |
| Harness + behaviour tests | `main_test.go` | `TestExitCodes`, `TestBackendSecretsAreNeverEchoed`, ... |
| Doctor against emulators | `doctor_integration_test.go` | `//go:build integration` |

## EXIT CODES (stable public contract)
| Code | Meaning | Source |
|------|---------|--------|
| 0 | ok | |
| 1 | other error | default |
| 2 | usage | `usageError`, `ErrInvalidArgument`, `ErrInvalidCIDR`, or any error before a leaf body started |
| 3 | not found | `ErrNotFound` |
| 4 | conflict | `ErrConflict` |
| 5 | pool exhausted | `ErrPoolExhausted` |
| 6 | doctor found a failed check | `doctorFailedError` (checked first) |
Changing a code or the mapping order in `exitCode` is a breaking change; update `commands.go` long help + `doctorHelp` + README together.

## CONVENTIONS
- Every command body is wrapped in `c.leaf(...)`: it sets `started`, so errors are classified by cause; anything failing before that (cobra args, flags, unknown command) exits 2.
- `SilenceErrors/SilenceUsage` are on; `runCLI` prints one `ipam: <msg>` line to stderr (prefix added once), plus a help hint only for exit 2.
- Openers are injected (`openFunc`, `openStorageFunc`) so tests use `mem://`/fakes; `doctor` uses the raw `ipam.OpenStorage`, not a Client.
- `version` needs no backend; `-ldflags "-X main.version=..."` sets it (`make build`).
- Empty lists render as JSON `[]`, not `null` (`TestEmptyListsAreJSONArrays`).
- Supplied allocation `--id` makes `alloc create` idempotent.
- The CLI uses `ipam.Open` defaults (8 attempts); it does not pass `WithMaxAttempts(0)`.

## DOCTOR
- Must never create, delete or change data. Checks: backend reachability, data protection (`storage.Inspector`; Unknown -> WARN), hierarchical namespace (FAIL), and a per-pool write probe.
- The write probe re-writes an existing reservations document with its own bytes under a version that can never match, and classifies precondition-failed / access-denied / success. Any change to the probe version must stay unmatchable on every backend AND be one Azure evaluates past its If-Match syntax check; read the comment in `doctor.go` before touching it.
- `--expect-write` / `--expect-no-write POOL` turn a pool's probe into a PASS/FAIL assertion (grant verification).
- Cannot detect a backend ignoring conditional DELETE (probing would delete data).

## ANTI-PATTERNS
- NEVER echo the backend URL, connection string or credentials in stderr/stdout (`TestBackendSecretsAreNeverEchoed`, `errors_message_test.go`).
- NEVER call `os.Exit` or read `os.Getenv` outside `main`; use the injected `getenv`.
- NEVER add a doctor check that writes new keys or deletes.
