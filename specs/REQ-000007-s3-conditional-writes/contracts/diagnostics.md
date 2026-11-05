# Contract: doctor, residue and operator guidance

Status: proposed; DQ-1 in `../plan.md` must be settled at G3.
Sources: `cmd/ipam/doctor.go`, `cmd/ipam/main.go`, `README.md`,
`storage/s3/s3.go` (`Inspect`), `storage/inspect.go`.

## Modes

`ipam doctor` on S3 uses the normal opening's evidence; it never runs a second
capability sequence. It adds `conditional-write-protection`, `probe-cleanup` and
`probe-residue` global rows to the existing check model. Endpoint verification
does not imply permission to mutate a specific reservations document.

Proposed new flag: `ipam doctor --no-write`. Validate it before opening, then
use the read-only opener. It suppresses opening probes, pending cleanup and
every existing per-pool write probe, on **all** backends. GET, HEAD and LIST
diagnostics, including protection inspection and fragmentation, remain available.
`conditional-write-protection` is WARN with "not verified: no-write mode";
never PASS based on a previous opening or a different identity's evidence.

Existing `--expect-write` and `--expect-no-write` remain repeatable per-pool grant
assertions and may be mixed for different pools as today. They are **not** the
new flag and do not silently select no-write construction. In explicit no-write
mode, report each assertion as WARN, "not evaluated: no-write mode"; do not
turn inability to send a request into an observed grant or denial. Existing
conflicting expectations for one pool remain usage errors before opening.

For normal S3 diagnosis, permit legacy per-pool false-version grant probes only
after this opening verifies. On unverified S3, suppress them and report WARN,
"not evaluated: backend verification unavailable/failed"; a failed capability
row still makes the overall report fail. Probe-prefix access denial says nothing
about a pool's write grants. Other backends keep existing per-pool probe
semantics outside explicit no-write mode. The per-pool S3 probes remain a
snapshot assumption, not safety against a store becoming malicious later.

This intentionally changes the old S3 doctor's universal no-create/delete
claim: default S3 opening now mutates only synthetic objects, while explicit
no-write mode performs no mutations. Update help, README and compatibility
tests together, without changing unrelated backend behavior. The associated
baseline documentation differences must be reviewed at G3; this design does
not edit any baseline or approved spec. Traces: AC-4 through AC-6, AC-11, AC-15; FR-7.

## Rows and exits

Keep `doctorReport{result, checks}` and `checkResult{check, pool?, status, detail,
stats?}`. Global rows have no pool; no new stats shape is needed for them.
Existing table headings and PASS/WARN/FAIL strings remain. New global rows
follow protection rows, preceding sorted per-pool rows. Run residue inventory
independently when possible, even if the existing pool reachability check fails;
a failed pool list must not conceal visible probe residue.

| Check | PASS | WARN | FAIL |
| --- | --- | --- | --- |
| conditional-write-protection | This opening completed both positive and negative controls. | Denied, interrupted, inconclusive or explicit no-write. | A false condition was accepted. |
| probe-cleanup | Not needed or live cleanup confirmed. | Pending/denied/uncertain cleanup, with fixed reason. | No independent FAIL; never conceal enforcement FAIL. |
| probe-residue | All inventory categories complete and zero visible residue. | Any residue, unknown visibility, truncation or inventory failure. | No independent FAIL. |

Preserve existing aggregation: any FAIL -> `fail`, exit 6; otherwise any WARN ->
`warn`, exit 0; otherwise `pass`, exit 0. Ordinary blocked writes exit 1, usage
errors exit 2, output errors exit 1. A WARN is explicitly not successful
verification or an evaluated assertion. Traces: AC-6, AC-11, AC-14, AC-15, AC-17.

Synthetic example of a `probe-residue` detail:
"live=1; historical=unknown; delete-markers=unknown; version listing denied".
Never include raw returned keys, raw SDK messages, bucket names, endpoints,
client identifiers, version IDs or credentials. Table and JSON give the same
meaning; no status depends on colour. Traces: NFR-4; constitution I, IV, V.

## Inventory and lifecycle

ListObjectsV2 counts live objects under the exact configured
`.ipam-checks/v1/` subtree. A separate read-only ListObjectVersions inventory
counts historical versions and delete markers. Filter/revalidate every returned
key against that exact subtree; prefix membership is not deletion authority.
Paginate both inventories; a diagnostic work bound of 10 pages per inventory
prevents unbounded work. If more pages remain, report partial counts and
`truncated`, never a complete empty result. Listing denial, malformed pagination
or inconsistent results mean unknown/partial visibility, not absence. No bucket
versioning lookup is needed to decide whether denied version listing is unknown.
These are S3-specific diagnostics, not mutations or changes to `Inspector`.

Operator guidance must explain that successful live DELETE may retain
noncurrent versions and deletion markers. Recommend a lifecycle filter scoped
to the configured reserved subtree, addressing noncurrent-version expiry and
expired delete markers; any current-object expiry must exceed the maximum
active check duration with operational margin. Operators choose retention and
install policy manually. Never install lifecycle rules, delete versions, or
grant administrative access from this feature. Unknown crash residue must not
be automatically reclaimed from age alone. Traces: AC-9 through AC-12, AC-16, AC-17.
