// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package document defines the versioned JSON storage format.
package document

import "time"

const (
	schemaVersionV1  = 1
	poolKind         = "pool"
	reservationsKind = "reservations"
)

// Parent is the v1 child-pool parent reference.
type Parent struct {
	PoolID       string `json:"pool_id"`
	AllocationID string `json:"allocation_id"`
}

// Pool is the v1 pool document.
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

// Reservations is the v1 reservations document.
type Reservations struct {
	SchemaVersion int                   `json:"schema_version"`
	Kind          string                `json:"kind"`
	PoolID        string                `json:"pool_id"`
	Incarnation   string                `json:"incarnation"`
	Allocations   map[string]Allocation `json:"allocations"`
}
