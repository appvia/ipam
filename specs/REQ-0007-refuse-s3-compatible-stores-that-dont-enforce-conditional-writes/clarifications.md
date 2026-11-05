# Clarifications

Answers below were provided by Matt (Requirement author) in [issue comment 6658327129](https://github.com/appvia/ipam/issues/7#issuecomment-6658327129), in response to questions in [issue comment 6658267719](https://github.com/appvia/ipam/issues/7#issuecomment-6658267719).

1. **Question:** Does “must not change or risk existing data” allow creating and removing a temporary check object, or must the check avoid all writes?
   **Answer:** “creating temporary check object is acceptable”.
2. **Question:** Does “one check per session” mean once per client instance, or once each time the backend is opened?
   **Answer:** “each time backend opened”.
