# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed

- The release workflow publishes the GitHub release when a `v*` tag is pushed,
  instead of creating a draft.
- Update all dependencies to their latest versions.
- Require Go 1.27.3.

## [0.1.0] - 2026-10-30

### Added

- Client library: pools, child pools carved from a parent, next-free and
  explicit CIDR allocations, idempotent allocation by UUIDv7 ID, metadata
  updates, release and delete, all through conditional (ETag) writes.
- Storage backends: Amazon S3 (`storage/s3`), Azure Blob Storage with
  `ARM_*` authentication through aztfauth (`storage/azblob`), local directory
  (`storage/local`) and in-memory (`storage/memory`), plus the
  `storagetest` contract suite.
- `ipam.Open` and `ipam.OpenStorage` to build a backend from a URL.
- `ipam` CLI with `pool`, `alloc`, `doctor` and `version` commands.
- README covering concepts, document formats, access model, CLI, recovery and
  the concurrency model; runnable examples in `example_test.go`.
- `ipam doctor` reports Azure blob versioning as unknown, not off, when no
  reservations document carries a version id, because documents written before
  versioning was enabled have none until they are overwritten.
- `Client.Close` releases the directory handle a local store holds open, and the
  CLI closes what it opens. Windows cannot delete a directory that is still open.
- Windows support: `file:///C:/dir` URLs name a Windows drive path, and the
  test suite runs on Linux, macOS and Windows in CI.
- Release workflow: a `v*` tag builds the CLI with goreleaser and creates a
  draft GitHub release.
- `scripts/license-headers.sh` and the `license-headers` make targets add and
  check the Apache-2.0 header on non-Go files.

[Unreleased]: https://github.com/appvia/ipam/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/appvia/ipam/releases/tag/v0.1.0
