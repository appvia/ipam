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
