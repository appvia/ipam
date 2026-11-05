# Brief: Refuse S3-compatible stores that don't enforce conditional writes

## Problem

IPAM depends on conditional writes to coordinate concurrent changes. An S3-compatible store that silently ignores those conditions can allow concurrent changes to hand out the same address range, with conflicts discovered only later.

## Users

Platform teams running IPAM through the CLI or Terraform provider with Amazon S3 or S3-compatible storage.

## Outcome

An IPAM client refuses to work with a store that does not enforce conditional writes and explains why before changing existing data. Amazon S3 and supported S3-compatible stores continue to work.

## Success measure

A non-enforcing store is rejected when its backend is opened; a supported store remains usable. The check runs each time the backend is opened. A temporary check object may be created, but existing data must not be changed or put at risk.

## Constraints

- Supported endpoints enforce `If-None-Match` and `If-Match` on `PutObject` and `If-Match` on `DeleteObject`; Amazon S3 is the reference target (BASE-0012).
- The endpoint check must not change or risk existing data.
- Checking another endpoint remains the operator's responsibility; the baseline spec suggests the storage contract suite as one way to check it.

## Out of scope

- Proving conditional delete on every vendor.
- Certifying third-party stores.

## Open questions

- None.
