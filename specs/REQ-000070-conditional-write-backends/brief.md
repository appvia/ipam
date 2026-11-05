# Brief: Refuse unverified conditional-write backends by default

**Requirement**: REQ-000070 (appvia/ipam#70)
**Created**: 2026-11-05
**Status**: Draft

## Problem

IPAM relies on storage services enforcing conditional writes to prevent concurrent changes from overwriting one another or allocating the same address twice. Some S3-compatible services accept these conditions but ignore them. Today operators can configure such a service without a warning, and the documentation does not clearly distinguish verified backends from unverified ones.

## Outcome metric

- **OM-1**: At release, zero unverified S3-compatible endpoints are opened without explicit operator opt-in, and backend classification makes zero network requests per open.

## Users

- **IPAM operators**: Need to know whether a backend is within the verified support boundary and to explicitly accept the risk before using an unverified S3-compatible endpoint.
- **IPAM maintainers**: Need a clear support boundary and consistent guidance for operating and diagnosing backends.

## Constraints

- Classify backends by URL without network calls or writes during open. Verified URLs are `s3://` without an endpoint or with an AWS S3 host (`*.amazonaws.com` or `*.amazonaws.com.cn`), account-form Azure Blob URLs, `file://` and `mem://`; other S3-compatible endpoints are unverified.
- S3-compatible services must enforce `If-None-Match: *` and `If-Match: <etag>` on object creation and replacement, and `If-Match: <etag>` on deletion. A service that ignores these conditions can corrupt pool and reservation documents during concurrent use.
- Preserve existing behavior for verified backends. Refuse unverified backends by default, while allowing explicit CLI and library opt-in. The CLI supports `--allow-unverified-backend` and `IPAM_ALLOW_UNVERIFIED_BACKEND=true`; the flag takes precedence and one warning is printed when the override is used. Errors and warnings name the override and link to documentation without disclosing endpoint URLs, bucket names, prefixes or credentials. Existing backend URL validation still applies.
- `ipam doctor` reports PASS for verified backends, WARN for unverified backends when allowed, and FAIL otherwise; it continues its other read-only checks when classification fails.
- Documentation must state the conditional-write requirement and verified support boundary, and direct operators to check unverified endpoints themselves, for example with the storage contract suite. It must explain that doctor cannot prove conditional DELETE and that its write probe can change data on a backend that ignores conditions.
- The library must enable a future Terraform provider opt-in, but changes to the provider itself are a separate follow-up.

## Out of scope

- Probing or proving conditional-write enforcement when a backend opens, or providing a read-only mode for unverified backends.
- Fixing the existing `ipam doctor` write probe or detecting non-enforcing Azure-compatible emulators.
- Implementing the Terraform provider change.

## Open questions

- None. The updated Requirement defines the business behavior; exact library API shape is a design decision.
