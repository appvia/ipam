// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package document defines the versioned JSON storage format.
package document

import "time"

const (
	// SchemaVersionV1 is the original document schema.
	SchemaVersionV1 = 1
	// SchemaVersionV2 adds the pool strategy and the reservations cooldown and release records.
	SchemaVersionV2 = 2

	poolKind         = "pool"
	reservationsKind = "reservations"
)

// Parent is the v1 child-pool parent reference.
type Parent struct {
	PoolID       string `json:"pool_id"`
	AllocationID string `json:"allocation_id"`
}

// Pool is a pool document. SchemaVersion selects the wire format: Strategy is
// carried only by v2 (required there) and must be empty for v1.
type Pool struct {
	SchemaVersion int               `json:"schema_version"`
	Kind          string            `json:"kind"`
	PoolID        string            `json:"pool_id"`
	Incarnation   string            `json:"incarnation"`
	Family        int               `json:"family"`
	CIDRs         []string          `json:"cidrs"`
	Parent        *Parent           `json:"parent"`
	Description   string            `json:"description"`
	Labels        map[string]string `json:"labels"`
	// Strategy is the v2 placement strategy name; it is not validated here.
	Strategy string `json:"-"`
}

// Request is the v1 stored allocation request.
type Request struct {
	Kind         string `json:"kind"`
	PrefixLength int    `json:"prefix_length,omitempty"`
	CIDR         string `json:"cidr,omitempty"`
}

// Allocation is the v1 stored allocation.
type Allocation struct {
	Request     Request           `json:"request"`
	CIDR        string            `json:"cidr"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	CreatedAt   time.Time         `json:"created_at"`
}

// Cooldown is the v2 stored cooldown configuration; mode is not validated here.
type Cooldown struct {
	Mode    string `json:"mode"`
	Seconds int64  `json:"seconds"`
}

// Reservations is a reservations document. SchemaVersion selects the wire
// format: Cooldown and Released are carried only by v2 (Released is required
// there, as RFC3339Nano UTC expiry times keyed by CIDR) and must be nil for v1.
type Reservations struct {
	SchemaVersion int                   `json:"schema_version"`
	Kind          string                `json:"kind"`
	PoolID        string                `json:"pool_id"`
	Incarnation   string                `json:"incarnation"`
	Allocations   map[string]Allocation `json:"allocations"`
	Cooldown      *Cooldown             `json:"-"`
	Released      map[string]string     `json:"-"`
}
