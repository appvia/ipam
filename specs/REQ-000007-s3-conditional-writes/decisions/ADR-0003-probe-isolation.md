# ADR-0003: Unique probe keys and positive-ownership cleanup

**Status**: Proposed; DQ-2 requires spec-owner resolution before implementation
**Traces**: AC-4, AC-9 through AC-12, AC-14, AC-16, AC-17; NFR-1, NFR-3, NFR-4

## Context

The verifier must remain harmless precisely when the store ignores conditions.
A fixed scratch key, prefix sweep, CAS-based ownership claim or age-only
garbage collector could delete another active check. `doc.go` already treats
lost ownership conservatively rather than reclaiming another incarnation.
The spec permits residue but does not define cross-process proof of abandoned
ownership. No trusted external liveness service is in scope.

## Decision

Generate a cryptographically random 256-bit owner and operate only on
`.ipam-checks/v1/<owner>/probe.json` relative to the configured scope. Probe
payloads are small and synthetic. Reserve this namespace from public mutations.
Cleanup is restricted to that exact generated key; returned list entries never
become deletion targets. A definitive initial collision is unowned and must
not be adopted or deleted. A lost response retains the exact candidate for
own-key recovery, without treating uncertainty as successful enforcement.

Attempt bounded cleanup after normal and failed checks, preserving enforcement
and cleanup as independent results. Track terminal pending records locally so a
later default opening can clean positively owned leftovers without touching
active sequences. Never reclaim foreign/crash residue from prefix or time.
Report such residue and recommend scoped lifecycle handling. Do not persist
credentials, introduce distributed locks/leases or claim crash-proof recovery.

This defines a **proposed** meaning of recoverable leftovers, not a waiver of
AC-10. If the owner requires automatic recovery after process death, stop and
obtain an approved ownership/liveness contract before building that behavior.

Alternatives: global scratch keys collide; timestamps do not prove inactivity;
marker CAS cannot establish exclusivity on a store being tested for broken CAS;
an external lock service expands scope and changes the coordination model.

## Consequences

Concurrent openings remain isolated even when conditions are ignored. Cleanup
may remain pending and historical versions may survive deletion. Process-local
pending records need race-safe claiming and bounded per-opening work; process
death trades automatic recovery for safety. Lifecycle retention belongs to
operators, not the agent. G3 cannot approve implementation until DQ-2 is settled.
