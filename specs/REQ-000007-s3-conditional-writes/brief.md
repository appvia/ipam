# Brief: Refuse S3 stores without conditional-write protection

## Problem

IPAM can use an S3-compatible store that accepts conditional-write requests but does not enforce them. Concurrent changes against such a store can allocate the same address range without warning. The current optional `ipam doctor` check does not protect clients when they first use a backend.

## Outcome

IPAM verifies conditional-write protection when an S3 backend is opened. Until the store is verified, users can read but cannot write; a store that does not enforce the required conditions is refused with a clear error before existing data is changed. Amazon S3 remains supported.

## Who benefits

Platform teams using the IPAM CLI or Terraform provider with Amazon S3 or S3-compatible storage.

## Success measures

- An endpoint that ignores conditional writes is identified before IPAM writes existing data.
- Reads remain available while a store is unverified, but writes do not.
- Verification runs once each time the backend is opened and takes no more than 1 second on Amazon S3 (about four or five small requests).

## Constraints

- Do not change or risk existing user data during verification. The author permits a temporary check object under a reserved prefix.
- A later probe cleans up its own leftover check objects. `ipam doctor` reports any residue that remains; documentation recommends a lifecycle rule for versioned buckets.
- The supported-endpoint rule is defined by [BASE-0012](../BASE-0012-s3-backend/spec.md): endpoints enforce `If-None-Match` and `If-Match` on `PutObject`, and `If-Match` on `DeleteObject`; Amazon S3 is the reference target.

## Out of scope

- Proving conditional delete behavior for every vendor.
- Certifying third-party stores.

## Open questions

- None. See [clarifications](clarifications.md).
