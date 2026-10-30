// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"net/netip"
	"time"
)

// RequestKind identifies how an allocation CIDR was selected.
type RequestKind string

const (
	// RequestNext selects the next CIDR with PrefixLength using the pool's
	// allocation strategy.
	RequestNext RequestKind = "next"
	// RequestExplicit requests the exact CIDR.
	RequestExplicit RequestKind = "explicit"
)

// AllocationStrategy selects where a RequestNext allocation is placed.
type AllocationStrategy string

const (
	// StrategyDefault leaves the strategy unset: CreatePool resolves it to
	// StrategyBestFit, and a CreatePool re-run accepts any stored strategy.
	StrategyDefault AllocationStrategy = ""
	// StrategyLowest places allocations at the lowest available address.
	StrategyLowest AllocationStrategy = "lowest"
	// StrategyBestFit places allocations in the smallest free block that fits.
	StrategyBestFit AllocationStrategy = "best-fit"
)

// CooldownMode selects how recently released CIDRs are treated.
type CooldownMode string

const (
	// CooldownNone reuses released CIDRs immediately.
	CooldownNone CooldownMode = ""
	// CooldownStrict never returns a RequestNext CIDR overlapping an unexpired
	// release. Explicit CIDR requests bypass cooldown, including strict mode.
	CooldownStrict CooldownMode = "strict"
	// CooldownFitAware avoids cooling ranges only when an equally tight block exists.
	CooldownFitAware CooldownMode = "fit-aware"
)

// Cooldown configures how long released CIDRs are held back for RequestNext.
// Explicit CIDR requests bypass cooldown in every mode, including strict,
// while still requiring containment and no overlap with live allocations.
// Mode is CooldownNone exactly when Duration is zero; otherwise Duration is
// a whole number of seconds between 1s and 8760h.
type Cooldown struct {
	Mode     CooldownMode
	Duration time.Duration
}

// PoolPolicy is a pool's allocation strategy and cooldown.
type PoolPolicy struct {
	Strategy AllocationStrategy
	Cooldown Cooldown
}

// ParentRef identifies the allocation that carves a child pool from its parent.
type ParentRef struct {
	PoolID       string
	AllocationID string
}

// Pool describes one single-family collection of disjoint CIDRs.
type Pool struct {
	ID          string
	Family      int
	CIDRs       []netip.Prefix
	Parent      *ParentRef
	Description string
	Labels      map[string]string
	// Policy is always concrete on pools returned by the client.
	Policy PoolPolicy
}

// Request records how an allocation was requested.
type Request struct {
	Kind         RequestKind
	PrefixLength int
	CIDR         netip.Prefix
}

// Allocation is one CIDR assigned from a pool.
type Allocation struct {
	ID          string
	PoolID      string
	Request     Request
	CIDR        netip.Prefix
	Description string
	Labels      map[string]string
	CreatedAt   time.Time
}

type poolDocument struct {
	Pool
	Incarnation string
}

type reservationsDocument struct {
	PoolID      string
	Incarnation string
	Allocations map[string]Allocation
	Cooldown    Cooldown
	// Released maps released CIDRs to the expiry of their cooldown.
	Released map[netip.Prefix]time.Time
}
