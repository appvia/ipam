# Data model: opening verification

No existing pool, reservation, allocation or schema-version changes. All added
persisted data is synthetic, temporary and outside application key prefixes.
(AC-1, AC-4; NFR-4.)

## Configured and verified handles

| Entity | Fields | Invariants and trace |
| --- | --- | --- |
| Configured S3 handle | Existing SDK client, bucket, normalized prefix, immutable per-call options; readiness false | `s3.New` remains offline. Public storage/inspection calls fail `ErrNotOpen` with zero requests. AC-10, AC-14. |
| Opening attempt | Caller context, newly generated candidate key, ownership token, phase, last known version, possible-write flag, primary result, cleanup result | Exists only in memory; no global or cross-open capability cache. Every explicit open gets its own attempt. AC-5, AC-6, AC-8. |
| Verified S3 handle | Copy of configured immutable transport/configuration, readiness true | Returned only after both conditions/readbacks verify and caller is not cancelled. No per-operation checks. AC-1, AC-6, AC-8. |
| Safe opening error | Kind, condition, phase, classified safe cause(s) | Unsupported and inconclusive are distinct. No URL, bucket, credential, raw SDK error or response body. AC-2, AC-3, AC-7, AC-9. |
| Warning | Stable code `s3_open_cleanup_incomplete`, fixed safe message | Independent of error/success; delivered at most once per attempt through context handler or default logger. AC-9, AC-12, AC-13. |

`storage.Version` remains opaque and is sent verbatim, as in
`storage/storage.go` and `storage/s3/s3.go`. Equality checks for readback and
staleness do not parse or synthesize ETags. (AC-3.)

## Scratch object

The key is `.ipam-checks/v1/<64-lowercase-hex>.json` relative to the configured
prefix; the nonce has 256 bits from `crypto/rand`. The nonce is an isolation
identifier, not authentication material. It contains no pool, tenant, client or
allocation identifier. At most one key is written per opening attempt; up to
three absence-checked candidate names are allowed before failing. (AC-4, AC-5.)

Payloads are UTF-8 JSON with a maximum encoded size of 1 KiB. Readbacks are
bounded to 4 KiB plus one overflow-detection byte, not the unbounded `Get`
allocation used for normal application reads. Example schema, not real data:

```json
{"kind":"ipam-opening-check","format":1,"owner":"fictional-per-open-nonce","phase":"a"}
```

The generated owner is the opening nonce; phase is one of `a`, `b`, `c`.
These distinct bytes produce an initial object, legitimate replacement and
deliberately rejected replacement. No timestamps, secrets, application data
or deployment details are written. There is no relationship to pool documents.
(AC-3, AC-4, AC-9; NFR-4.)

## State transitions

`configured -> checking -> verified` or `configured -> checking -> refused`.
These are states of each new candidate, not mutations of a caller's source
handle. Cleanup is attempted from every state where a scratch write may have
landed. It has its own `not-needed / removed / incomplete` result; `incomplete`
emits a warning and cannot turn refusal into success or verified into refusal.
Check cancellation before publication. (AC-6, AC-8, AC-12, AC-13, AC-14.)

Protocol phases: `select-key`, `create-a`, `reject-create-b`, `replace-b`,
`reject-stale-c`, then `cleanup`. An SDK unknown outcome makes verification
inconclusive even if a later read establishes ownership for cleanup. Readback
does not retroactively prove the rejected condition. (AC-2, AC-3, AC-7.)

## Lifetime and ownership limits

Attempt cleanup immediately; use five-second bounded cancellation-detached
cleanup after failure/cancellation too. No janitor, lifecycle policy, namespace
scan or crash recovery is added. Versioned buckets can retain noncurrent
synthetic versions and delete markers; successful logical removal is not a
claim of physical erasure. Ownership checks are defense in depth, not atomic
exclusion against arbitrary other writers. The unresolved absolute AC-4
guarantee and required namespace assumption are documented in ADR-0003.
(AC-4, AC-5, AC-8, AC-12, AC-13; process-interruption exclusion.)
