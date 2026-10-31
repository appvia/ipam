// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/appvia/ipam"
)

const releaseHelp = `Release an allocation, returning its CIDR to the pool.

Releasing an ID that is not in the pool succeeds without changes.

Child-pool carve-outs have IDs of the form pool:<child>:<incarnation>. They are
normally released by 'ipam pool delete <child>'. An operator may release a
carve-out leaked by an interrupted pool create or delete by passing its full ID.
The release is refused (pool_in_use) while that child incarnation still has a
pool or reservations document.

WARNING: releasing a pool:<child>:<incarnation> carve-out is UNSAFE while a
'pool create' for that child may still be running anywhere. A creator that has
reserved the carve-out but not yet written the child's documents cannot be
detected, and it would publish the child pool on the released space, which the
parent can then hand out again (overlapping networks). Only release a leaked
carve-out once you are sure no create for that child is in flight.`

type cli struct {
	stdout      io.Writer
	getenv      func(string) string
	open        openFunc
	openStorage openStorageFunc
	backend     string
	output      string
	started     bool
	closers     []io.Closer
}

func (c *cli) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "ipam",
		Short: "Manage IP address pools and allocations",
		Long: `ipam manages IP address pools and CIDR allocations stored as JSON documents
in a storage backend (S3, Azure Blob, or a local directory).

The backend is chosen with --backend or IPAM_BACKEND:
  s3://<bucket>/<prefix>?region=..&endpoint=..&path_style=true
  azblob://<account>.blob.core.windows.net/<container>/<prefix>
  azblob://<container>/<prefix>   (AZURE_STORAGE_CONNECTION_STRING)
  file:///absolute/path
Credentials come from the environment, never from the URL.

Exit codes: 0 ok, 1 error, 2 usage, 3 not found, 4 conflict, 5 pool exhausted,
6 'ipam doctor' found a failed check.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE:          groupRun,
	}

	root.PersistentFlags().StringVar(&c.backend, "backend", "", "backend URL (env IPAM_BACKEND)")
	root.PersistentFlags().StringVarP(&c.output, "output", "o", "table", "output format: json or table")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{msg: err.Error()} })
	root.AddCommand(c.poolCommand(), c.allocCommand(), c.doctorCommand(), c.versionCommand())

	return root
}

func groupRun(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
	}

	return cmd.Help()
}

// leaf marks the start of a command body so that errors returned from it are
// classified by their cause instead of as usage errors.
func (c *cli) leaf(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		c.started = true

		if c.output != "json" && c.output != "table" {
			return usagef("--output must be json or table, not %q", c.output)
		}

		return fn(cmd, args)
	}
}

func (c *cli) backendURL() (string, error) {
	backend := c.backend
	if backend == "" {
		backend = c.getenv("IPAM_BACKEND")
	}

	if backend == "" {
		return "", usagef("a backend is required: set --backend or IPAM_BACKEND")
	}

	return backend, nil
}

func (c *cli) client(cmd *cobra.Command) (*ipam.Client, error) {
	backend, err := c.backendURL()
	if err != nil {
		return nil, err
	}

	client, err := c.open(cmd.Context(), backend)
	if err != nil {
		return nil, err
	}

	c.closers = append(c.closers, client)

	return client, nil
}

// closeAll releases every client and store the command opened.
func (c *cli) closeAll() {
	for _, closer := range c.closers {
		_ = closer.Close()
	}
}

func (c *cli) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: c.leaf(func(_ *cobra.Command, _ []string) error {
			if c.output == "json" {
				return writeJSON(c.stdout, map[string]string{"version": version})
			}

			_, err := fmt.Fprintln(c.stdout, version)

			return err
		}),
	}
}

func (c *cli) poolCommand() *cobra.Command {
	pool := &cobra.Command{Use: "pool", Short: "Manage pools", RunE: groupRun}
	pool.AddCommand(
		c.poolCreateCommand(),
		&cobra.Command{
			Use: "get <id>", Short: "Show a pool", Args: cobra.ExactArgs(1),
			RunE: c.leaf(func(cmd *cobra.Command, args []string) error {
				client, err := c.client(cmd)
				if err != nil {
					return err
				}

				got, err := client.GetPool(cmd.Context(), args[0])
				if err != nil {
					return err
				}

				return c.printPools([]ipam.Pool{got}, true)
			}),
		},
		&cobra.Command{
			Use: "list", Short: "List pools", Args: cobra.NoArgs,
			RunE: c.leaf(func(cmd *cobra.Command, _ []string) error {
				client, err := c.client(cmd)
				if err != nil {
					return err
				}

				pools, err := client.ListPools(cmd.Context())

				var unsupported *ipam.UnsupportedPoolsError
				if errors.As(err, &unsupported) {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"ipam: warning: skipped pools with an unsupported schema (upgrade ipam): %s\n",
						strings.Join(unsupported.PoolIDs, ", "))

					err = nil
				}

				if err != nil {
					return err
				}

				return c.printPools(pools, false)
			}),
		},
		&cobra.Command{
			Use: "delete <id>", Short: "Delete an empty pool and release its parent carve-out", Args: cobra.ExactArgs(1),
			RunE: c.leaf(func(cmd *cobra.Command, args []string) error {
				client, err := c.client(cmd)
				if err != nil {
					return err
				}

				if err := client.DeletePool(cmd.Context(), args[0]); err != nil {
					return err
				}

				if c.output == "json" {
					return writeJSON(c.stdout, deletedJSON{ID: args[0], Deleted: true})
				}

				_, err = fmt.Fprintf(c.stdout, "deleted pool %s\n", args[0])

				return err
			}),
		},
		c.poolUpdateCommand(),
	)

	return pool
}

func (c *cli) poolCreateCommand() *cobra.Command {
	var (
		cidrs        []string
		parent       string
		prefixLength int
		meta         metadataFlags
		policy       policyFlags
	)

	cmd := &cobra.Command{
		Use:   "create <id> (--cidr CIDR... | --parent ID (--prefix-length N | --cidr CIDR)) [--strategy S] [--cooldown D --cooldown-mode M]",
		Short: "Create a root pool or carve a child pool from a parent",
		Args:  cobra.ExactArgs(1),
		RunE: c.leaf(func(cmd *cobra.Command, args []string) error {
			spec := ipam.PoolSpec{ID: args[0], ParentPool: parent, Description: meta.description}

			labels, err := parseLabels(meta.labels)
			if err != nil {
				return err
			}

			spec.Labels = labels

			spec.Policy.Strategy, err = policy.parseStrategy(cmd)
			if err != nil {
				return err
			}

			spec.Policy.Cooldown, err = policy.parseCooldown(cmd)
			if err != nil {
				return err
			}

			prefixSet := cmd.Flags().Changed("prefix-length")

			switch {
			case parent == "" && prefixSet:
				return usagef("--prefix-length requires --parent")
			case parent == "" && len(cidrs) == 0:
				return usagef("a pool needs --cidr, or --parent with --prefix-length or --cidr")
			case parent == "":
				for _, raw := range cidrs {
					prefix, err := parseCIDR(raw)
					if err != nil {
						return err
					}

					spec.CIDRs = append(spec.CIDRs, prefix)
				}
			case len(cidrs)+boolCount(prefixSet) != 1:
				return usagef("a child pool needs exactly one of --prefix-length or --cidr")
			case prefixSet:
				spec.Request = ipam.Request{Kind: ipam.RequestNext, PrefixLength: prefixLength}
			default:
				prefix, err := parseCIDR(cidrs[0])
				if err != nil {
					return err
				}

				spec.Request = ipam.Request{Kind: ipam.RequestExplicit, CIDR: prefix}
			}

			client, err := c.client(cmd)
			if err != nil {
				return err
			}

			created, err := client.CreatePool(cmd.Context(), spec)
			if err != nil {
				return err
			}

			return c.printPools([]ipam.Pool{created}, true)
		}),
	}

	cmd.Flags().StringArrayVar(&cidrs, "cidr", nil, "pool CIDR (repeatable for root pools; one explicit CIDR for child pools)")
	cmd.Flags().StringVar(&parent, "parent", "", "parent pool to carve this pool from")
	cmd.Flags().IntVar(&prefixLength, "prefix-length", 0, "carve the next free CIDR of this prefix length from --parent")
	meta.register(cmd)
	policy.register(cmd)

	return cmd
}

func (c *cli) poolUpdateCommand() *cobra.Command {
	var (
		meta       metadataFlags
		policy     policyFlags
		noCooldown bool
	)

	cmd := &cobra.Command{
		Use: "update <id> [--description TEXT] [--label k=v...] [--strategy S] " +
			"[--cooldown D --cooldown-mode M | --no-cooldown]",
		Short: "Update a pool's description, labels, strategy or cooldown (--label replaces the whole label set)",
		Args:  cobra.ExactArgs(1),
		RunE: c.leaf(func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()

			cooldownSet := flags.Changed("cooldown") || flags.Changed("cooldown-mode")
			metaChange := flags.Changed("description") || flags.Changed("label")
			policyChange := flags.Changed("strategy") || cooldownSet || noCooldown

			if noCooldown && cooldownSet {
				return usagef("--no-cooldown cannot be combined with --cooldown or --cooldown-mode")
			}

			if !metaChange && !policyChange {
				return usagef("nothing to update: pass --description, --label, --strategy, --cooldown with --cooldown-mode, or --no-cooldown")
			}

			labels, err := parseLabels(meta.labels)
			if err != nil {
				return err
			}

			strategy, err := policy.parseStrategy(cmd)
			if err != nil {
				return err
			}

			cooldown, err := policy.parseCooldown(cmd)
			if err != nil {
				return err
			}

			client, err := c.client(cmd)
			if err != nil {
				return err
			}

			current, err := client.GetPool(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			if policyChange {
				merged := current.Policy

				if flags.Changed("strategy") {
					merged.Strategy = strategy
				}

				if noCooldown {
					merged.Cooldown = ipam.Cooldown{}
				} else if cooldownSet {
					merged.Cooldown = cooldown
				}

				if err := client.UpdatePoolPolicy(cmd.Context(), args[0], merged); err != nil {
					return err
				}
			}

			if metaChange {
				description, labels := meta.merge(cmd, current.Description, current.Labels, labels)
				if err := client.UpdatePoolMetadata(cmd.Context(), args[0], description, labels); err != nil {
					return err
				}
			}

			updated, err := client.GetPool(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			return c.printPools([]ipam.Pool{updated}, true)
		}),
	}

	meta.register(cmd)
	policy.register(cmd)
	cmd.Flags().BoolVar(&noCooldown, "no-cooldown", false, "turn the cooldown off (not with --cooldown or --cooldown-mode)")

	return cmd
}

// policyFlags holds the pool allocation-policy flags shared by pool create and
// pool update.
type policyFlags struct {
	strategy     string
	cooldown     string
	cooldownMode string
}

func (p *policyFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&p.strategy, "strategy", "",
		"allocation strategy: best-fit (smallest free block that fits) or lowest (lowest address); "+
			"default best-fit for new pools")
	cmd.Flags().StringVar(&p.cooldown, "cooldown", "",
		"hold released CIDRs back for this duration, e.g. 1h (whole seconds, 1s to 8760h); needs --cooldown-mode")
	cmd.Flags().StringVar(&p.cooldownMode, "cooldown-mode", "",
		"cooldown mode for next requests: strict (never reuse a cooling range) or fit-aware "+
			"(avoid cooling ranges when an equally tight block exists); explicit CIDR requests bypass "+
			"cooldown in both modes, including strict; needs --cooldown")
}

// parseStrategy returns the strategy given with --strategy, or the library
// default when the flag was not passed.
func (p *policyFlags) parseStrategy(cmd *cobra.Command) (ipam.AllocationStrategy, error) {
	if !cmd.Flags().Changed("strategy") {
		return ipam.StrategyDefault, nil
	}

	switch strategy := ipam.AllocationStrategy(p.strategy); strategy {
	case ipam.StrategyBestFit, ipam.StrategyLowest:
		return strategy, nil
	default:
		return "", usagef("invalid --strategy %q: want best-fit or lowest", p.strategy)
	}
}

// parseCooldown returns the cooldown given with --cooldown and --cooldown-mode.
// Passing only one of the two is a usage error; passing neither returns the
// zero Cooldown.
func (p *policyFlags) parseCooldown(cmd *cobra.Command) (ipam.Cooldown, error) {
	durationSet := cmd.Flags().Changed("cooldown")
	modeSet := cmd.Flags().Changed("cooldown-mode")

	if durationSet != modeSet {
		return ipam.Cooldown{}, usagef("--cooldown and --cooldown-mode must be passed together")
	}

	if !durationSet {
		return ipam.Cooldown{}, nil
	}

	duration, err := time.ParseDuration(p.cooldown)
	if err != nil {
		return ipam.Cooldown{}, usagef("invalid --cooldown %q: want a duration such as 1h or 30m", p.cooldown)
	}

	switch mode := ipam.CooldownMode(p.cooldownMode); mode {
	case ipam.CooldownStrict, ipam.CooldownFitAware:
		return ipam.Cooldown{Mode: mode, Duration: duration}, nil
	default:
		return ipam.Cooldown{}, usagef("invalid --cooldown-mode %q: want strict or fit-aware", p.cooldownMode)
	}
}

func (c *cli) allocCommand() *cobra.Command {
	alloc := &cobra.Command{Use: "alloc", Aliases: []string{"allocation"}, Short: "Manage allocations", RunE: groupRun}
	alloc.AddCommand(
		c.allocCreateCommand(),
		&cobra.Command{
			Use: "get <pool> <id>", Short: "Show an allocation", Args: cobra.ExactArgs(2),
			RunE: c.leaf(func(cmd *cobra.Command, args []string) error {
				client, err := c.client(cmd)
				if err != nil {
					return err
				}

				got, err := client.GetAllocation(cmd.Context(), args[0], args[1])
				if err != nil {
					return err
				}

				return c.printAllocations([]ipam.Allocation{got}, true)
			}),
		},
		&cobra.Command{
			Use: "list <pool>", Short: "List a pool's allocations, including child-pool carve-outs", Args: cobra.ExactArgs(1),
			RunE: c.leaf(func(cmd *cobra.Command, args []string) error {
				client, err := c.client(cmd)
				if err != nil {
					return err
				}

				allocations, err := client.ListAllocations(cmd.Context(), args[0])
				if err != nil {
					return err
				}

				return c.printAllocations(allocations, false)
			}),
		},
		&cobra.Command{
			Use: "release <pool> <id>", Short: "Release an allocation", Long: releaseHelp, Args: cobra.ExactArgs(2),
			RunE: c.leaf(func(cmd *cobra.Command, args []string) error {
				client, err := c.client(cmd)
				if err != nil {
					return err
				}

				if err := client.Release(cmd.Context(), args[0], args[1]); err != nil {
					return err
				}

				if c.output == "json" {
					return writeJSON(c.stdout, releasedJSON{PoolID: args[0], ID: args[1], Released: true})
				}

				_, err = fmt.Fprintf(c.stdout, "released allocation %s from pool %s\n", args[1], args[0])

				return err
			}),
		},
		c.allocUpdateCommand(),
	)

	return alloc
}

func (c *cli) allocCreateCommand() *cobra.Command {
	var (
		cidr         string
		id           string
		prefixLength int
		meta         metadataFlags
	)

	cmd := &cobra.Command{
		Use:   "create <pool> (--prefix-length N | --cidr CIDR) [--id UUID]",
		Short: "Allocate the next free CIDR of a prefix length, or an explicit CIDR",
		Long: `Allocate the next free CIDR of a prefix length, or an explicit CIDR.

The allocation ID is generated (UUIDv7) unless --id is given, and is printed.
Repeating a create with the same --id and request returns the same allocation;
the same --id with a different request is a conflict (id_reused).`,
		Args: cobra.ExactArgs(1),
		RunE: c.leaf(func(cmd *cobra.Command, args []string) error {
			spec := ipam.AllocationSpec{ID: id, Description: meta.description}

			labels, err := parseLabels(meta.labels)
			if err != nil {
				return err
			}

			spec.Labels = labels

			prefixSet := cmd.Flags().Changed("prefix-length")
			cidrSet := cmd.Flags().Changed("cidr")

			switch {
			case prefixSet == cidrSet:
				return usagef("alloc create needs exactly one of --prefix-length or --cidr")
			case prefixSet:
				spec.PrefixLength = prefixLength
			default:
				prefix, err := parseCIDR(cidr)
				if err != nil {
					return err
				}

				spec.CIDR = prefix
			}

			client, err := c.client(cmd)
			if err != nil {
				return err
			}

			allocation, err := client.Allocate(cmd.Context(), args[0], spec)
			if err != nil {
				return err
			}

			return c.printAllocations([]ipam.Allocation{allocation}, true)
		}),
	}

	cmd.Flags().IntVar(&prefixLength, "prefix-length", 0, "allocate the next free CIDR of this prefix length")
	cmd.Flags().StringVar(&cidr, "cidr", "", "allocate exactly this CIDR")
	cmd.Flags().StringVar(&id, "id", "", "allocation ID (UUID); generated when omitted")
	meta.register(cmd)

	return cmd
}

func (c *cli) allocUpdateCommand() *cobra.Command {
	var meta metadataFlags

	cmd := &cobra.Command{
		Use:   "update <pool> <id> [--description TEXT] [--label k=v...]",
		Short: "Update an allocation's description or labels (--label replaces the whole label set)",
		Args:  cobra.ExactArgs(2),
		RunE: c.leaf(func(cmd *cobra.Command, args []string) error {
			if err := meta.requireChange(cmd); err != nil {
				return err
			}

			labels, err := parseLabels(meta.labels)
			if err != nil {
				return err
			}

			client, err := c.client(cmd)
			if err != nil {
				return err
			}

			current, err := client.GetAllocation(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}

			description, labels := meta.merge(cmd, current.Description, current.Labels, labels)
			if err := client.UpdateAllocationMetadata(cmd.Context(), args[0], args[1], description, labels); err != nil {
				return err
			}

			updated, err := client.GetAllocation(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}

			return c.printAllocations([]ipam.Allocation{updated}, true)
		}),
	}

	meta.register(cmd)

	return cmd
}

type metadataFlags struct {
	description string
	labels      []string
}

func (m *metadataFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&m.description, "description", "", "free-text description")
	cmd.Flags().StringArrayVar(&m.labels, "label", nil, "label as key=value (repeatable)")
}

func (m *metadataFlags) requireChange(cmd *cobra.Command) error {
	if !cmd.Flags().Changed("description") && !cmd.Flags().Changed("label") {
		return usagef("nothing to update: pass --description and/or --label")
	}

	return nil
}

// merge keeps the current value of every metadata flag that was not passed.
func (m *metadataFlags) merge(
	cmd *cobra.Command,
	description string,
	current, labels map[string]string,
) (string, map[string]string) {
	if cmd.Flags().Changed("description") {
		description = m.description
	}

	if !cmd.Flags().Changed("label") {
		labels = current
	}

	return description, labels
}

func parseLabels(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	labels := make(map[string]string, len(raw))

	for _, item := range raw {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			return nil, usagef("invalid --label %q: want key=value", item)
		}

		if _, dup := labels[key]; dup {
			return nil, usagef("duplicate --label key %q", key)
		}

		labels[key] = value
	}

	return labels, nil
}

func parseCIDR(raw string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return netip.Prefix{}, usagef("invalid --cidr %q: %v", raw, err)
	}

	return prefix, nil
}

func boolCount(b bool) int {
	if b {
		return 1
	}

	return 0
}
