// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/appvia/ipam"
	"github.com/appvia/ipam/storage"
)

const doctorHelp = `Check a backend without changing any data.

Checks:
  backend                 pools/ can be listed and a pool document read.
  data-protection         versioning or soft delete is enabled (unknown: WARN).
  hierarchical-namespace  Azure accounts with a hierarchical namespace FAIL.
  write-probe             per pool: re-writes reservations/<pool>.json with its
                          own bytes under a deliberately wrong version. A
                          precondition failure means writes are allowed and
                          conditional writes are honoured; access denied means
                          no write access; success means the backend IGNORES
                          conditional writes (FAIL; the bytes were identical,
                          so the content is unchanged). A pool whose document
                          cannot be read is a WARN and is not written to.
  fragmentation           per pool: utilisation, free space, largest free block,
                          free blocks by prefix length and, for the pool's most
                          requested prefix length, how much of the free space is
                          allocatable and the fragmentation index. Read-only.
                          WARN when the index is >= 0.5 and at least 4 such
                          prefixes would fit if the free space were packed, or
                          when the statistics cannot be read (not found,
                          unsupported schema or read error). Never FAIL; a WARN
                          makes the overall result WARN and the exit code stays 0.

--expect-write and --expect-no-write turn the write probe of a pool into an
assertion. Doctor never creates, deletes or modifies data. It cannot detect a
backend that ignores conditional DELETE, because probing that would delete data.

Exit codes: 0 no failed check, 6 at least one failed check (1, 2 as usual).`

const (
	statusPass = "PASS"
	statusWarn = "WARN"
	statusFail = "FAIL"

	// probeVersion is the version the write probe sends as If-Match. It must be
	// a well-formed Azure ETag (a quoted "0x" plus hex): Azure Blob Storage
	// answers any other If-Match (unquoted, or quoted non-hex such as
	// "doctor-probe") with 412 before it checks permissions, which would report
	// a reader as "write allowed". It can never match a real version: Azure
	// ETags encode a non-zero timestamp, S3 ETags are MD5-based hex without
	// "0x", and the file and memory backends use unquoted versions.
	probeVersion storage.Version = `"0x000000000000000"`
)

type checkResult struct {
	Check  string             `json:"check"`
	Pool   string             `json:"pool,omitempty"`
	Status string             `json:"status"`
	Detail string             `json:"detail"`
	Stats  *fragmentationJSON `json:"stats,omitempty"`
}

// fragmentationJSON is the machine-readable form of a fragmentation check.
// Numbers are decimal strings; allocatable, capacity and fragmentation_index
// are empty when the pool has no reference prefix length.
type fragmentationJSON struct {
	Utilization        string          `json:"utilization"`
	Total              string          `json:"total"`
	Free               string          `json:"free"`
	Allocatable        string          `json:"allocatable"`
	Capacity           string          `json:"capacity"`
	FragmentationIndex string          `json:"fragmentation_index"`
	LargestFree        string          `json:"largest_free"`
	FreeBlocks         []freeBlockJSON `json:"free_blocks"`
	ReferenceLength    int             `json:"reference_length"`
	Strategy           string          `json:"strategy"`
	Cooldown           string          `json:"cooldown"`
}

type freeBlockJSON struct {
	PrefixLength int `json:"prefix_length"`
	Count        int `json:"count"`
}

const (
	// fragmentationMinCapacity is the smallest capacity for which a high
	// fragmentation index (>= 1/2) is a WARN.
	fragmentationMinCapacity = 4
)

type doctorReport struct {
	Result string        `json:"result"`
	Checks []checkResult `json:"checks"`
}

type doctorFailedError struct{ failed int }

func (e *doctorFailedError) Error() string {
	return fmt.Sprintf("doctor: %d check(s) failed", e.failed)
}

func (c *cli) doctorCommand() *cobra.Command {
	var expectWrite, expectNoWrite []string

	cmd := &cobra.Command{
		Use:   "doctor [--expect-write POOL...] [--expect-no-write POOL...]",
		Short: "Check backend access, data protection and write permission without changing data",
		Long:  doctorHelp,
		Args:  cobra.NoArgs,
		RunE: c.leaf(func(cmd *cobra.Command, _ []string) error {
			expectations, err := writeExpectations(expectWrite, expectNoWrite)
			if err != nil {
				return err
			}

			backend, err := c.backendURL()
			if err != nil {
				return err
			}

			store, err := c.openStorage(cmd.Context(), backend)
			if err != nil {
				return err
			}

			if closer, ok := store.(io.Closer); ok {
				c.closers = append(c.closers, closer)
			}

			report := diagnose(cmd.Context(), store, strings.HasPrefix(backend, "file:"), expectations)
			if err := c.printDoctor(report); err != nil {
				return err
			}

			if failed := report.count(statusFail); failed > 0 {
				return &doctorFailedError{failed: failed}
			}

			return nil
		}),
	}

	cmd.Flags().StringArrayVar(&expectWrite, "expect-write", nil, "pool that must be writable (repeatable)")
	cmd.Flags().StringArrayVar(&expectNoWrite, "expect-no-write", nil, "pool that must not be writable (repeatable)")

	return cmd
}

// writeExpectations maps each pool to true (write expected) or false (no
// write expected).
func writeExpectations(write, noWrite []string) (map[string]bool, error) {
	expectations := map[string]bool{}

	for _, pools := range []struct {
		ids      []string
		writable bool
	}{{write, true}, {noWrite, false}} {
		for _, id := range pools.ids {
			if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
				return nil, usagef("invalid pool %q", id)
			}

			if previous, seen := expectations[id]; seen && previous != pools.writable {
				return nil, usagef("pool %q is in both --expect-write and --expect-no-write", id)
			}

			expectations[id] = pools.writable
		}
	}

	return expectations, nil
}

// diagnose runs every check. It only reads, apart from the write probe, which
// sends a pool's own reservations bytes under a version that cannot match.
func diagnose(ctx context.Context, store storage.Storage, fileBackend bool, expectations map[string]bool) doctorReport {
	var report doctorReport

	pools, backend := checkBackend(ctx, store)
	report.add(backend)

	if backend.Status == statusFail {
		return report.finish()
	}

	report.add(checkProtection(ctx, store, fileBackend)...)

	for id := range expectations {
		if !slices.Contains(pools, id) {
			pools = append(pools, id)
		}
	}

	slices.Sort(pools)

	for _, id := range pools {
		expectation, expected := expectations[id]
		report.add(probeWrite(ctx, store, id, expectation, expected))
	}

	client := ipam.New(store)

	for _, id := range pools {
		report.add(checkFragmentation(ctx, client, id))
	}

	return report.finish()
}

func checkBackend(ctx context.Context, store storage.Storage) ([]string, checkResult) {
	result := checkResult{Check: "backend", Status: statusFail}

	keys, err := store.List(ctx, "pools/")
	if err != nil {
		result.Detail = fmt.Sprintf("cannot list pools/: %v", err)
		return nil, result
	}

	var pools []string

	for _, key := range keys {
		id, ok := strings.CutSuffix(strings.TrimPrefix(key, "pools/"), ".json")
		if ok && id != "" && !strings.Contains(id, "/") {
			pools = append(pools, id)
		}
	}

	if len(pools) > 0 {
		if _, _, err := store.Get(ctx, "pools/"+pools[0]+".json"); err != nil {
			result.Detail = fmt.Sprintf("cannot read pools/%s.json: %v", pools[0], err)
			return nil, result
		}
	}

	result.Status = statusPass
	result.Detail = fmt.Sprintf("pools/ is listable and readable (%d pool(s))", len(pools))

	return pools, result
}

func checkProtection(ctx context.Context, store storage.Storage, fileBackend bool) []checkResult {
	protection := checkResult{Check: "data-protection"}
	namespace := checkResult{Check: "hierarchical-namespace"}

	info := storage.Info{}

	inspector, ok := store.(storage.Inspector)
	if ok {
		var err error

		info, err = inspector.Inspect(ctx)
		if err != nil {
			protection.Status = statusFail
			protection.Detail = fmt.Sprintf("cannot inspect the backend: %v", err)
			namespace.Status = statusWarn
			namespace.Detail = "not determined: inspecting the backend failed"

			return []checkResult{protection, namespace}
		}
	}

	versions := fmt.Sprintf("versioning %s, soft delete %s", info.Versioning, info.SoftDelete)

	switch {
	case info.Versioning == storage.Yes || info.SoftDelete == storage.Yes:
		protection.Status = statusPass
		protection.Detail = versions
	case info.Versioning == storage.No && info.SoftDelete == storage.No:
		protection.Status = statusFail
		protection.Detail = versions + ": an overwritten or deleted document cannot be recovered"
	case fileBackend:
		protection.Status = statusWarn
		protection.Detail = "versioning and soft delete are not applicable to file:// backends; back up the directory"
	default:
		protection.Status = statusWarn
		protection.Detail = versions + ": could not confirm that documents can be recovered"
	}

	switch info.HierarchicalNamespace {
	case storage.Yes:
		namespace.Status = statusFail
		namespace.Detail = "hierarchical namespace (Data Lake Gen2) is enabled; it is not supported"
	case storage.No:
		namespace.Status = statusPass
		namespace.Detail = "no hierarchical namespace"
	default:
		namespace.Status = statusWarn
		namespace.Detail = "hierarchical namespace could not be determined"
	}

	return []checkResult{protection, namespace}
}

// probeWrite re-writes reservations/<pool>.json with its own bytes under a
// version that cannot match, so a backend honouring conditional writes
// rejects it and a backend ignoring them writes identical content.
func probeWrite(ctx context.Context, store storage.Storage, pool string, wantWrite, expected bool) checkResult {
	result := checkResult{Check: "write-probe", Pool: pool, Status: statusFail}
	key := "reservations/" + pool + ".json"

	data, _, err := store.Get(ctx, key)
	if err != nil {
		result.Status = statusWarn

		if errors.Is(err, storage.ErrNotFound) {
			result.Detail = "no reservations document; write probe skipped"
		} else {
			result.Detail = fmt.Sprintf("reservations document not readable (%v); write probe skipped", err)
		}

		return result
	}

	var writable bool

	_, err = store.PutIfMatch(ctx, key, data, probeVersion)

	switch {
	case err == nil:
		result.Detail = "CRITICAL: conditional writes ignored: a write with a wrong version succeeded " +
			"(the same bytes were written, so the content is unchanged); concurrent changes can be lost"

		return result
	case errors.Is(err, storage.ErrPreconditionFailed):
		writable = true
	case errors.Is(err, storage.ErrAccessDenied):
		writable = false
	default:
		result.Detail = fmt.Sprintf("write probe failed: %v", err)
		return result
	}

	switch {
	case expected && wantWrite && !writable:
		result.Detail = "write denied, but --expect-write was given"
	case expected && !wantWrite && writable:
		result.Detail = "write allowed, but --expect-no-write was given"
	case writable:
		result.Status = statusPass
		result.Detail = "write allowed; conditional writes honoured"
	default:
		result.Status = statusPass
		result.Detail = "write denied"
	}

	return result
}

// checkFragmentation reports a pool's free-space statistics. It only reads and
// never FAILs: unavailable statistics are a WARN naming the error class, not
// the underlying error text, which could carry a URL.
func checkFragmentation(ctx context.Context, client *ipam.Client, pool string) checkResult {
	result := checkResult{Check: "fragmentation", Pool: pool, Status: statusWarn}

	stats, err := client.PoolStats(ctx, pool, 0)
	if err != nil {
		class := "read error"

		switch {
		case errors.Is(err, ipam.ErrNotFound):
			class = "not found"
		case errors.Is(err, ipam.ErrUnsupportedSchema):
			class = "unsupported schema"
		}

		result.Detail = "statistics unavailable: " + class

		return result
	}

	result.Detail = fragmentationDetail(stats)
	result.Stats = fragmentationStats(stats)

	result.Status = statusPass
	if stats.FragmentationIndex != nil && stats.Capacity != nil &&
		stats.Capacity.Cmp(big.NewInt(fragmentationMinCapacity)) >= 0 &&
		stats.FragmentationIndex.Cmp(big.NewRat(1, 2)) >= 0 {
		result.Status = statusWarn
	}

	return result
}

func utilization(stats ipam.PoolStats) *big.Rat {
	if stats.Total.Sign() == 0 {
		return new(big.Rat)
	}

	used := new(big.Int).Sub(stats.Total, stats.Free)

	return new(big.Rat).SetFrac(used, stats.Total)
}

func fragmentationDetail(stats ipam.PoolStats) string {
	percent := new(big.Rat).Mul(utilization(stats), big.NewRat(100, 1))
	largest := "none"

	if stats.Largest.IsValid() {
		largest = fmt.Sprintf("/%d", stats.Largest.Bits())
	}

	blocks := make([]string, 0, len(stats.Histogram))
	for _, block := range stats.Histogram {
		blocks = append(blocks, fmt.Sprintf("/%d:%d", block.PrefixLength, block.Count))
	}

	if len(blocks) == 0 {
		blocks = append(blocks, "none")
	}

	detail := fmt.Sprintf("util %s%%, free %s of %s, largest %s, blocks %s",
		percent.FloatString(1), stats.Free, stats.Total, largest, strings.Join(blocks, " "))

	if stats.ReferenceLength != 0 && stats.Allocatable != nil && stats.Capacity != nil {
		detail += fmt.Sprintf(", /%d allocatable %s of %s", stats.ReferenceLength, stats.Allocatable, stats.Capacity)

		if stats.FragmentationIndex != nil {
			detail += ", index " + stats.FragmentationIndex.FloatString(2)
		}
	}

	return detail
}

func fragmentationStats(stats ipam.PoolStats) *fragmentationJSON {
	out := &fragmentationJSON{
		Utilization:     utilization(stats).FloatString(4),
		Total:           stats.Total.String(),
		Free:            stats.Free.String(),
		FreeBlocks:      make([]freeBlockJSON, 0, len(stats.Histogram)),
		ReferenceLength: stats.ReferenceLength,
		Strategy:        string(stats.Policy.Strategy),
		Cooldown:        "none",
	}

	if stats.Largest.IsValid() {
		out.LargestFree = stats.Largest.String()
	}

	for _, block := range stats.Histogram {
		out.FreeBlocks = append(out.FreeBlocks, freeBlockJSON{PrefixLength: block.PrefixLength, Count: block.Count})
	}

	if stats.Allocatable != nil {
		out.Allocatable = stats.Allocatable.String()
	}

	if stats.Capacity != nil {
		out.Capacity = stats.Capacity.String()
	}

	if stats.FragmentationIndex != nil {
		out.FragmentationIndex = stats.FragmentationIndex.FloatString(2)
	}

	if mode := stats.Policy.Cooldown.Mode; mode != ipam.CooldownNone {
		out.Cooldown = fmt.Sprintf("%s %s", mode, stats.Policy.Cooldown.Duration)
	}

	return out
}

func (r *doctorReport) add(checks ...checkResult) {
	r.Checks = append(r.Checks, checks...)
}

func (r *doctorReport) count(status string) int {
	n := 0

	for _, check := range r.Checks {
		if check.Status == status {
			n++
		}
	}

	return n
}

func (r *doctorReport) finish() doctorReport {
	switch {
	case r.count(statusFail) > 0:
		r.Result = "fail"
	case r.count(statusWarn) > 0:
		r.Result = "warn"
	default:
		r.Result = "pass"
	}

	return *r
}

func (c *cli) printDoctor(report doctorReport) error {
	if c.output == "json" {
		return writeJSON(c.stdout, report)
	}

	table := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "CHECK\tPOOL\tSTATUS\tDETAIL")

	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", check.Check, dash(check.Pool), check.Status, check.Detail)
	}

	if err := table.Flush(); err != nil {
		return err
	}

	_, err := fmt.Fprintf(c.stdout, "RESULT: %s\n", report.Result)

	return err
}
