// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"strings"
)

var (
	// ErrNotFound identifies a missing pool or allocation.
	ErrNotFound = errors.New("ipam: not found")
	// ErrConflict identifies a state or identity conflict.
	ErrConflict = errors.New("ipam: conflict")
	// ErrPoolExhausted identifies a pool without a matching free CIDR.
	ErrPoolExhausted = errors.New("ipam: pool exhausted")
	// ErrInvalidCIDR identifies malformed or inconsistent CIDR data.
	ErrInvalidCIDR = errors.New("ipam: invalid CIDR")
	// ErrInvalidArgument identifies an invalid non-CIDR argument.
	ErrInvalidArgument = errors.New("ipam: invalid argument")
	// ErrUnsupportedSchema identifies an unknown on-disk schema version.
	ErrUnsupportedSchema = errors.New("ipam: unsupported schema")
)

// OpError adds operation and resource context while preserving error identity.
type OpError struct {
	Op     string
	Pool   string
	ID     string
	Reason string
	Err    error
}

// Error implements error.
func (e *OpError) Error() string {
	if e == nil {
		return "<nil>"
	}

	message := e.Op
	if e.Pool != "" {
		// Pool operations ("create pool") already name the noun.
		if e.Op == "pool" || strings.HasSuffix(e.Op, " pool") {
			message += " " + e.Pool
		} else {
			message += " pool " + e.Pool
		}
	}

	if e.ID != "" {
		message += " allocation " + e.ID
	}

	if e.Reason != "" {
		message += " (" + e.Reason + ")"
	}

	if e.Err != nil {
		message += ": " + e.Err.Error()
	}

	if message == "" {
		return "ipam operation failed"
	}

	return message
}

// Unwrap returns the underlying sentinel or cause.
func (e *OpError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}
