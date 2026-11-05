# Clarifications

Answers below were provided by Matt (Requirement author) in [issue comment 6658327129](https://github.com/appvia/ipam/issues/7#issuecomment-6658327129), in response to questions in [issue comment 6658267719](https://github.com/appvia/ipam/issues/7#issuecomment-6658267719).

1. **Question:** Does “must not change or risk existing data” allow creating and removing a temporary check object, or must the check avoid all writes?
   **Answer:** “creating temporary check object is acceptable”.
2. **Question:** Does “one check per session” mean once per client instance, or once each time the backend is opened?
   **Answer:** “each time backend opened”.

## Specification review - 2026-11-05

Matt (Requirement author) answered the specification's three open questions in
[review 5785996377](https://github.com/appvia/ipam/pull/59#pullrequestreview-5785996377).

3. **Question:** Must temporary-object removal succeed before opening succeeds,
   and what happens if removal fails?
   **Answer:** “Temporary-object cleanup: warn if removal fails”. The check
   attempts to remove its temporary data. Failed removal produces a warning;
   it does not prevent an otherwise-verified open or replace a verification
   failure with success. No guarantee of removal after process interruption
   is introduced.
4. **Question:** Does the check change the baseline's offline-construction
   promise, or is opening a distinct boundary?
   **Answer:** “Direct-construction boundary: yes, open is a boundary”. The
   opening check applies at the opening boundary; construction alone retains
   the existing no-network-call contract in BASE-0012 AC-4.
5. **Question:** Does this opening check need to select or enforce storage
   regions?
   **Answer:** “Data residency: not applicable, ignore”. Recorded as a scope
   clarification: this change checks the operator's existing configured store,
   selects no region and adds no processor or regional eligibility check. It
   does not amend or waive the constitution's deployment obligations.
