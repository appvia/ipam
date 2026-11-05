# ADR-0001: Retain the pinned Go implementation stack

**Status**: Proposed for G3
**Traces**: AC-1, AC-5 through AC-8; NFR-1, NFR-2; constitution II, III

## Context

The requirement specifies observable S3 safety behavior, not a technology.
`appvia/ipam` is already a Go library and Cobra CLI. `storage/s3/s3.go` uses AWS
SDK v2 and Smithy; `open.go` wires typed backends into the root client. A second
runtime or external probe process would add a new credential and trust boundary
to a per-opening check with a 1-second added-latency limit.

## Decision

Choose Go 1.27.3 and golangci-lint 2.14.0 as pinned in `mise.toml` and `go.mod`.
Retain AWS SDK core v1.47.5, config v1.33.12, S3 v1.114.6, Smithy v1.28.7 and
Cobra v1.10.2; retain the exact dependency graph and `go.sum`. Use the standard
library for random synthetic ownership, contexts and HTTP fake tests. No new
database, service, runtime or dependency. Existing Go code is evidence for this
choice, not an assumed requirement.

No installed Go stack skill exists. Commands are `make build`, `make test`,
`make lint`; focused tests use `go test ./storage/s3 ./cmd/ipam . -race -count=1`.
Use `ac-test-mapping` for implementation evidence. Existing
`.github/workflows/test.yml` supplies cross-platform tests, Linux race tests,
lint and emulator integration. Preserve all of them.

Alternatives: a shell/CLI sidecar repeats SDK configuration and exposes more
error/secret surfaces; a new runtime or SDK needs migration without satisfying
any additional AC; doctor-only enforcement leaves library users unsafe.

## Consequences

Typed and URL callers share a single safety boundary without process startup
cost. Private low-level operations must coexist carefully with public guarded
methods, and test fixtures must account for opening I/O. Actual reference-store
latency still requires qualification; the stack choice is not proof of NFR-1.
