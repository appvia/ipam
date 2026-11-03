# Feature Specification: IPAM command-line interface

**Requirement**: Baseline (no Requirement issue)
**Spec path**: `specs/BASE-0014-cli/spec.md`
**Created**: 2026-11-03
**Status**: Draft
**Input**: Existing code at appvia/ipam@3833bccf042b01540a5ab4ea002e8ee73c7f19b7

## User journeys *(mandatory)*

### Journey 1 - Manage pools and allocations (Priority: P1)

A CLI operator selects a storage backend, creates a root pool or a child pool, allocates a CIDR, and inspects the results. The operator can update descriptions and labels, release allocations, and delete empty pools. Human-readable tables are the default output.

**Why this priority**: These commands expose the library's ordinary pool and allocation operations to operators without requiring a Go program.

### Journey 2 - Automate repeatable operations (Priority: P1)

An automation author supplies the backend through an environment variable or flag, requests JSON, and uses the process exit status to distinguish invalid input, missing records, conflicts, exhaustion, and other failures. Supplying an allocation ID allows a create request to be repeated without creating another allocation.

**Why this priority**: Scripts need structured results and stable failure categories to decide whether an operation completed or needs attention.

### Journey 3 - Configure pool policy (Priority: P2)

A CLI operator selects a pool's allocation strategy and optional release cooldown when creating a pool, or changes the policy of an existing pool. The operator inspects the resulting policy in JSON or table output.

**Why this priority**: Policy configuration controls how subsequent allocations use the pool. The policy's allocation semantics belong to BASE-0002 allocation-policy.

### Journey 4 - Discover commands and identify the binary (Priority: P2)

A CLI user reads command help or prints the CLI version before configuring a backend.

**Why this priority**: Help and version information support initial use and diagnosis without access to stored pool data.

## Acceptance criteria *(mandatory)*

- **AC-1**: **Given** a command that accesses pool or allocation data, **When** the operator selects a backend, **Then** a nonempty `--backend` value takes precedence over `IPAM_BACKEND`; otherwise the environment variable supplies the backend. Backend URL interpretation belongs to BASE-0009 backend-url.
  - Evidence: `cmd/ipam/main_test.go` `TestBackendSelection`; `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`

- **AC-2**: **Given** a valid backend and an unused pool ID, **When** the operator runs `pool create <id>` with one or more repeatable `--cidr` flags and optional `--description` and `--label key=value` flags, **Then** the command creates a root pool and displays its CIDRs and metadata. Pool creation rules belong to BASE-0001 pool-lifecycle.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`; `cmd/ipam/main_test.go` `TestTableOutput`

- **AC-3**: **Given** an existing parent pool with suitable free space, **When** the operator runs `pool create <id> --parent <pool>` with either `--prefix-length N` or one explicit `--cidr`, **Then** the command displays the created child's CIDR and, in JSON, a `parent` object containing `pool_id` and `allocation_id`. Child-pool lifecycle rules belong to BASE-0001 pool-lifecycle.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`

- **AC-4**: **Given** existing pools, **When** the operator runs `pool get <id>` or `pool list`, **Then** the command displays the selected pool or the available pool collection, respectively.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`

- **AC-5**: **Given** a pool with labels, **When** the operator runs `pool update <id> --description <text>` without a label flag, **Then** the command displays the updated description and preserves the existing labels.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`

- **AC-6**: **Given** an empty pool that can be deleted under BASE-0001 pool-lifecycle, **When** the operator runs `pool delete <id>`, **Then** the command succeeds and the pool no longer appears in `pool list`; JSON acknowledgement output has the keys `id` and `deleted`.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`

- **AC-7**: **Given** an existing pool, **When** the operator runs `alloc create <pool>` with either `--prefix-length N` or `--cidr CIDR`, **Then** the command displays the allocation, and in JSON output its `request.kind` is `next` or `explicit`, respectively; when `--id` is omitted, the displayed allocation ID is a canonical UUIDv7. Allocation selection and reservation rules belong to BASE-0003 allocations and BASE-0002 allocation-policy.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`

- **AC-8**: **Given** an allocation created using `alloc create --id <UUID>`, **When** the operator repeats the same ID and request, **Then** the command returns the same ID and CIDR; reusing that ID with a different request exits with status `4` and an `id_reused` diagnostic.
  - Evidence: `cmd/ipam/main_test.go` `TestSuppliedIDIsIdempotent`

- **AC-9**: **Given** existing allocations in a pool, **When** the operator runs `alloc get <pool> <id>` or `alloc list <pool>`, **Then** the command displays the selected allocation or the pool's allocation collection, respectively.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`

- **AC-10**: **Given** an existing allocation, **When** the operator runs `alloc update <pool> <id>` with description and label flags, **Then** the displayed allocation contains the updated metadata without changing its CIDR; a subsequent labels-only update preserves its description.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`

- **AC-11**: **Given** an existing ordinary allocation, **When** the operator runs `alloc release <pool> <id>`, **Then** the command succeeds, JSON acknowledgement output has the keys `pool_id`, `id`, and `released`, and a subsequent `alloc get` exits with status `3`. Release semantics belong to BASE-0003 allocations.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file`

- **AC-12**: **Given** valid pool creation arguments without policy flags, **When** the operator creates a pool with JSON output, **Then** its `strategy` is `best-fit` and its output omits `cooldown`.
  - Evidence: `cmd/ipam/pool_policy_test.go` `TestPoolCreateDefaultStrategyIsBestFit`

- **AC-13**: **Given** valid pool creation arguments, **When** the operator also supplies `--strategy lowest --cooldown 1h --cooldown-mode strict`, **Then** create and get JSON output show `strategy: lowest` and a cooldown with `mode: strict` and `duration: 1h0m0s`; table output exposes the strategy and cooldown columns and the cooldown's mode and duration.
  - Evidence: `cmd/ipam/pool_policy_test.go` `TestPoolCreateWithPolicy`

- **AC-14**: **Given** an existing pool with a cooldown, **When** the operator updates only its strategy, replaces its cooldown alongside a description, or passes `--no-cooldown`, **Then** the displayed result reflects the requested policy change: a strategy-only update retains the cooldown mode, a cooldown update retains the strategy, and `--no-cooldown` removes the cooldown from JSON output.
  - Evidence: `cmd/ipam/pool_policy_test.go` `TestPoolUpdatePolicy`

- **AC-15**: **Given** successful create, get, update, or list commands with `-o json`, **When** the command displays records, **Then** individual records are JSON objects and lists are JSON arrays. Pool objects without a cooldown have the keys `id`, `family`, `cidrs`, `description`, `labels`, and `strategy`, with `parent` additionally present for child pools; `family` is the JSON number `4` or `6`. Allocation objects have the keys `id`, `pool_id`, `cidr`, `request`, `description`, `labels`, and `created_at`; an unlabelled pool or allocation has `labels: {}`, and `created_at` is an RFC3339-compatible timestamp.
  - Evidence: `cmd/ipam/main_test.go` `TestEverySubcommandJSON/mem` and `TestEverySubcommandJSON/file` (IPv4); UNTESTED (unlabelled pool `labels` and IPv6 `family` value; behaviour in `cmd/ipam/output.go` `poolJSON` and `toPoolJSON`)

- **AC-16**: **Given** no pools, or an existing pool with no allocations, **When** the operator lists the corresponding collection with JSON output, **Then** stdout contains `[]`, not `null`.
  - Evidence: `cmd/ipam/main_test.go` `TestEmptyListsAreJSONArrays`

- **AC-17**: **Given** successful pool or allocation commands without an output flag, **When** records are displayed, **Then** the output is a table: pool output includes comma-separated CIDRs and `key=value` labels, and allocation output exposes the ID, CIDR, creation time, description, and labels columns.
  - Evidence: `cmd/ipam/main_test.go` `TestTableOutput`

- **AC-18**: **Given** no backend configuration, **When** the user runs `version`, **Then** the command succeeds and prints the CLI version; `version -o json` succeeds and emits an object whose sole key is `version`.
  - Evidence: `cmd/ipam/main_test.go` `TestVersionNeedsNoBackend`

- **AC-19**: **Given** no backend configuration, **When** the user runs `--help`, the bare `pool` command, or `alloc release --help`, **Then** the command prints help to stdout and exits with status `0`.
  - Evidence: `cmd/ipam/main_test.go` `TestHelpExitsZero`

- **AC-20**: **Given** a backend containing readable pools and pools with unsupported schemas, **When** the operator runs `pool list` in JSON or table mode, **Then** the command displays the readable pools, exits with status `0`, and writes a warning to stderr identifying skipped pool IDs and advising an upgrade.
  - Evidence: `cmd/ipam/pool_policy_test.go` `TestPoolListWarnsAboutUnsupportedPools`

- **AC-21**: **Given** an allocation command, **When** the operator uses `allocation` in place of `alloc`, **Then** the same allocation subcommands and flags are available.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/commands.go` `allocCommand`)

- **AC-22**: **Given** an existing pool or allocation with metadata, **When** the operator updates it, **Then** omitted metadata flags preserve their current values, any supplied `--label` flags replace the whole label set rather than merging it, and an explicitly empty `--description` clears the description.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/commands.go` `metadataFlags.merge`, `poolUpdateCommand`, and `allocUpdateCommand`)

- **AC-23**: **Given** a create or metadata-update command, **When** the operator supplies repeatable `--label key=value` flags with distinct nonempty keys, **Then** each label is split at the first `=`; empty values and further `=` characters in a value are accepted.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/commands.go` `parseLabels`)

- **AC-24**: **Given** table output, **When** pools or allocations are displayed, **Then** pool columns are `ID`, `FAMILY`, `CIDRS`, `PARENT`, `STRATEGY`, `COOLDOWN`, `DESCRIPTION`, and `LABELS`, in that order, and allocation columns are `ID`, `CIDR`, `CREATED_AT`, `DESCRIPTION`, and `LABELS`. Pool family is displayed as `IPv4` or `IPv6`; absent parents, cooldowns, descriptions, and label sets display as `-`; labels are displayed in ascending key order, separated by commas.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/output.go` `printPools`, `printAllocations`, `formatLabels`, and `dash`)

- **AC-25**: **Given** allocation JSON output, **When** the allocation is displayed, **Then** a `next` request contains `kind` and `prefix_length` without `cidr`, an `explicit` request contains `kind` and `cidr` without `prefix_length`, and `created_at` is formatted in UTC using RFC3339Nano.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/output.go` `toAllocationJSON`)

- **AC-26**: **Given** a successful pool deletion or allocation release, **When** acknowledgement output is emitted, **Then** JSON contains the supplied identifying arguments and `deleted: true` or `released: true`, respectively. Table mode instead prints `deleted pool <id>` or `released allocation <id> from pool <pool>`. The release acknowledgement echoes the supplied arguments and does not confirm prior existence: it is also emitted when the supplied ID was not in the pool. Release idempotence belongs to BASE-0003 allocations.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/commands.go` `poolCommand` and `allocCommand`)

- **AC-27**: **Given** a build without an injected version, **When** the user runs `version`, **Then** it reports `dev`; a version supplied at build time through `-ldflags "-X main.version=..."` is the reported version instead.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/main.go` `version` and `cmd/ipam/commands.go` `versionCommand`)

- **AC-28**: **Given** a `pool update` request containing both policy and metadata changes, **When** the metadata update fails, **Then** the command reports the failure.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/commands.go` `poolUpdateCommand`)

### Edge cases

- **AC-29**: **Given** a command that needs a backend, **When** neither a nonempty `--backend` nor `IPAM_BACKEND` is available, **Then** it exits with status `2` and a diagnostic mentioning `IPAM_BACKEND`; an unsupported backend scheme also exits with status `2`.
  - Evidence: `cmd/ipam/main_test.go` `TestBackendSelection`

- **AC-30**: **Given** an executable command, **When** the operator selects an output format other than `json` or `table` through `--output` or `-o`, **Then** the command exits with status `2` and an output-format diagnostic. Explicit help, bare root, and bare command-group invocations ignore the output flag, print help, and exit with status `0` instead.
  - Evidence: `cmd/ipam/main_test.go` `TestExitCodes/bad output`; UNTESTED (invalid output flags with help or bare root/group invocations; behaviour in `cmd/ipam/commands.go` `leaf`, `rootCommand`, and `groupRun`)

- **AC-31**: **Given** an unknown command, unknown subcommand, unknown flag, missing required positional argument, or extra positional argument, **When** the invocation is parsed, **Then** the CLI writes a diagnostic to stderr and exits with status `2`.
  - Evidence: `cmd/ipam/main_test.go` `TestExitCodes/unknown command`, `TestExitCodes/unknown subcommand`, `TestExitCodes/unknown flag`, `TestExitCodes/missing args`, and `TestExitCodes/extra args`

- **AC-32**: **Given** a pool-create invocation, **When** a root pool has no CIDR or is given `--prefix-length`, or a child request combines a prefix length with a CIDR or supplies multiple CIDRs, **Then** the CLI rejects the invocation with status `2`.
  - Evidence: `cmd/ipam/main_test.go` `TestExitCodes/no cidr`, `TestExitCodes/prefix without parent`, `TestExitCodes/root cidr with prefix`, `TestExitCodes/cidr and parent prefix`, and `TestExitCodes/two child cidrs`

- **AC-33**: **Given** an allocation-create invocation, **When** both or neither of `--prefix-length` and `--cidr` are supplied, or the CIDR cannot be parsed, **Then** the CLI rejects the invocation with status `2`.
  - Evidence: `cmd/ipam/main_test.go` `TestExitCodes/alloc both`, `TestExitCodes/alloc neither`, and `TestExitCodes/bad cidr`

- **AC-34**: **Given** a command accepting labels, **When** a label lacks `=` or a key is repeated, **Then** the CLI exits with status `2` and a label diagnostic.
  - Evidence: `cmd/ipam/main_test.go` `TestExitCodes/bad label` and `TestExitCodes/duplicate label`

- **AC-35**: **Given** a command accepting labels, **When** a label has an empty key, **Then** the CLI rejects it with status `2`.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/commands.go` `parseLabels`)

- **AC-36**: **Given** a pool or allocation update invocation, **When** the operator supplies no change flags, **Then** the CLI rejects the invocation with status `2` and a diagnostic.
  - Evidence: `cmd/ipam/pool_policy_test.go` `TestPoolPolicyFlagErrors/update nothing`; `cmd/ipam/main_test.go` `TestExitCodes/update nothing`

- **AC-37**: **Given** a pool creation or policy-update invocation, **When** the operator supplies an unknown or empty strategy, only one of `--cooldown` and `--cooldown-mode`, an invalid cooldown mode, an unparsable, zero, fractional-second, negative, or excessive cooldown duration, or `--no-cooldown` together with cooldown-setting flags, **Then** the CLI rejects the invalid request with status `2` and a diagnostic. The rejected creates do not create the requested pool, and the rejected updates in these cases leave the existing pool's strategy unchanged.
  - Evidence: `cmd/ipam/pool_policy_test.go` `TestPoolPolicyFlagErrors` (subtests `create cooldown without mode`, `create mode without cooldown`, `create bad strategy`, `create empty strategy`, `create bad mode`, `create bad duration`, `create zero duration`, `create huge duration`, `update cooldown without mode`, `update bad strategy`, `update no-cooldown with cooldown`, and `update no-cooldown with mode`, plus the enclosing postcondition checks); UNTESTED (fractional-second and negative durations; behaviour in `cmd/ipam/commands.go` `policyFlags.parseCooldown`, `cmd/ipam/main.go` `exitCode`, and `validate.go` `validateCooldown`)

- **AC-38**: **Given** input rejected by the library as `ErrInvalidArgument` or `ErrInvalidCIDR`, **When** the CLI executes the operation, **Then** it exits with status `2` and prints an error. This includes malformed pool IDs, invalid allocation IDs, an explicit CIDR outside a pool, and a requested prefix length beyond the address family. Library input validation belongs to BASE-0005 input-validation, operation semantics to BASE-0001 pool-lifecycle and BASE-0003 allocations, and error semantics to BASE-0006 error-contract.
  - Evidence: `cmd/ipam/main_test.go` `TestLibraryValidationErrorsExitTwo`; `cmd/ipam/main_test.go` `TestExitCodes/bad pool id` and `TestExitCodes/reserved id`

- **AC-39**: **Given** a pool or allocation that does not exist, **When** the operator requests it, **Then** the CLI exits with status `3` and a not-found diagnostic.
  - Evidence: `cmd/ipam/main_test.go` `TestExitCodes/pool not found` and `TestExitCodes/alloc not found`

- **AC-40**: **Given** an operation refused because a pool is not empty, a different pool already occupies the requested ID, or an allocation overlaps an existing reservation, **When** the CLI executes the operation, **Then** it exits with status `4` and exposes the corresponding `pool_not_empty`, `pool_exists`, or `overlap` reason.
  - Evidence: `cmd/ipam/main_test.go` `TestExitCodes/pool not empty`, `TestExitCodes/pool exists`, and `TestExitCodes/overlap`

- **AC-41**: **Given** a pool that cannot satisfy an allocation, **When** the operator attempts to allocate, **Then** the CLI exits with status `5`; when strict cooldown prevents reuse, the diagnostic includes `(cooldown)` and indicates when reuse becomes possible. Exhaustion and cooldown semantics belong to BASE-0003 allocations and BASE-0002 allocation-policy.
  - Evidence: `cmd/ipam/main_test.go` `TestExitCodes/exhausted`; `cmd/ipam/pool_policy_test.go` `TestCooldownRefusalExitsExhausted`

- **AC-42**: **Given** an operation failing for a reason outside the usage, not-found, conflict, exhaustion, or doctor-failure categories, **When** the CLI reports the error, **Then** it exits with status `1`; fetching a pool with an unsupported schema is one such failure. Document schema rules belong to BASE-0007 document-format.
  - Evidence: `cmd/ipam/main_test.go` `TestGenericErrorExitsOne`

- **AC-43**: **Given** a library error already prefixed with `ipam: `, **When** the CLI reports it to stderr, **Then** the prefix is not duplicated as `ipam: ipam: `.
  - Evidence: `cmd/ipam/errors_message_test.go` `TestStderrPrefixIsNotDoubled`

- **AC-44**: **Given** an explicit allocation outside its pool or a prefix length shorter than its pool, **When** the CLI rejects the request, **Then** it exits with status `2` and reports the requested CIDR or length together with the relevant pool and pool boundary, without the duplicated phrase `pool pool`.
  - Evidence: `cmd/ipam/errors_message_test.go` `TestInvalidCIDRReasonAndExitCode/outside` and `TestInvalidCIDRReasonAndExitCode/shorter`

- **AC-45**: **Given** an invalid backend URL containing credentials or credential-bearing query parameters, **When** the operator supplies it through either `--backend` or `IPAM_BACKEND` to a pool or allocation command, **Then** the command exits with status `2`, emits a diagnostic, and does not echo the embedded credentials to stdout or stderr. URL rejection rules belong to BASE-0009 backend-url.
  - Evidence: `cmd/ipam/main_test.go` `TestBackendSecretsAreNeverEchoed` (pool and allocation invocations through both backend sources)

- **AC-46**: **Given** a failed command, **When** its exit status is `2`, including library validation failures classified as `2` (AC-38), **Then** stderr also includes `Run 'ipam --help' for usage.`; failures classified with other exit statuses do not receive that hint.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/main.go` `runCLI`)

- **AC-47**: **Given** no backend configuration, **When** the user invokes the bare root command or bare `alloc` group, **Then** the CLI prints help and exits with status `0`.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/commands.go` `rootCommand`, `allocCommand`, and `groupRun`)

- **AC-48**: **Given** a child-pool creation request with `--parent`, **When** neither `--prefix-length` nor `--cidr` is supplied, **Then** the CLI rejects the invocation with status `2`.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/commands.go` `poolCreateCommand`)

- **AC-49**: **Given** an operation using the library's default retry budget, which the CLI has no flag to change, **When** the operation exhausts its retries under contention, **Then** the CLI exits with status `4` and a `contention` diagnostic. Retry semantics belong to BASE-0004 concurrency-and-retry.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/main.go` `openClient` and `exitCode`, and `client.go` `defaultMaxAttempts` and `contentionError`)

- **AC-50**: **Given** a persistently incomplete pool, **When** an operation other than `pool create` exhausts its retries with the `pool_incomplete` / `ErrNotFound` variant defined by BASE-0006 error-contract AC-27, **Then** the CLI exits with status `3` and a `pool_incomplete` diagnostic. Retry semantics belong to BASE-0004 concurrency-and-retry.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/main.go` `exitCode` and `client.go` `contentionError`)

- **AC-51**: **Given** a root-pool creation request or a child-pool creation request with one `--cidr`, **When** the supplied CIDR cannot be parsed, **Then** the CLI exits with status `2` and an `invalid --cidr` diagnostic before accessing the backend.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/commands.go` `poolCreateCommand` and `parseCIDR`)

- **AC-52**: **Given** an in-flight operation, **When** an interrupt (`SIGINT`) or `SIGTERM` cancels it, **Then** the CLI stops the operation and exits with status `1`.
  - Evidence: UNTESTED (behaviour in `cmd/ipam/main.go` `main` and `exitCode`, and `client.go` `waitRetry`)

- **AC-53**: **Given** a persistently incomplete pool, **When** `pool create` exhausts its retries with the `pool_incomplete` / `ErrConflict` variant defined by BASE-0006 error-contract AC-26, **Then** the CLI exits with status `4` and a `pool_incomplete` diagnostic. Retry semantics belong to BASE-0004 concurrency-and-retry.
  - Evidence: UNTESTED (error built by `pool.go` `createRetryError`; mapped to status 4 by `cmd/ipam/main.go` `exitCode`)

## Non-functional requirements *(mandatory)*

- **NFR-1**: The CLI's `--cooldown` help text states whole-second durations from **1 second** through **8760 hours**. Source: `cmd/ipam/commands.go` `policyFlags.register`. Policy validation and enforcement are owned by BASE-0002 allocation-policy.

## Key entities *(include if the feature involves data)*

- **Backend selection**: A backend URL supplied by `--backend` or `IPAM_BACKEND`. Accepted schemes, URL grammar, and credential sourcing are defined by BASE-0009 backend-url.
- **Output selection**: Global `--output` / `-o` accepts `table` or `json`, with `table` as the default. Record output goes to stdout; error and skipped-pool diagnostics go to stderr.
- **Pool result**: ID, address family, CIDRs, description, label map, allocation strategy, optional parent reference, and optional cooldown. A parent reference contains the parent pool ID and its allocation ID. A cooldown result contains `mode` and a duration string.
- **Allocation result**: Allocation ID, pool ID, CIDR, original request, description, label map, and creation timestamp. Creation accepts optional `--id`, `--description`, and repeatable `--label` flags.
- **Metadata change**: `--description` and repeatable `--label key=value` flags on create and update commands. Update flags distinguish an omitted value from an explicitly supplied value. There is no dedicated flag to replace the label set with an empty set.
- **Pool policy change**: `--strategy best-fit|lowest`, paired `--cooldown D --cooldown-mode strict|fit-aware`, and update-only `--no-cooldown`. Allocation-policy semantics are defined by BASE-0002 allocation-policy.
- **Exit status**: `0` success, `1` other error, `2` usage or argument validation, `3` not found, `4` conflict, and `5` pool exhausted. Status `6` is reserved for a failed doctor check; see BASE-0015 cli-doctor. The library's error categories are defined by BASE-0006 error-contract.
- **Version result**: Plain version text in table mode, or a JSON object with the `version` property.

The command surface is:

| Command | Positional arguments | Operation flags |
| --- | --- | --- |
| `pool create` | `<id>` | Root: repeatable `--cidr`; child: `--parent` and either `--prefix-length` or one `--cidr`; metadata and policy flags |
| `pool get` | `<id>` | None |
| `pool list` | None | None |
| `pool update` | `<id>` | Metadata flags, policy flags, `--no-cooldown` |
| `pool delete` | `<id>` | None |
| `alloc create` | `<pool>` | Exactly one of `--prefix-length` or `--cidr`; optional `--id` and metadata flags |
| `alloc get` | `<pool> <id>` | None |
| `alloc list` | `<pool>` | None |
| `alloc update` | `<pool> <id>` | Metadata flags |
| `alloc release` | `<pool> <id>` | None |
| `version` | None | None |

`allocation` is an alias for `alloc`. Global backend and output flags apply to these commands. The root and command groups expose help.

## Out of scope *(mandatory)*

- Pool storage, carve-outs, incarnation pairing, pool deletion safety, and residue handling: see BASE-0001 pool-lifecycle.
- Strategy ranking, cooldown timing, explicit requests bypassing cooldown, policy persistence, and partial updates within the library's policy operation: see BASE-0002 allocation-policy.
- Allocation reservation correctness, release idempotence, and allocation metadata persistence: see BASE-0003 allocations.
- Pool-ID and allocation-ID grammars and other library input-validation rules: see BASE-0005 input-validation.
- `alloc list` passes through the library's collection, including carve-outs as defined in BASE-0007 document-format Key entities. `alloc release` accepts the full carve-out ID through the same positional argument and delegates release safety to the library. Release help documents this recovery use and warns against releasing a carve-out while a create for that child might still be running; these lifecycle rules belong to BASE-0001 pool-lifecycle and BASE-0003 allocations.
- Library sentinel and operation-error definitions: see BASE-0006 error-contract.
- Utilisation and fragmentation statistics: see BASE-0008 pool-stats. The assigned command surface has no standalone pool-statistics command.
- Backend URL security rules and backend-specific storage guarantees: see BASE-0009 backend-url and its backend capabilities.
- `ipam doctor`, its probes, fragmentation warnings, additional flags, output, and failed-check status `6`: see BASE-0015 cli-doctor.
- Build and release automation, shell completion behaviour, and additional output formats beyond JSON and table.

## Open questions *(mandatory, empty before G2)*

- **OQ-1**: A combined `pool update` can leave a policy change applied when the metadata update fails. Maintainer answer (2026-11-03): this is a bug. A Bug citing this spec is to be raised after merge, and this entry is then removed.
