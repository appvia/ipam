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
	// RequestNext selects the lowest available CIDR with PrefixLength.
	RequestNext RequestKind = "next"
	// RequestExplicit requests the exact CIDR.
	RequestExplicit RequestKind = "explicit"
)

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
}
