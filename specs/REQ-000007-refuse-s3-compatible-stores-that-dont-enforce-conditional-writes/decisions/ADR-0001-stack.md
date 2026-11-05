# ADR-0001: Keep the verifier in the pinned Go library stack

**Status**: Proposed for G3
**Trace**: AC-1, AC-6, AC-10, AC-14; Constitution I, II, III.

## Context

This Requirement selects a stack explicitly rather than assuming one from the
registry. The product is a Go library and CLI: `go.mod`, `client.go`, `open.go`
and `storage/s3/s3.go` already connect consumers to the AWS SDK. `mise.toml`
pins Go 1.27.3 and golangci-lint 2.14.0. `go.mod`/`go.sum` pin and verify SDK
dependencies, and `.github/workflows/test.yml` builds on cross-platform tests.
The check must also work for in-process infrastructure automation without an
optional external command. (AC-10.)

## Decision

Choose Go 1.27.3, the existing Go standard library and pinned AWS SDK for Go v2:
core v1.47.5, config v1.33.12, S3 v1.114.6, smithy-go v1.28.7. Use standard
`crypto/rand`, context, JSON, logging and HTTP test facilities; no new dependency,
service, language or executable. Keep the verifier inside `storage/s3` and wire
the existing root opening APIs. (AC-1, AC-6, AC-10, AC-14.)

No organisation Go stack skill is listed for this product. Required commands
are `make build`, `make test`, `make lint`; use the checked-in `ac-test-mapping`
skill for named test evidence. Keep existing CI and exact pins.
(Constitution II, III.)

## Consequences

All consumers receive the same safety behaviour at library open; no provider
shell-out or separate runtime is required. Existing SDK retry/outcome handling
and cross-platform test patterns remain reusable. (AC-6, AC-7, AC-10.)

The SDK can map different server responses to the same storage sentinel; the
verifier must distinguish definitive 412 from 409 and uncertain attempts.
Fake-store acceptance tests do not certify every vendor/version. A new external
checker or alternative runtime was rejected because it adds deployment and
credential-handling surfaces without improving the required semantics.
(AC-2, AC-3, AC-7, AC-9; certification exclusion.)
