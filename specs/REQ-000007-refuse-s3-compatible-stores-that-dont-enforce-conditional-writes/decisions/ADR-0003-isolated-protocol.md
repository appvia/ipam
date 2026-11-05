# ADR-0003: Isolate the protocol and expose its integrity assumption

**Status**: Proposed; blocked on owner resolution of absolute AC-4
**Trace**: AC-1 through AC-5, AC-7, AC-9, AC-11; NFR-2, NFR-4.

## Context

`storage/s3/s3.go` implements create-only and version-conditional PUT through
opaque ETags. `storage/storagetest/contract.go` demonstrates positive and stale-
version cases. `cmd/ipam/doctor.go` instead probes existing reservations; this
Requirement excludes changing that probe and requires no existing-object
mutations, even on stores that ignore the condition being tested. (AC-4.)

There is a fundamental atomicity limit: before the first probe write there is
no trusted conditional primitive. A random key is not proof of absence. An
absence GET is not atomic with the subsequent PUT; a different writer can
create an object in between, and a non-enforcing store can overwrite it. An
ownership read before cleanup has the same limitation if deletes also ignore
conditions. Absolute AC-4 cannot be proven for arbitrary concurrent writers
solely with these APIs. (AC-4; NFR-2.)

## Decision

Propose a reserved `.ipam-checks/v1/` namespace under the existing prefix,
256-bit per-open random names, bounded collision checks and exclusively
synthetic owner-marked payloads. Cooperating concurrent opens choose independent
keys. Never scan or probe application documents. (AC-4, AC-5; NFR-4.)

The protocol uses successful create/update controls, failed duplicate creation,
and a failed replacement with an actually stale observed ETag, with bounded
readbacks after each stage. Accept only definitive 412 with no unknown-outcome
history for the negative probes; 409 and all uncertain/malformed results refuse
opening as inconclusive. Both default and custom endpoints run it.
See `../contracts/conditional-write-check.md`. (AC-1, AC-2, AC-3, AC-7, AC-11.)

Do not claim this satisfies AC-4 in an unconstrained adversarial namespace.
G3 must obtain an owner decision reconciling exclusive cooperating namespace
use, honest reads and cryptographic isolation with the approved absolute
integrity requirement. If that assumption is unacceptable, stop implementation
and require an approved spec clarification or a trusted storage isolation
primitive. This ADR does not create that primitive or waive the AC.
(AC-4; Constitution I.)

## Consequences

No pool/reservation bytes or versions are deliberately touched, even when either
negative probe unexpectedly succeeds. Concurrent conforming opens do not share
scratch data. A static key, existing-document probe, fabricated ETag-only test,
endpoint allowlist or negative-only protocol is rejected for integrity,
correctness or false acceptance. (AC-1 through AC-5, AC-11.)

At least one temporary object and multiple storage requests are necessary;
one check is not one request. Random isolation and ownership checks make normal
collisions negligible but not impossible or atomic. False-capability claims by
a malicious server, later behavioural changes and conditional-delete support
remain outside what this point-in-time PUT check can prove. (AC-6; stated scope.)
