# Brief: Refuse S3-compatible stores that do not enforce conditional writes

**Requirement**: REQ-000007 (appvia/ipam#7)
**Created**: 2026-11-05
**Status**: Draft

## Problem

Platform teams use IPAM through the CLI or Terraform provider with Amazon S3 and S3-compatible storage. If a store ignores conditional writes, concurrent changes can hand out the same address range without warning. Today IPAM does not check this when the backend is first used, and the optional `ipam doctor` check may not be run.

## Outcome metric

- **OM-1**: Each backend open performs at most 1 conditional-write check; an endpoint that does not enforce conditional writes is refused with a clear message before existing data is changed.

## Users

- **Platform teams**: Use IPAM safely through the CLI or Terraform provider with S3 and S3-compatible storage.

## Constraints

- Checking must not change or risk existing data. The maintainer confirmed that creating and removing a temporary check object is acceptable.
- Supported endpoints enforce `If-None-Match` and `If-Match` on `PutObject` and `If-Match` on `DeleteObject`; Amazon S3 is the reference target (BASE-0012).
- Amazon S3 and supported S3-compatible stores must continue to work.

## Out of scope

- Proving conditional delete on every vendor.
- Certifying third-party stores.

## Open questions

- None.
