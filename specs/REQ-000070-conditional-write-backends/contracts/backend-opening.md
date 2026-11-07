# Contract: Library classification and URL opening

**Status**: Proposed at G3; covers the FR-1 production selections.
**Consumers**: Root library callers, CLI, later Terraform provider integration.

## Public API

Proposed declarations, not implementation bodies:

```go
type BackendClassification string

const (
    BackendVerified BackendClassification = "verified"
    BackendUnverified BackendClassification = "unverified"
)

func ClassifyBackend(rawURL string) (BackendClassification, error)

// Explicit context-scoped opening option; both opener signatures are retained.
func WithAllowUnverifiedBackend(ctx context.Context) context.Context
func Open(ctx context.Context, rawURL string, opts ...Option) (*Client, error)
func OpenStorage(ctx context.Context, rawURL string) (storage.Storage, error)

// Sentinel identity is matched through errors.Is, including wrapping.
var ErrUnverifiedBackend error
```

The opening option derives a context using a private typed key, following
`storage/context.go`'s `WithAccessPropagationRetry` pattern. Parent cancellation,
deadline and values are preserved, and the parent is not modified. No marker
means no consent. Scope a derived context to the intended opening; any URL open
given that same context inherits its explicit consent. Do not persist it or
reuse it globally. Existing nil client options retain their existing behavior.
The sentinel text is `ipam: unverified backend`; refusal wraps it with static
guidance. It does not also match `ErrInvalidArgument`, `ErrConflict` or a
storage sentinel. The API creates no public access mode or probing interface.
(AC-5, AC-7, AC-8; FR-2/FR-5.)

Synthetic usage shapes for consumers:

```go
classification, err := ipam.ClassifyBackend(rawURL)
store, err := ipam.OpenStorage(ipam.WithAllowUnverifiedBackend(ctx), rawURL)
client, err := ipam.Open(ipam.WithAllowUnverifiedBackend(ctx), rawURL, ipam.WithMaxAttempts(0))
```

These are separate usage examples; callers handle each error before using its
result. No URL or credential is required in classification output. A provider
can classify first, present its own warning, and translate its later explicit
configuration into a narrowly scoped opening context without this Requirement
changing the provider.

## Pure policy algorithm

All entry points reuse `parseOpenURL` and one private classifier over
`openTarget`. Validation errors take precedence over refusal even with consent.

| Validated selection | Result | Trace |
| --- | --- | --- |
| S3 with no `endpoint=` | verified | AC-1 |
| S3 with endpoint hostname ending in `.amazonaws.com` or `.amazonaws.com.cn`, with a nonempty preceding hostname | verified | AC-2 |
| Account-form Azure Blob under `.blob.core.windows.net` | verified | AC-3 |
| File or memory | verified | AC-3 |
| S3 with any other endpoint hostname | unverified | AC-4, AC-18, AC-25 |

For endpoint matching use the parsed endpoint's `Hostname()`, ASCII case-folded
to lowercase, not its URL text, `Host` with port, bucket, prefix, query or
path. Match dot-bounded suffixes only; bare `amazonaws.com`/`amazonaws.com.cn`,
IP literals and `amazonaws.com.example.test` are unverified. A terminal DNS dot
is not stripped: it conservatively needs consent because it does not match the
literal approved suffix. Valid ports/path style and endpoint paths do not
otherwise affect hostname classification. Do not change validation to reject
formerly valid nonmatching hosts. (AC-2, AC-4, AC-23, AC-25.)

The broad AWS host suffixes are the approved policy, not evidence that every
matching AWS service is S3 or that HTTP, DNS or SDK configuration is trustworthy.
Preserve the existing HTTP/HTTPS grammar; no new TLS or credential policy is
introduced. SDK default resolution is treated as verified as specified, not
inspected for hidden configuration overrides. There are zero DNS lookups,
credential reads, requests, probes, timers or learned classifications in
`ClassifyBackend`. (AC-1, AC-17, AC-18; no-auth/no-grammar-change constraints.)

Container-form Azure emulators are outside this table. Do not expand the public
verified list to cover them implicitly; resolve the plan's G3 scope question
before assigning that existing test-only URL a public classification.

## Opening outcomes

| Input and policy | Store/client result | Error or behavior | Trace |
| --- | --- | --- | --- |
| Invalid URL, any consent | nil | Existing `ErrInvalidArgument`, reason only | AC-23 |
| Valid unverified URL, absent consent | nil | Wrap `ErrUnverifiedBackend`; zero constructors | AC-5 |
| Valid unverified URL, explicit consent | Existing construction result | Constructor/operation errors remain real errors; classification stays unverified | AC-7, AC-18 |
| Valid verified URL | Existing construction result | Existing prerequisites and error behavior; no warning | AC-1..AC-3, AC-27 |

`OpenStorage` reads consent from the context after validation and classification,
before construction. `Open` continues to call `OpenStorage` and then `New` with
the existing options; callback order and once-only evaluation are unchanged.
Existing retry/backoff/clock/random/sleeper/ID options retain their values.
Constructor failure returns no client; no partially initialized handle escapes.
`OpenStorage` preserves successful return types, close ownership and constructor
wrapping. Neither opening entry point prints anything or performs diagnostic
storage operations. (AC-5, AC-7, AC-17, AC-27.)

Existing calls and fixed-arity opener function assignments still compile.
`Option` remains the existing `func(*Client)` type; do not replace it with a new
interface or require callers to convert `[]Option`. Direct backend construction
and `New(store, ...)
remain caller-managed escape surfaces outside the URL policy; no backend is
marked verified on that basis. (FR-7 scope; AC-17, AC-18.)

## Static refusal and documentation

Use the common documentation URL:
`https://github.com/appvia/ipam#supported-backends`.

The library refusal states that the S3 endpoint is unverified, requires
`If-None-Match: *` and `If-Match: <etag>` on PutObject and `If-Match: <etag>` on
DeleteObject, warns that ignoring them risks concurrent corruption, names the
applicable library option and both CLI/environment opt-ins, and links to that
URL. It never interpolates the input URL, endpoint, bucket, prefix, credentials
or constructor errors into this policy message. Normal invalid URL and verified
constructor errors retain existing redaction rules. (AC-5, AC-6, AC-21, AC-24.)

The README section and `s3.Config.Endpoint` doc comment use that same anchor.
Operator contract-suite guidance must include conditional delete, with isolated
synthetic stores. An emulator run skipping conditional delete cannot qualify a
new verified provider. (AC-19..AC-21; FR-8.)
