# Clarifications

Requirement: appvia/ipam#7

1. **Question**: Does “must not change or risk existing data” allow creating and removing a temporary check object, or must the check avoid all writes?
   **Answer**: Creating a temporary check object is acceptable.
   **Answered by**: matt-FFFFFF on 2026-11-05 ([issue comment](https://github.com/appvia/ipam/issues/7#issuecomment-6658327129)); question in [issue comment](https://github.com/appvia/ipam/issues/7#issuecomment-6658267719).

2. **Question**: Does “one check per session” mean once per client instance, or once each time the backend is opened?
   **Answer**: Each time the backend is opened.
   **Answered by**: matt-FFFFFF on 2026-11-05 ([issue comment](https://github.com/appvia/ipam/issues/7#issuecomment-6658327129)); question in [issue comment](https://github.com/appvia/ipam/issues/7#issuecomment-6658267719).
