# Clarifications

## Author scope update

The Requirement author confirms that the earlier brief must be redrafted: unverified backends are now refused by default, with explicit CLI and environment-variable overrides and a library open option.

**Source**: Requirement author's issue comment on 2026-11-05: [issue comment](https://github.com/appvia/ipam/issues/70#issuecomment-6669575815).

## Q1 - Doctor write probes without consent

**Question**: When doctor reports an unverified backend as FAIL because no
opt-in was supplied, should its existing write-probe checks be skipped entirely,
or may they still run?

**Answered by**: The Requirement author on 2026-11-07.

**Answer**: Existing write-probe checks must be skipped with a message to the
user when an unverified backend fails classification because no opt-in was
supplied. Other read-only checks remain available as required by the brief.

**Source**: [Requirement author's answer](https://github.com/appvia/ipam/issues/70#issuecomment-6705238569).

**Specification mapping**: FR-6, AC-28 and NFR-7 in [spec.md](spec.md).
No open questions remain.

## Q2 - Container-form Azure classification

**Question**: Should the existing test-only container-form Azure URL be
classified as Unverified, require explicit consent through the URL openers,
and retain all existing loopback and connection-string restrictions, with
emulator tests supplying explicit consent?

**Answered by**: Requirement author on 2026-11-09.

**Answer**: Yes. Container-form Azure stays test-only and is classified
Unverified; emulator tests open it with explicit consent. All existing
loopback and connection-string restrictions remain, and verified account-form
Azure behavior is unchanged.

**Source**: [Requirement author answer](https://github.com/appvia/ipam/issues/70#issuecomment-6742245024).

**Specification mapping**: FR-1; the existing unverified-backend requirements
and AC-5, AC-7, AC-8, AC-15..AC-17, AC-23 and AC-28 apply to this form.
No open questions remain.
