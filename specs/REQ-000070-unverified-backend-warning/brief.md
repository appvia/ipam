# Brief: Warn when backend is not a verified conditional-write store, and document the S3 requirement

**Requirement**: REQ-000070 (appvia/ipam#70)
**Created**: 2026-11-05
**Status**: Draft

## Problem

IPAM depends on its storage backend honoring conditional writes to prevent concurrent updates from overwriting one another. Some S3-compatible services accept conditional-write headers but ignore them, and operators currently get no warning that their selected endpoint has not been verified. The README also describes S3-compatible servers without making the conditional-write requirement clear.

Operators need to know which backends IPAM treats as verified, what an unverified endpoint means for safety, and how to assess that endpoint themselves. Library and provider callers also need a way to surface the classification to their users.

## Outcome metric

- **OM-1**: At release, 100% of backend-opening paths classify the configured backend without a network call; each CLI invocation for an unverified S3 endpoint emits exactly 1 warning and remains usable, while verified backends emit 0 warnings.

## Users

- **IPAM operators**: Need a clear warning when using an unverified S3-compatible endpoint and documentation explaining the conditional-write requirement and how to check the endpoint.
- **Library and provider integrators**: Need to access the backend classification so they can present an appropriate warning to their users.
- **IPAM maintainers**: Need one explicit, maintainable list of verified backend types and a documented support boundary.

## Constraints

- Classification is based on the configured URL and must not make network calls or perform probes when opening a backend.
- An unverified backend continues to open and support the same operations as before; the warning informs operators but does not enforce or prove backend behavior.
- Warnings and errors must not reveal endpoint URLs, bucket names, prefixes, or credentials.
- Operators can silence the warning after checking their endpoint; the exact setting is left for design.
- Documentation must explain the conditional-write contract, verified backend boundary, operator verification responsibility, and the known limits of `ipam doctor` and its write probe.

## Out of scope

- Probing an endpoint or otherwise proving at runtime that it enforces conditional writes.
- Refusing to open an unverified backend or changing it to read-only.
- Changing the existing `ipam doctor` write probe or detecting conditional-delete behavior through a destructive probe.
- Detecting non-enforcing behavior on Azure-compatible emulators.

## Open questions

- None for the business outcome. The warning-suppression setting's exact name and form are intentionally deferred to design.
