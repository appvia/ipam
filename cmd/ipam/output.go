// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/appvia/ipam"
)

type parentJSON struct {
	PoolID       string `json:"pool_id"`
	AllocationID string `json:"allocation_id"`
}

type cooldownJSON struct {
	Mode     string `json:"mode"`
	Duration string `json:"duration"`
}

type poolJSON struct {
	ID          string            `json:"id"`
	Family      int               `json:"family"`
	CIDRs       []string          `json:"cidrs"`
	Parent      *parentJSON       `json:"parent,omitempty"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	Strategy    string            `json:"strategy"`
	Cooldown    *cooldownJSON     `json:"cooldown,omitempty"`
}

type requestJSON struct {
	Kind         string `json:"kind"`
	PrefixLength *int   `json:"prefix_length,omitempty"`
	CIDR         string `json:"cidr,omitempty"`
}

type allocationJSON struct {
	ID          string            `json:"id"`
	PoolID      string            `json:"pool_id"`
	CIDR        string            `json:"cidr"`
	Request     requestJSON       `json:"request"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	CreatedAt   string            `json:"created_at"`
}

type deletedJSON struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type releasedJSON struct {
	PoolID   string `json:"pool_id"`
	ID       string `json:"id"`
	Released bool   `json:"released"`
}

func writeJSON(w io.Writer, value any) error {
	return json.NewEncoder(w).Encode(value)
}

func toPoolJSON(pool ipam.Pool) poolJSON {
	out := poolJSON{
		ID:          pool.ID,
		Family:      pool.Family,
		CIDRs:       make([]string, 0, len(pool.CIDRs)),
		Description: pool.Description,
		Labels:      pool.Labels,
		Strategy:    string(pool.Policy.Strategy),
	}

	if pool.Policy.Cooldown.Mode != ipam.CooldownNone {
		out.Cooldown = &cooldownJSON{
			Mode:     string(pool.Policy.Cooldown.Mode),
			Duration: pool.Policy.Cooldown.Duration.String(),
		}
	}

	for _, cidr := range pool.CIDRs {
		out.CIDRs = append(out.CIDRs, cidr.String())
	}

	if pool.Parent != nil {
		out.Parent = &parentJSON{PoolID: pool.Parent.PoolID, AllocationID: pool.Parent.AllocationID}
	}

	return out
}

func toAllocationJSON(allocation ipam.Allocation) allocationJSON {
	request := requestJSON{Kind: string(allocation.Request.Kind)}
	if allocation.Request.Kind == ipam.RequestExplicit {
		request.CIDR = allocation.Request.CIDR.String()
	} else {
		length := allocation.Request.PrefixLength
		request.PrefixLength = &length
	}

	return allocationJSON{
		ID:          allocation.ID,
		PoolID:      allocation.PoolID,
		CIDR:        allocation.CIDR.String(),
		Request:     request,
		Description: allocation.Description,
		Labels:      allocation.Labels,
		CreatedAt:   allocation.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (c *cli) printPools(pools []ipam.Pool, single bool) error {
	if c.output == "json" {
		items := make([]poolJSON, 0, len(pools))
		for _, pool := range pools {
			items = append(items, toPoolJSON(pool))
		}

		if single {
			return writeJSON(c.stdout, items[0])
		}

		return writeJSON(c.stdout, items)
	}

	table := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "ID\tFAMILY\tCIDRS\tPARENT\tSTRATEGY\tCOOLDOWN\tDESCRIPTION\tLABELS")

	for _, pool := range pools {
		item := toPoolJSON(pool)

		parent := "-"
		if item.Parent != nil {
			parent = item.Parent.PoolID
		}

		cooldown := "-"
		if item.Cooldown != nil {
			cooldown = item.Cooldown.Mode + " " + item.Cooldown.Duration
		}

		_, _ = fmt.Fprintf(table, "%s\tIPv%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.ID, item.Family, strings.Join(item.CIDRs, ","), parent,
			item.Strategy, cooldown, dash(item.Description), formatLabels(item.Labels))
	}

	return table.Flush()
}

func (c *cli) printAllocations(allocations []ipam.Allocation, single bool) error {
	if c.output == "json" {
		items := make([]allocationJSON, 0, len(allocations))
		for _, allocation := range allocations {
			items = append(items, toAllocationJSON(allocation))
		}

		if single {
			return writeJSON(c.stdout, items[0])
		}

		return writeJSON(c.stdout, items)
	}

	table := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "ID\tCIDR\tCREATED_AT\tDESCRIPTION\tLABELS")

	for _, allocation := range allocations {
		item := toAllocationJSON(allocation)
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			item.ID, item.CIDR, item.CreatedAt, dash(item.Description), formatLabels(item.Labels))
	}

	return table.Flush()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}

	pairs := make([]string, 0, len(labels))
	for _, key := range slices.Sorted(maps.Keys(labels)) {
		pairs = append(pairs, key+"="+labels[key])
	}

	return strings.Join(pairs, ",")
}

func dash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
