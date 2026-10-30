# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

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
