// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package document

import (
	"encoding/json"
	"fmt"
)

type envelope struct {
	SchemaVersion *int   `json:"schema_version"`
	Kind          string `json:"kind"`
	PoolID        string `json:"pool_id"`
}

// EncodePool serializes one v1 pool document with stable indentation.
func EncodePool(pool Pool) ([]byte, error) {
	if err := validateEnvelope(pool.SchemaVersion, pool.Kind, pool.PoolID, poolKind, pool.PoolID); err != nil {
		return nil, err
	}

	pool.Labels = normalizedLabels(pool.Labels)

	return encode(pool)
}

// DecodePool parses one v1 pool document and checks its storage key.
func DecodePool(data []byte, expectedPoolID string) (Pool, error) {
	if err := decodeEnvelope(data, expectedPoolID, poolKind); err != nil {
		return Pool{}, err
	}

	var pool Pool
	if err := json.Unmarshal(data, &pool); err != nil {
		return Pool{}, fmt.Errorf("decode pool: %w: %w", err, ErrInvalidDocument)
	}

	pool.Labels = normalizedLabels(pool.Labels)

	return pool, nil
}

// EncodeReservations serializes one v1 reservations document with stable indentation.
func EncodeReservations(reservations Reservations) ([]byte, error) {
	if err := validateEnvelope(
		reservations.SchemaVersion,
		reservations.Kind,
		reservations.PoolID,
		reservationsKind,
		reservations.PoolID,
	); err != nil {
		return nil, err
	}

	if reservations.Allocations == nil {
		return nil, fmt.Errorf("reservations allocations must be an object: %w", ErrInvalidDocument)
	}

	reservations.Allocations = normalizedAllocationLabels(reservations.Allocations)

	return encode(reservations)
}

// DecodeReservations parses one v1 reservations document and checks its storage key.
func DecodeReservations(data []byte, expectedPoolID string) (Reservations, error) {
	if err := decodeEnvelope(data, expectedPoolID, reservationsKind); err != nil {
		return Reservations{}, err
	}

	var reservations Reservations
	if err := json.Unmarshal(data, &reservations); err != nil {
		return Reservations{}, fmt.Errorf("decode reservations: %w: %w", err, ErrInvalidDocument)
	}

	if reservations.Allocations == nil {
		return Reservations{}, fmt.Errorf("reservations allocations must be an object: %w", ErrInvalidDocument)
	}

	reservations.Allocations = normalizedAllocationLabels(reservations.Allocations)

	return reservations, nil
}

func decodeEnvelope(data []byte, expectedPoolID, expectedKind string) error {
	var header envelope
	if err := json.Unmarshal(data, &header); err != nil {
		return fmt.Errorf("decode envelope: %w: %w", err, ErrInvalidDocument)
	}

	if header.SchemaVersion == nil {
		return fmt.Errorf("missing schema_version: %w", ErrInvalidDocument)
	}

	return validateEnvelope(*header.SchemaVersion, header.Kind, header.PoolID, expectedKind, expectedPoolID)
}

func validateEnvelope(schemaVersion int, kind, poolID, expectedKind, expectedPoolID string) error {
	if schemaVersion != schemaVersionV1 {
		return fmt.Errorf("schema_version %d: %w", schemaVersion, ErrUnsupportedSchema)
	}

	if kind != expectedKind {
		return fmt.Errorf("kind %q, expected %q: %w", kind, expectedKind, ErrInvalidDocument)
	}

	if poolID == "" || poolID != expectedPoolID {
		return fmt.Errorf("pool_id %q, expected %q: %w", poolID, expectedPoolID, ErrInvalidDocument)
	}

	return nil
}

func encode(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode document: %w", err)
	}

	return append(data, '\n'), nil
}

func normalizedAllocationLabels(allocations map[string]Allocation) map[string]Allocation {
	normalized := make(map[string]Allocation, len(allocations))
	for id, allocation := range allocations {
		allocation.Labels = normalizedLabels(allocation.Labels)
		normalized[id] = allocation
	}

	return normalized
}

func normalizedLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return map[string]string{}
	}

	return labels
}
