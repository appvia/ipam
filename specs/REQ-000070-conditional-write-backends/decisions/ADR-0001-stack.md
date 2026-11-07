# ADR-0001: Retain the exact-pinned Go library and Cobra CLI stack

**Status**: Proposed for G3
**Trace**: AC-1..AC-28; FR-7 compatibility; NFR-1/NFR-5; constitution II/III.

## Context

Product registry membership determines which repository to inspect, not which
stack to assume. `go.mod` declares `github.com/appvia/ipam`, Go 1.27.3 and
Cobra v1.10.2; `open.go`, `client.go`, `storage/storage.go` and
`cmd/ipam/commands.go` implement the needed library, storage and command seams.
`mise.toml` pins Go 1.27.3 and golangci-lint 2.14.0. `go.mod`/`go.sum` carry
exact dependencies, while `.github/workflows/test.yml` runs platform tests,
Linux race checks, lint and emulator integration on PRs.

The feature is a local policy decision on validated input and an extension to
existing diagnostics. It needs no service, database, cloud deployment or new
SDK. Existing conditional storage behavior must remain unchanged.

## Decision

Use the existing Go/Cobra stack at its checked-in pins. Implement future policy
logic in the root library, and consent/presentation in the existing CLI. Use
standard `net/url`, `strings`, `errors` and `strconv` helpers rather than adding
a classification dependency. Add no new runtime, framework, platform or store.

Use the installed `ac-test-mapping` skill for later AC evidence. No Go stack
skill is present in the product checkout; explicit commands are `make build`,
`make test`, `make lint`, with existing integration jobs retained. Maintain
`go.mod`/`go.sum`, full-SHA action pins and toolchain pins unchanged unless a
separate dependency requirement is approved. Agents do not apply infrastructure.

## Alternatives considered

- A new policy service or external provider registry would add network activity,
  availability dependencies and deployment without improving the specified
  host-only contract (AC-17, NFR-1).
- A different language/CLI framework would unnecessarily duplicate existing
  option, error and output contracts and endanger verified-backend compatibility
  (FR-7, AC-27).
- An additional hostname library is unnecessary for dot-bounded ASCII suffix
  comparisons of already validated URLs (AC-2, AC-25).

## Consequences

Existing callers, test harnesses and CI can cover the change in one repository
with no toolchain migration. Exact dependency pins and existing regressions are
preserved. Host classification remains a limited support policy and cannot
prove endpoint enforcement; that limitation belongs in operator documentation
and the threat model, not a new technology layer (AC-18..AC-20).
