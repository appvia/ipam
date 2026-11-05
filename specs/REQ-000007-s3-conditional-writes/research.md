# Design research

Read-only research of the registered code repository `appvia/ipam`; no external
service was contacted, no endpoint was probed and no production data was used.
No `.specify/extensions.yml` or setup-plan script exists in this checkout.
The Appvia plan template was read directly; no scaffolder was run that could
write outside the permitted spec directory. There are no planning hooks to run.

| Question | Decision and rationale | Alternatives / evidence | Trace |
| --- | --- | --- | --- |
| Where can enforcement cover all users? | Gate inside the typed S3 Store and default constructor. | CLI-only misses consumers; `open.go` and `client.go` wrap typed Stores. | AC-1, AC-6 through AC-8 |
| How to preserve reads? | Keep construction usable after failed verification and provide an explicit report; gate mutations. | Returning probe errors from `New` makes `OpenStorage` fail through `backendArgumentError`. | AC-5, AC-6, AC-14 |
| What establishes real conditional-write support? | Two positive controls and two false-condition rejections on a private key. | `doctor.go` currently tests only wrong-match rejection; `mapError` also maps 409, insufficient as exact proof. | AC-2, AC-3, AC-13 |
| Which stack? | Existing pinned Go, AWS SDK and Cobra; no new dependency. | Sidecar/new runtime adds configuration and credential surfaces. See ADR-0001. | NFR-1; constitution III |
| How to keep no-write really non-mutating? | Proposed `--no-write` and read-only construction before I/O. | Current `--expect-no-write` still sends PUT and is not that mode. DQ-1 is unresolved. | AC-15 |
| How to recover leftovers without deleting active work? | Exact-owner terminal records only; unknown orphans are observed, not swept. | Prefix, timestamp and CAS-based ownership are unsafe when conditions are ignored. DQ-2 is unresolved. | AC-10, AC-16 |
| How to constrain latency? | One-second shared elapsed budget and single-attempt proof; qualify actual reference-store latency separately. | Existing access-propagation retry budgets can be much longer; fake timing alone cannot establish AWS latency. | NFR-1; FR-7 |
| How to expose residue? | Separate read-only live/version/marker inventories with visibility states. | `Inspect` currently reads only versioning, while `List` only exposes live objects. | AC-11, AC-12, AC-17 |

Research resolved implementation locations and alternatives, but not the two
spec semantic gaps. Spec Kit's unresolved-clarification gate therefore remains
blocked; the plan and contracts are proposals for human review, not authority to
start implementation. The spec itself and its clarification history are unchanged.

The issue's suggestion of an opt-out is source data and conflicts with approved
FR-3; no bypass is designed. A comment asking to ignore a constitutional rule
was not followed. The current checked-in constitution, rather than that request,
is the authority used by the constitution check. No real identifiers or secret
examples from existing repository documentation are copied into these artifacts.
