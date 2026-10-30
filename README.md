# ipam

Go client library and CLI for an IP address manager that keeps its state as
JSON documents in object storage (Amazon S3, Azure Blob Storage, or a local
directory). There's no server and no database. Every change is a conditional
write (ETag compare-and-swap), so many processes can allocate from the same
pool at once without overlapping.

The Terraform provider `appvia/ipam` is built on this library. The Terraform
modules `terraform-aws-ipam` and `terraform-azurerm-ipam` provision the bucket
or storage account and the identities described in [Access model](#access-model).

## Concepts

**Pool.** A named, single-family (IPv4 or IPv6) set of disjoint CIDRs that
allocations are taken from. Pool IDs are lowercase letters, digits and
hyphens, 1 to 63 characters, starting and ending with a letter or digit.

**Root pool.** A pool created with explicit CIDRs, for example `10.0.0.0/8`.

**Parent pool and child pool.** A child pool is carved out of a parent pool,
either as the next free block of a prefix length or as one explicit CIDR. The
carve-out is an ordinary allocation in the parent with the ID
`pool:<child>:<incarnation>`, so the parent never hands that space out again.
Deleting the child releases the carve-out.

**Allocation.** One CIDR assigned from a pool. You ask for either the next free
block of a prefix length (`next`) or an exact CIDR (`explicit`). Each pool has
its own strategy for `next`: `best-fit` takes the smallest free block that
fits, and `lowest` takes the lowest free address. New pools default to
`best-fit`. Pools created before strategies existed are `lowest`. See
[Allocation strategy and fragmentation](#allocation-strategy-and-fragmentation).

**Explicit CIDR as reservation.** An explicit allocation reserves exactly that
range. Use it to record space that's already in use (an existing VPC, a peered
network), so `next` allocations never land on it. It fails with an `overlap`
conflict if any part of the range is already allocated.

**Generated allocation IDs and idempotency by ID.** Every allocation has a
UUID. If you don't pass one, the client generates a UUIDv7 once, before its
first attempt, and reuses it on every retry. Repeating a request with the same
ID and the same request returns the existing allocation without a second
write, which makes retries after a lost response safe. Reusing an ID with a
different request is a conflict with reason `id_reused`.

**Incarnation.** Every `CreatePool` call generates a random incarnation and
writes it into both of the pool's documents. A pool is usable only while both
documents exist with the same incarnation. This stops a slow or interrupted
creator from reviving a deleted pool.

## Storage layout

A backend holds two JSON documents per pool, below an optional prefix:

| Key | Contents | Written by |
| --- | --- | --- |
| `pools/<id>.json` | the pool definition | `CreatePool`, `UpdatePoolMetadata`, `DeletePool` |
| `reservations/<id>.json` | the pool's allocations | `Allocate`, `Release`, `UpdateAllocationMetadata`, and pool create/delete |

Splitting them means that allocating never touches the pool definition, so an
identity that may only allocate needs no write access to `pools/*`.

`pools/prod-main.json`:

```json
{
  "schema_version": 1,
  "kind": "pool",
  "pool_id": "prod-main",
  "incarnation": "0123456789abcdef0123456789abcdef",
  "family": 4,
  "cidrs": [
    "10.0.0.0/16",
    "10.2.0.0/16"
  ],
  "parent": {
    "pool_id": "global",
    "allocation_id": "pool:prod-main:0123456789abcdef0123456789abcdef"
  },
  "description": "production",
  "labels": {
    "region": "eu-west-1",
    "team": "platform"
  }
}
```

`parent` is present only on child pools. `reservations/prod-main.json`:

```json
{
  "schema_version": 1,
  "kind": "reservations",
  "pool_id": "prod-main",
  "incarnation": "0123456789abcdef0123456789abcdef",
  "allocations": {
    "018f22e2-1865-7be8-9f45-35d90607d0f7": {
      "request": {
        "kind": "next",
        "prefix_length": 24
      },
      "cidr": "10.0.0.0/24",
      "description": "application",
      "labels": {
        "environment": "prod"
      },
      "created_at": "2026-10-29T12:30:00Z"
    },
    "pool:child-1:fedcba9876543210fedcba9876543210": {
      "request": {
        "kind": "explicit",
        "cidr": "10.2.0.0/24"
      },
      "cidr": "10.2.0.0/24",
      "description": "",
      "labels": {},
      "created_at": "2026-10-29T12:31:00Z"
    }
  }
}
```

Each document picks its schema version on its own, every time it's written:

- The pool document is v1 when the strategy is `lowest`, and v2 otherwise. A
  v2 pool document adds `strategy`.
- The reservations document is v1 when the pool has no cooldown and no release
  records, and v2 otherwise. A v2 reservations document adds `cooldown`
  (`mode` and whole `seconds`) and `released`, a map from each released CIDR to
  the time its cooldown ends.

So a pool created with the default `best-fit` strategy has a v2 pool document,
and a `lowest` pool without a cooldown keeps exactly the v1 bytes shown above.
The v2 forms of the same two documents:

```json
{
  "schema_version": 2,
  "kind": "pool",
  "pool_id": "prod-main",
  "incarnation": "0123456789abcdef0123456789abcdef",
  "family": 4,
  "cidrs": [
    "10.0.0.0/16",
    "10.2.0.0/16"
  ],
  "parent": {
    "pool_id": "global",
    "allocation_id": "pool:prod-main:0123456789abcdef0123456789abcdef"
  },
  "description": "production",
  "labels": {
    "region": "eu-west-1",
    "team": "platform"
  },
  "strategy": "best-fit"
}
```

```json
{
  "schema_version": 2,
  "kind": "reservations",
  "pool_id": "prod-main",
  "incarnation": "0123456789abcdef0123456789abcdef",
  "allocations": {
    "018f22e2-1865-7be8-9f45-35d90607d0f7": {
      "request": {
        "kind": "next",
        "prefix_length": 24
      },
      "cidr": "10.0.0.0/24",
      "description": "application",
      "labels": {
        "environment": "prod"
      },
      "created_at": "2026-10-29T12:30:00Z"
    }
  },
  "cooldown": {
    "mode": "strict",
    "seconds": 86400
  },
  "released": {
    "10.0.1.0/24": "2026-10-30T12:30:00Z"
  }
}
```

A document with an unknown `schema_version` is refused with
`ErrUnsupportedSchema`. Clients older than this release only know v1, so they
refuse v2 documents; see [Upgrading](#upgrading). Unknown fields are ignored
when reading.

Don't edit these documents by hand. Use the CLI or the library, which keep the
incarnations and the non-overlap invariant intact.

## Backend URLs and credentials

The CLI (`--backend` or `IPAM_BACKEND`) and `ipam.Open`/`ipam.OpenStorage`
take the same URLs:

| URL | Backend |
| --- | --- |
| `s3://<bucket>/<prefix>?region=..&endpoint=..&path_style=true` | Amazon S3 or an S3-compatible server. All query parameters are optional. |
| `azblob://<account>.blob.core.windows.net/<container>/<prefix>` | Azure Blob Storage with Entra ID credentials from `ARM_*` variables. |
| `azblob://<container>/<prefix>` | Azure Blob through `AZURE_STORAGE_CONNECTION_STRING`. Loopback endpoints (Azurite) only. |
| `file:///absolute/path` | A directory on a local disk. Created if missing. |
| `mem://` | In-memory, for tests. Every `Open` gets an empty store. |

Credentials never go in the URL. A URL with user info, an Azure query (SAS
token), an unknown scheme or query parameter, or a malformed value is
rejected with `ErrInvalidArgument`, and the error text never echoes the URL.

**AWS** uses the AWS SDK default credential chain: environment variables,
shared config and credentials files (`AWS_PROFILE`), web identity, ECS and EC2
instance roles. The region comes from `?region=`, or else from `AWS_REGION` or
the shared config. It's never looked up through instance metadata, and a
missing region is an error.

**Azure** authenticates exactly like the `azurerm` and `azapi` Terraform
providers, through [aztfauth](https://github.com/Azure/entrauth). Set the usual
`ARM_*` variables: `ARM_TENANT_ID`, `ARM_CLIENT_ID`, `ARM_CLIENT_SECRET`,
`ARM_CLIENT_CERTIFICATE_PATH`, `ARM_USE_OIDC` (GitHub Actions and Azure DevOps
token variables are picked up), `ARM_USE_MSI`, `ARM_USE_AKS_WORKLOAD_IDENTITY`,
and `ARM_USE_CLI` (on by default, so a local `az login` works). Shared keys and
SAS tokens aren't supported. The container must already exist; the backend
never creates or deletes it.

```text
export IPAM_BACKEND='s3://acme-ipam/prod?region=eu-west-2'
export IPAM_BACKEND='azblob://acmeipam.blob.core.windows.net/ipam'
```

## CLI

Build it with `make build` (or `go build ./cmd/ipam`). Every command takes two
global flags:

- `--backend URL`: the backend; defaults to `IPAM_BACKEND`.
- `-o, --output json|table`: output format; defaults to `table`.

Commands:

| Command | What it does |
| --- | --- |
| `ipam pool create <id> --cidr CIDR...` | Create a root pool. Repeat `--cidr` for more ranges. |
| `ipam pool create <id> --parent ID --prefix-length N` | Carve the next free `/N` from a parent as a child pool. |
| `ipam pool create <id> --parent ID --cidr CIDR` | Carve an explicit CIDR from a parent. |
| `ipam pool get <id>` | Show a pool. |
| `ipam pool list` | List pools. Pools with an unsupported schema are skipped with a warning on stderr; the command still exits 0. |
| `ipam pool update <id> [--description TEXT] [--label k=v...]` | Change metadata. `--label` replaces the whole label set. |
| `ipam pool update <id> [--strategy S] [--cooldown D --cooldown-mode M \| --no-cooldown]` | Change the allocation policy. |
| `ipam pool delete <id>` | Delete an empty pool, releasing its parent carve-out. |
| `ipam alloc create <pool> --prefix-length N [--id UUID]` | Allocate the next free `/N`. Prints the generated ID. |
| `ipam alloc create <pool> --cidr CIDR [--id UUID]` | Reserve an explicit CIDR. |
| `ipam alloc get <pool> <id>` | Show an allocation. |
| `ipam alloc list <pool>` | List allocations, including child-pool carve-outs. |
| `ipam alloc update <pool> <id> [--description TEXT] [--label k=v...]` | Change metadata. |
| `ipam alloc release <pool> <id>` | Release an allocation. Releasing an absent ID succeeds. |
| `ipam doctor [--expect-write POOL...] [--expect-no-write POOL...]` | Check a backend without changing data (see below). |
| `ipam version` | Print the CLI version. |
| `ipam completion bash\|zsh\|fish\|powershell` | Print a shell completion script. |

`pool create` and `alloc create` also take `--description TEXT` and
`--label key=value` (repeatable). `pool create` also takes the policy flags:

- `--strategy best-fit|lowest`: the `next` strategy. Defaults to `best-fit`.
- `--cooldown D` with `--cooldown-mode strict|fit-aware`: hold released CIDRs
  back for `D`, a Go duration such as `1h` or `30m`. The two flags go
  together. `D` must be whole seconds, from `1s` to `8760h`.

`pool update` takes the same flags, plus `--no-cooldown` to turn the cooldown
off. Flags you don't pass keep their current value. When you pass both policy
and metadata flags, the policy is written first and the metadata second. If
the metadata write fails, the policy change stays applied; run the same
command again to finish.

Table output for pools has `STRATEGY` and `COOLDOWN` columns. JSON output
always has `"strategy"`, and has `"cooldown": {"mode": ..., "duration": ...}`
only when the pool has a cooldown. `alloc` has the alias `allocation`.
`ipam help <command>` and `--help` show help for any command.

A walk-through against a local directory:

```sh
export IPAM_BACKEND="file://$PWD/ipam-data"

ipam pool create global --cidr 10.0.0.0/8 --description 'all private space'
ipam pool create prod --parent global --prefix-length 16 --label env=prod
ipam alloc create prod --prefix-length 24 --description app
ipam alloc create prod --cidr 10.0.200.0/24
ipam alloc list prod
ipam alloc list global
ipam pool list -o json
```

`ipam alloc list prod` prints something like:

```text
ID                                    CIDR           CREATED_AT                   DESCRIPTION  LABELS
01a18ac9-7e3f-7d23-b9bf-922f047b361e  10.0.0.0/24    2026-10-30T03:10:23.297999Z  app          -
01a18ac9-7e53-76f5-a51b-52c349b78d52  10.0.200.0/24  2026-10-30T03:10:23.317368Z  -            -
```

and `ipam alloc list global` shows the child pool's carve-out:

```text
ID                                          CIDR         CREATED_AT                   DESCRIPTION  LABELS
pool:prod:f645550f4061e0b075ad1aab5ac620b8  10.0.0.0/16  2026-10-30T03:10:23.260011Z  -            env=prod
```

Idempotent creates with a caller-chosen ID, metadata updates and teardown:

```sh
ipam alloc create prod --prefix-length 24 --id 018f22e2-1865-7be8-9f45-35d90607d0f7
ipam alloc create prod --prefix-length 24 --id 018f22e2-1865-7be8-9f45-35d90607d0f7
ipam alloc update prod 018f22e2-1865-7be8-9f45-35d90607d0f7 --label team=web
ipam alloc get prod 018f22e2-1865-7be8-9f45-35d90607d0f7 -o json
ipam pool update prod --description 'production'
ipam pool get prod
```

The second `alloc create` returns the same CIDR as the first and writes
nothing. [Cleaning up](#cleaning-up) shows how to tear the example down.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success. |
| 1 | Any other error (storage, network, access denied). |
| 2 | Usage errors (unknown command or flag), invalid arguments and CIDRs, and backend setup that fails validation at construction, such as a missing credential, a missing region or a malformed backend URL. |
| 3 | Not found (`ErrNotFound`). |
| 4 | Conflict (`ErrConflict`): overlap, ID reuse, pool not empty, pool exists, contention. |
| 5 | Pool exhausted (`ErrPoolExhausted`). |
| 6 | `ipam doctor` found at least one failed check. |

Credential or permission failures that happen at run time, after the backend
was constructed (for example a wrong secret or an access-denied answer from
the store), are not usage errors: they exit 1 as access denied.

Errors go to stderr as `ipam: <message>`. The message carries the reason, for
example `delete pool prod (pool_not_empty): ipam: conflict`.

### ipam doctor

`ipam doctor` checks that a backend is usable and safe, and never creates,
deletes or modifies data:

| Check | PASS | WARN | FAIL |
| --- | --- | --- | --- |
| `backend` | `pools/` is listable and a pool document readable | | listing or reading fails |
| `data-protection` | versioning or soft delete is on | state unknown (no permission to read it, Azurite), or a `file://` backend | both are known to be off |
| `hierarchical-namespace` | flat namespace | unknown | Azure hierarchical namespace (Data Lake Gen2) is on |
| `write-probe` (per pool) | conditional writes honoured, or write denied | reservations document missing or unreadable (probe skipped) | the backend ignored a conditional write, or an `--expect-write`/`--expect-no-write` assertion failed |
| `fragmentation` (per pool) | statistics read, index below the WARN threshold | fragmentation index >= 0.5 with capacity >= 4, or statistics unavailable (not found, unsupported schema, read error) | never |

The write probe sends a pool's own reservations bytes back with a version that
can't match. A precondition failure proves you can write and that conditions
are enforced; access denied proves you can't write. If the write succeeds, the
backend ignores conditions, which is a FAIL. The bytes were identical, so no
content changed.

`--expect-write POOL` and `--expect-no-write POOL` (both repeatable) turn a
pool's probe into an assertion. Run doctor as each identity to prove its
access. Against the local walk-through backend, where you can write
everything:

```sh
ipam doctor --expect-write prod
```

This exits 0 with `RESULT: warn`, because a `file://` backend has no
versioning. Run as an environment's deploy identity against cloud storage, the
assertion that matters is that it can write its own pool and nothing else:

```text
ipam doctor --expect-write prod --expect-no-write global
```

The same command run with the platform identity's full access FAILs on
`global` (`write allowed, but --expect-no-write was given`) and exits 6.

The `fragmentation` check only reads. It's described in
[Monitoring](#monitoring). A fragmentation WARN makes the overall result
`warn`, which still exits 0. Free-space metrics ignore cooling ranges;
strict cooldown can refuse a request even when allocatable > 0. The active
cooling range count is reported separately.

**Limit:** doctor can't detect a backend that ignores conditional DELETE,
because probing that would delete data. It only covers conditional writes.

## Upgrading

Upgrade every client that reads the bucket (library users, the CLI, the
Terraform provider) before you create a pool with a strategy other than
`lowest` or with a cooldown. Until then, create pools with
`--strategy lowest` and no cooldown: their documents stay v1, and older
clients keep working with them.

This matters because the default `best-fit` strategy writes a v2 pool
document, and older clients refuse v2:

- v0.2.0 `ipam pool list` fails on a bucket that holds a v2 pool. New clients
  skip such a pool and warn instead (see [CLI](#cli)).
- A v0.2.0 client that deletes a child of a v2 parent leaks the child's
  carve-out in the parent. The space stays reserved and nothing overlaps.
  Reclaim it with a new client:

  ```text
  ipam alloc release <parent> pool:<child>:<incarnation>
  ```

  Read [Recovering a leaked allocation](#recovering-a-leaked-allocation) first.

## Allocation strategy and fragmentation

Fragmentation is free space broken into pieces too small for the requests you
make. A pool can have plenty of free addresses in total and still fail a
`/24` request because no single aligned `/24` is free.

`lowest` puts each allocation at the lowest free address that fits. It's
predictable, but releases leave holes that later requests of other sizes
split up. `best-fit` puts each allocation in the smallest free block that
fits (lowest address on a tie), which keeps large blocks whole for large
requests. In the committed simulation test
(`TestSimulationBestFitNotWorseThanLowest`, 16 mixed-size churn scenarios)
`lowest` had 20 failed requests caused by fragmentation and `best-fit` had 6,
and at the end `best-fit` left room for 21568 `/24`s against 21441 for
`lowest`. Those are totals over the scenarios. They don't promise that
`best-fit` wins on every workload.

Either strategy works best when you shape the requests:

- Segregate sizes into child pools. For example, carve a `/20` child pool
  that only ever hands out `/28`s.
- Standardise request sizes, so freed space fits the next request.
- Put explicit reservations for legacy space at the edges of a range, not in
  the middle.
- Prefer one large CIDR per pool over many small ones.
- For IPv6, use uniform `/64` or `/56` sizes.

Change the strategy of an existing pool with
`ipam pool update <id> --strategy best-fit`. Only later allocations are
affected.

### Cooldown

A cooldown holds a released CIDR back for a while, so that a stale reference
(a DNS record, a firewall rule) doesn't hit a new owner straight away. There
are two modes:

- `strict` never hands out space that's still cooling. That's a guarantee,
  and it means an allocation can be refused while the pool has free space.
  The refusal is `ErrPoolExhausted` (exit 5) with reason `cooldown`, and the
  message names the time when cooling space becomes usable.
- `fit-aware` avoids cooling space only when an equally tight free block
  exists. It costs nothing in fragmentation, but the delay is weak: when the
  only good fit is cooling, it's reused.

A strict refusal from the CLI:

```sh
ipam pool create edge --cidr 10.9.0.0/24 --cooldown 1h --cooldown-mode strict
ipam alloc create edge --prefix-length 24 --id 018f22e2-1865-7be8-9f45-35d90607d0f7
ipam alloc release edge 018f22e2-1865-7be8-9f45-35d90607d0f7
ipam alloc create edge --prefix-length 24
```

The last command exits 5:

```text
ipam: allocate pool edge allocation 01a18ed5-9aeb-7d6d-ba04-b409d54946f3 (cooldown): only cooling space fits until 2026-10-30T23:02:06Z: ipam: pool exhausted
```

Switching to `fit-aware` lets the same request reuse the cooling `/24`:

```sh
ipam pool update edge --cooldown 24h --cooldown-mode fit-aware
ipam alloc create edge --prefix-length 24
```

Pick a duration that covers how long stale references to an address live in
your environment, such as DNS TTLs and the time to clean up rules that point
at it. Durations are whole seconds, at most `8760h` (one year). Keep them
short where you can, for two reasons:

- Every release adds a record to the pool's reservations document, and the
  record stays until its cooldown ends. A long cooldown on a busy pool makes
  that document bigger, and every write to the pool rewrites it.
- Release and expiry times come from the clock of the client that writes the
  document. Clock skew between clients shifts when cooling space becomes
  usable.

`ipam pool update <id> --no-cooldown` turns the cooldown off and drops every
release record.

`UpdatePoolPolicy` (and so `pool update` with policy flags) makes up to two
writes, the strategy in the pool document and then the cooldown in the
reservations document. They aren't atomic. If the second fails, the error says
so and the pool keeps the new strategy with the old cooldown; repeating the
same update completes it.

### Monitoring

`ipam doctor` reports a `fragmentation` row per pool, for example:

```text
fragmentation           net     PASS    util 0.0%, free 65536 of 65536, largest /16, blocks /16:1
```

The detail lists utilisation, free addresses, the largest free block and the
free blocks by prefix length. When the pool has allocations, it adds the
pool's most requested prefix length, how many of those are allocatable
against how many would fit if the free space were packed (capacity), and the
fragmentation index. With `-o json` the same numbers are in `stats`:
`utilization`, `total`, `free`, `allocatable`, `capacity`,
`fragmentation_index`, `largest_free`, `free_blocks`, `reference_length`,
`strategy` and `cooldown`.

Free space, allocatable, capacity and the fragmentation index ignore cooling
ranges: they measure fragmentation, not immediate availability under cooldown.
Strict cooldown may refuse a request even when allocatable > 0. The detail
appends `cooling N` when N > 0, counting active cooling ranges separately.

The check WARNs when the fragmentation index is 0.5 or more and the capacity
is at least 4, or when the statistics can't be read. It never FAILs, and a
WARN-only run exits 0, so alert on the `warn` result rather than the exit
code.

## Library usage

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"time"

	"github.com/appvia/ipam"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Use your backend URL here, for example s3://acme-ipam/prod?region=eu-west-2.
	client, err := ipam.Open(ctx, "mem://", ipam.WithMaxAttempts(0))
	if err != nil {
		log.Fatal(err)
	}

	if _, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:    "prod",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
	}); err != nil {
		log.Fatal(err)
	}

	a, err := client.Allocate(ctx, "prod", ipam.AllocationSpec{PrefixLength: 24, Description: "app"})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(a.CIDR)
}
```

This prints `10.0.0.0/24`.

Runnable versions of this and more live in [example_test.go](example_test.go)
(`ExampleOpen`, `ExampleClient_Allocate`, `ExampleClient_CreatePool_child`,
`ExampleClient_Release`, `ExampleWithMaxAttempts`); `go test -run Example .`
runs them.

The client API:

- `ipam.Open(ctx, url, opts...)` and `ipam.OpenStorage(ctx, url)` build a
  backend from a URL. `ipam.New(store, opts...)` wraps a backend you built
  yourself.
- Pools: `CreatePool`, `GetPool`, `ListPools`, `UpdatePoolMetadata`,
  `UpdatePoolPolicy`, `PoolStats`, `DeletePool`. `ListPools` skips pools with
  an unsupported schema: it returns the readable pools together with an
  `*ipam.UnsupportedPoolsError` that names the skipped ones.
- Allocations: `Allocate`, `GetAllocation`, `ListAllocations`,
  `UpdateAllocationMetadata`, `Release`.
- `ipam.NewAllocationID()` returns a fresh UUIDv7, for callers that want to
  record the ID before they call `Allocate`.
- Options: `WithMaxAttempts`, `WithBackoff`, `WithClock`, `WithRand`,
  `WithSleeper`, `WithIDGenerator`.

Errors wrap the sentinels `ErrNotFound`, `ErrConflict`, `ErrPoolExhausted`,
`ErrInvalidCIDR`, `ErrInvalidArgument` and `ErrUnsupportedSchema`; test them
with `errors.Is`. Most come as an `*ipam.OpError` whose `Reason` names the
cause: `pool_exists`, `pool_incomplete`, `pool_create_raced`,
`pool_not_empty`, `pool_in_use`, `id_reused`, `overlap`,
`incarnation_mismatch` or `contention`.

### Storage backends

Each backend has a typed constructor, for callers that don't want URLs:

- `s3.New(ctx, s3.Config{Bucket, Prefix, Region, Endpoint, UsePathStyle})`
  from `github.com/appvia/ipam/storage/s3`. Makes no network calls.
- `azblob.New(ctx, azblob.Config{AccountURL, Container, Prefix, Credential})`
  from `github.com/appvia/ipam/storage/azblob`. Build the credential with
  `azblob.AuthConfigFromEnv()` (the `ARM_*` variables), optionally
  `.Merge(override)`, then `.Credential(logger)`. Makes no network calls;
  tokens are fetched on first use.
- `local.New(dir)` from `github.com/appvia/ipam/storage/local`. Uses a lock
  file and atomic renames; the directory must be on a local disk, not a
  network file system.
- `memory.New()` from `github.com/appvia/ipam/storage/memory`, for tests.

All of them implement `storage.Storage` (`Get`, `CreateIfAbsent`,
`PutIfMatch`, `DeleteIfMatch`, `List`). `storagetest.Run` is the contract
suite a new backend must pass.

`storage.WithAccessPropagationRetry(ctx)` marks a context so that the S3 and
Azure backends retry access-denied responses for a while, to ride out a role
assignment that hasn't propagated yet. Use it for writes right after granting
access; never use it for reads, which should fail fast.

## Concurrency model

Every write is conditional. A new document is written only if absent
(`If-None-Match: *`); an update or delete is sent with the ETag that was read
(`If-Match`). When another writer got there first, the client re-reads and
tries again with full-jitter exponential backoff (10ms base, 250ms cap by
default, `WithBackoff` to change it). Locks and leases aren't used. A
`storage.ErrUnknownOutcome` (a timeout or reset after the request may have
been applied) is treated like a conflict: the client re-reads, and the
idempotent allocation ID turns a write that did land into a success.

By default the client tries 8 times and then returns `ErrConflict` with
reason `contention`. `WithMaxAttempts(0)` retries until the context ends.

> **Warning: long-lived callers must use `WithMaxAttempts(0)` with a context
> deadline.** Two callers creating the same pool with the same spec at the same
> time converge (the loser returns the winner's pool) only if the winner
> finishes within the loser's retry budget. With the default 8 attempts a slow
> backend can make the loser return a temporary `pool_incomplete` conflict;
> retrying succeeds. The Terraform provider, and any service that must not fail
> on contention, should pass `WithMaxAttempts(0)` and bound the call with
> `context.WithTimeout`.

All allocations of a pool live in one document, so every writer to a pool
contends on one ETag, and every conflict costs a re-read and a backoff. If a
pool is hot (many pipelines allocating at once), split
it into child pools, for example one per region or environment, so writers
spread across documents. Within one process the client also serialises writes
to the same pool, which removes self-contention.

Child-pool creation writes three documents in order: the carve-out in the
parent's reservations, the child's reservations document, then the child's
pool document. If a process stops part-way, `CreatePool` for that ID returns
`ErrConflict` with reason `pool_incomplete`, and `DeletePool` clears the
residue. Residue never causes an overlap.

## Access model

Two kinds of identity use the store:

- The **platform identity** owns the pool hierarchy. It writes and deletes
  `pools/*` and `reservations/*`, and lists the bucket.
- Each **environment's deploy identity** allocates. It reads `pools/*` and
  `reservations/*`, and may only overwrite `reservations/<pool>.json` for its
  own environment's pools. It never deletes: releasing an allocation is an
  overwrite. An environment with no pools of its own is read-only.

Required permissions:

| Role | Needs |
| --- | --- |
| Platform | read, write and delete on `pools/*` and `reservations/*`; list the bucket or container |
| Environment | read on `pools/*` and `reservations/*`; overwrite (no delete) on its own `reservations/<pool>.json`; unconditional `s3:ListBucket` on AWS |
| Reader | read on `pools/*` and `reservations/*`; list |

On S3 the environment identity needs `s3:ListBucket` without a prefix
condition. S3 answers a request for a missing key with 404 only to callers
that can list the bucket, and with 403 otherwise; the client relies on 404 to
tell a deleted pool from a denied one. `s3:GetBucketVersioning` lets
`ipam doctor` confirm versioning.

### AWS IAM policy for an environment

This is the policy `terraform-aws-ipam` attaches to an environment's roles,
for bucket `acme-ipam` and pools `uks-prod` and `ukw-prod`. Add
`kms:Decrypt` and `kms:GenerateDataKey` on the key if the bucket uses SSE-KMS.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ReadDocuments",
      "Effect": "Allow",
      "Action": ["s3:GetObject"],
      "Resource": [
        "arn:aws:s3:::acme-ipam/pools/*",
        "arn:aws:s3:::acme-ipam/reservations/*"
      ]
    },
    {
      "Sid": "WriteOwnReservations",
      "Effect": "Allow",
      "Action": ["s3:PutObject"],
      "Resource": [
        "arn:aws:s3:::acme-ipam/reservations/uks-prod.json",
        "arn:aws:s3:::acme-ipam/reservations/ukw-prod.json"
      ]
    },
    {
      "Sid": "BucketRead",
      "Effect": "Allow",
      "Action": ["s3:ListBucket", "s3:GetBucketVersioning"],
      "Resource": ["arn:aws:s3:::acme-ipam"]
    }
  ]
}
```

The platform policy grants `s3:GetObject`, `s3:PutObject` and
`s3:DeleteObject` on `pools/*` and `reservations/*`, plus the same
`BucketRead` statement.

### Azure role assignment for an environment

Environment identities get two assignments at container scope. One is
`Storage Blob Data Reader`. The other carries a version 2.0 condition that
allows `blobs/write` only on the environment's own reservations documents.
`terraform-azurerm-ipam` offers two ways to grant it, chosen by
`environment_role`:

- `builtin` (the default): the built-in `Storage Blob Data Contributor` role.
  The condition below is extended with clauses that deny `blobs/delete`,
  `blobs/add/action` and `blobs/move/action`. That role also includes
  management-plane actions such as deleting or modifying the container, which
  conditions can't restrict. The module puts a `CanNotDelete` lock on the
  storage account, which blocks container deletion through ARM, and soft delete
  covers the rest.
- `custom`: a data-actions-only custom role holding just
  `Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read` and
  `.../blobs/write` (no delete, no add, no management-plane actions).

This is the condition the module renders in `custom` mode for an environment
that owns `uks-prod` and `ukw-prod`:

```text
(
 (
  !(ActionMatches{'Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write'})
 )
 OR
 (
  @Resource[Microsoft.Storage/storageAccounts/blobServices/containers/blobs:path] StringEquals 'reservations/uks-prod.json'
  OR
  @Resource[Microsoft.Storage/storageAccounts/blobServices/containers/blobs:path] StringEquals 'reservations/ukw-prod.json'
 )
)
```

Don't give environment identities the built-in `Contributor` role. It
includes management-plane actions such as deleting the container. The
platform identity gets `Storage Blob Data Contributor` at container scope
without a condition.

The storage account must **not** have hierarchical namespace (Data Lake Gen2)
enabled. A rename there can move a blob past the path conditions above.
`ipam doctor` fails on such an account.

### Recovery

Turn on bucket versioning (S3) or blob versioning plus blob and container soft
delete (Azure). The documents are the only record of what's allocated; with
versioning, an overwritten or deleted document can be restored. Both
Terraform modules enable this by default, and `ipam doctor` reports it.

## Recovering a leaked allocation

An allocation can leak if a caller crashes after the write lands but before it
records the ID, for example Terraform killed between the write and saving
state. Find it, then either adopt it or release it:

```sh
ipam alloc list prod
```

Adopt it into Terraform state with the pool and ID:

```text
terraform import ipam_allocation.app prod/018f22e2-1865-7be8-9f45-35d90607d0f7
```

or release it:

```sh
ipam alloc release prod 018f22e2-1865-7be8-9f45-35d90607d0f7
```

A child-pool carve-out `pool:<child>:<incarnation>` can also leak, when a pool
create or delete is interrupted. `DeletePool` never scans for these. It's
safe to leave one in place, since the space just stays reserved. To reclaim
it, release it by its full ID with `ipam alloc release <parent> pool:<child>:<incarnation>`.
The release is refused with `pool_in_use` while that child incarnation still
has a pool or reservations document.

> **Warning: releasing a `pool:<child>:<incarnation>` carve-out is unsafe while
> a `CreatePool` for that child may still be running anywhere.** A creator that
> has committed the carve-out but not yet written the child's reservations
> document can't be detected. It would publish the child pool on the released
> space, which the parent can then hand out again, giving overlapping
> networks. Release a leaked carve-out only once you're sure no create for that
> child is in flight.

## Testing and verification status

`make test` runs the unit suite with the race detector. `make lint` runs
golangci-lint. The backends also have integration tests against emulators
(Azurite and an S3-compatible server; see `scripts/emulators`). They're behind
the `integration` build tag (`make test-integration`) and skip themselves
unless their `IPAM_TEST_*` variables are set; each integration test file lists
the ones it reads.

> **Warning: conditional DELETE on real S3 is unproven by the test suite.** The
> emulators don't reliably enforce `If-Match` on `DeleteObject`, so the S3
> integration tests skip that case there
> (`IPAM_TEST_S3_SKIP_CONDITIONAL_DELETE=1`). It's exercised only against real
> AWS with `IPAM_TEST_S3_BUCKET` set. `ipam doctor` can't check it either.

> **Warning: real-cloud access control and Azure credentials are unverified.**
> The IAM policy and Azure role assignments in [Access model](#access-model)
> were tested only under MinIO's IAM engine and as rendered text (the Terraform
> modules' plan-mode `terraform test` runs). Enforcement by real AWS IAM and by
> real Azure RBAC and ABAC conditions hasn't been tested. Neither has the Azure
> credential path (aztfauth with `ARM_*`) against a real storage account,
> because Azurite uses a connection string. Grant enforcement is checked only
> by applying the module and running `ipam doctor --expect-write <own pool>
> --expect-no-write <other pool>` as each identity. The provider repository's
> `scripts/e2e.sh` real-cloud legs (`IPAM_TEST_S3_BUCKET` or
> `AZURE_STORAGE_ACCOUNT_URL`) use the caller's own credentials. They prove
> the real credential path and real conditional PUTs, not the module grants.
> Conditional DELETE on real S3 is covered by the integration run above.

## Cleaning up

A pool can only be deleted once it's empty, and a parent only once its child
pools are gone. To tear down the walk-through above:

```sh
for id in $(ipam alloc list prod -o json | jq -r '.[].id'); do
  ipam alloc release prod "$id"
done
ipam pool delete prod
ipam pool delete global
```

## License

Apache 2.0. See [LICENSE](LICENSE).
