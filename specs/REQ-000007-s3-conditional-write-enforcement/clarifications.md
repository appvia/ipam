# Clarifications

Requirement: appvia/ipam#7

1. **Question**: Does “must not change or risk existing data” allow creating and removing a temporary check object, or must the check avoid all writes?
   **Answer**: Creating a temporary check object is acceptable.
   **Answered by**: matt-FFFFFF on 2026-11-05 ([issue comment](https://github.com/appvia/ipam/issues/7#issuecomment-6658327129)); question in [issue comment](https://github.com/appvia/ipam/issues/7#issuecomment-6658267719).

2. **Question**: Does “one check per session” mean once per client instance, or once each time the backend is opened?
   **Answer**: Each time the backend is opened.
   **Answered by**: matt-FFFFFF on 2026-11-05 ([issue comment](https://github.com/appvia/ipam/issues/7#issuecomment-6658327129)); question in [issue comment](https://github.com/appvia/ipam/issues/7#issuecomment-6658267719).

## Open questions raised in PR review

3. **Question**: With read-only credentials, should IPAM refuse to open the S3 backend, skip the check with a warning, or permit reads only while treating the endpoint as unverified?
   **Status**: Awaiting the Requirement author's answer.
   **Raised by**: matt-FFFFFF in [PR review](https://github.com/appvia/ipam/pull/64#pullrequestreview-5788475425).

4. **Question**: Is probe residue (including object versions or delete markers left by an interrupted check) acceptable, and who should clean it up?
   **Status**: Awaiting the Requirement author's answer.
   **Raised by**: matt-FFFFFF in [PR review](https://github.com/appvia/ipam/pull/64#pullrequestreview-5788475425).

5. **Question**: What is the maximum acceptable added latency for one probe sequence during each S3 backend open?
   **Status**: Awaiting the Requirement author's answer.
   **Raised by**: matt-FFFFFF in [PR review](https://github.com/appvia/ipam/pull/64#pullrequestreview-5788475425).
