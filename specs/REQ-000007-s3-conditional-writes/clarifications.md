# Clarifications

Answers below are from the Requirement author in comments on [appvia/ipam#7](https://github.com/appvia/ipam/issues/7).

1. **Question:** Does “must not change or risk existing data” allow creating and removing a temporary check object, or must the check avoid all writes? ([question](https://github.com/appvia/ipam/issues/7#issuecomment-6658267719))
   **Answer:** Creating a temporary check object is acceptable. ([author's answer](https://github.com/appvia/ipam/issues/7#issuecomment-6658327129))

2. **Question:** Does “one check per session” mean once per client instance, or once each time the backend is opened? ([question](https://github.com/appvia/ipam/issues/7#issuecomment-6658267719))
   **Answer:** Check each time the backend is opened. ([author's answer](https://github.com/appvia/ipam/issues/7#issuecomment-6658327129))

3. **Question:** With read-only credentials, should IPAM refuse to open the S3 backend, skip the check with a warning, or permit reads only while treating the endpoint as unverified? ([question](https://github.com/appvia/ipam/issues/7#issuecomment-6664642902))
   **Answer:** Allow reads and refuse writes until the store is verified. ([author's answer](https://github.com/appvia/ipam/issues/7#issuecomment-6665330131))

4. **Question:** Is probe residue (including object versions or delete markers left by an interrupted check) acceptable, and who should clean it up? ([question](https://github.com/appvia/ipam/issues/7#issuecomment-6664642902))
   **Answer:** Residue is acceptable under the reserved prefix. The next probe cleans up its own leftovers, `ipam doctor` reports any that remain, and the documentation recommends a lifecycle rule on versioned buckets. ([author's answer](https://github.com/appvia/ipam/issues/7#issuecomment-6665330131))

5. **Question:** What is the maximum acceptable added latency for one probe sequence during each S3 backend open? ([question](https://github.com/appvia/ipam/issues/7#issuecomment-6664642902))
   **Answer:** No more than 1 second on Amazon S3, for about four or five small requests. ([author's answer](https://github.com/appvia/ipam/issues/7#issuecomment-6665330131))
