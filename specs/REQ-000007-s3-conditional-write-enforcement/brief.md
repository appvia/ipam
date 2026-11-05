# Brief: Refuse S3-compatible stores that do not enforce conditional writes

**Requirement**: REQ-000007 (appvia/ipam#7)
**Created**: 2026-11-05
**Status**: Draft

## Problem

Platform teams use IPAM through the CLI or Terraform provider with Amazon S3 and S3-compatible storage. If a store ignores conditional writes, concurrent changes can hand out the same address range without warning. Today IPAM does not check this when the backend is first used, and the optional `ipam doctor` check may not be run.

## Outcome metric

- **OM-1**: Each S3 backend open performs one bounded conditional-operation probe sequence; an endpoint that does not enforce the required conditions is refused with a clear message before any existing IPAM data is changed. The acceptable added open latency remains to be confirmed.

## Users

- **Platform teams**: Use IPAM safely through the CLI or Terraform provider with S3 and S3-compatible storage.

## Constraints

- Check only `s3://` backends; do not check `azblob://` or `file://` backends.
- Check enforcement of conditional object creation, replacement, and deletion. Supported endpoints enforce `If-None-Match` and `If-Match` on `PutObject` and `If-Match` on `DeleteObject`; Amazon S3 is the reference target (BASE-0012).
- The maintainer confirmed that creating and removing a temporary check object is acceptable. The probe must use a reserved key space that IPAM never treats as application data and must not modify existing IPAM documents. Whether residue from interrupted probes is acceptable and how it is cleaned up remains to be confirmed.
- The check must handle an operation whose outcome is unknown without incorrectly declaring the endpoint safe. The permissions needed to perform and verify the check must be documented.
- Amazon S3 and supported S3-compatible stores must continue to work.

## Out of scope

- Certifying third-party stores.

## Open questions

1. With read-only credentials, should IPAM refuse to open the S3 backend, skip the check with a warning, or permit reads only while treating the endpoint as unverified?
2. Is probe residue (including object versions or delete markers left by an interrupted check) acceptable, and who should clean it up?
3. What is the maximum acceptable added latency for one probe sequence during each S3 backend open?
