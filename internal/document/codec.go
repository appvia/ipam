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

// poolV2 is the v2 wire form: the v1 fields followed by the strategy.
type poolV2 struct {
	Pool
	Strategy string `json:"strategy"`
}

// reservationsV2 is the v2 wire form: the v1 fields followed by cooldown and release records.
type reservationsV2 struct {
	Reservations
	Cooldown *Cooldown         `json:"cooldown"`
	Released map[string]string `json:"released"`
}

// EncodePool serializes one pool document, v1 or v2 by SchemaVersion, with stable indentation.
func EncodePool(pool Pool) ([]byte, error) {
	if err := validateEnvelope(pool.SchemaVersion, pool.Kind, pool.PoolID, poolKind, pool.PoolID); err != nil {
		return nil, err
	}

	pool.Labels = normalizedLabels(pool.Labels)

	if pool.SchemaVersion == SchemaVersionV1 {
		if pool.Strategy != "" {
			return nil, fmt.Errorf("v1 pool must not carry a strategy: %w", ErrInvalidDocument)
		}

		return encode(pool)
	}

	if pool.Strategy == "" {
		return nil, fmt.Errorf("v2 pool strategy is required: %w", ErrInvalidDocument)
	}

	return encode(poolV2{Pool: pool, Strategy: pool.Strategy})
}

// DecodePool parses one v1 or v2 pool document and checks its storage key.
func DecodePool(data []byte, expectedPoolID string) (Pool, error) {
	version, keys, err := decodeEnvelope(data, expectedPoolID, poolKind)
	if err != nil {
		return Pool{}, err
	}

	var pool Pool

	if version == SchemaVersionV1 {
		if err := rejectKeys(version, keys, "strategy"); err != nil {
			return Pool{}, err
		}

		if err := json.Unmarshal(data, &pool); err != nil {
			return Pool{}, fmt.Errorf("decode pool: %w: %w", err, ErrInvalidDocument)
		}
	} else {
		var wire poolV2
		if err := json.Unmarshal(data, &wire); err != nil {
			return Pool{}, fmt.Errorf("decode pool: %w: %w", err, ErrInvalidDocument)
		}

		if wire.Strategy == "" {
			return Pool{}, fmt.Errorf("v2 pool strategy is required: %w", ErrInvalidDocument)
		}

		pool = wire.Pool
		pool.Strategy = wire.Strategy
	}

	pool.Labels = normalizedLabels(pool.Labels)

	return pool, nil
}

// EncodeReservations serializes one reservations document, v1 or v2 by
// SchemaVersion, with stable indentation.
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

	if reservations.SchemaVersion == SchemaVersionV1 {
		if reservations.Cooldown != nil || reservations.Released != nil {
			return nil, fmt.Errorf("v1 reservations must not carry cooldown or released: %w", ErrInvalidDocument)
		}

		return encode(reservations)
	}

	if reservations.Released == nil {
		return nil, fmt.Errorf("v2 reservations released must be an object: %w", ErrInvalidDocument)
	}

	return encode(reservationsV2{
		Reservations: reservations,
		Cooldown:     reservations.Cooldown,
		Released:     reservations.Released,
	})
}

// DecodeReservations parses one v1 or v2 reservations document and checks its storage key.
func DecodeReservations(data []byte, expectedPoolID string) (Reservations, error) {
	version, keys, err := decodeEnvelope(data, expectedPoolID, reservationsKind)
	if err != nil {
		return Reservations{}, err
	}

	var reservations Reservations

	if version == SchemaVersionV1 {
		if err := rejectKeys(version, keys, "cooldown", "released"); err != nil {
			return Reservations{}, err
		}

		if err := json.Unmarshal(data, &reservations); err != nil {
			return Reservations{}, fmt.Errorf("decode reservations: %w: %w", err, ErrInvalidDocument)
		}
	} else {
		var wire reservationsV2
		if err := json.Unmarshal(data, &wire); err != nil {
			return Reservations{}, fmt.Errorf("decode reservations: %w: %w", err, ErrInvalidDocument)
		}

		if wire.Released == nil {
			return Reservations{}, fmt.Errorf("v2 reservations released must be an object: %w", ErrInvalidDocument)
		}

		reservations = wire.Reservations
		reservations.Cooldown = wire.Cooldown
		reservations.Released = wire.Released
	}

	if reservations.Allocations == nil {
		return Reservations{}, fmt.Errorf("reservations allocations must be an object: %w", ErrInvalidDocument)
	}

	reservations.Allocations = normalizedAllocationLabels(reservations.Allocations)

	return reservations, nil
}

// decodeEnvelope validates the envelope and returns the schema version and the
// top-level keys present in the document.
func decodeEnvelope(data []byte, expectedPoolID, expectedKind string) (int, map[string]json.RawMessage, error) {
	var header envelope
	if err := json.Unmarshal(data, &header); err != nil {
		return 0, nil, fmt.Errorf("decode envelope: %w: %w", err, ErrInvalidDocument)
	}

	if header.SchemaVersion == nil {
		return 0, nil, fmt.Errorf("missing schema_version: %w", ErrInvalidDocument)
	}

	if err := validateEnvelope(*header.SchemaVersion, header.Kind, header.PoolID, expectedKind, expectedPoolID); err != nil {
		return 0, nil, err
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return 0, nil, fmt.Errorf("decode envelope: %w: %w", err, ErrInvalidDocument)
	}

	return *header.SchemaVersion, keys, nil
}

func rejectKeys(version int, keys map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		if _, present := keys[name]; present {
			return fmt.Errorf("schema_version %d must not contain %q: %w", version, name, ErrInvalidDocument)
		}
	}

	return nil
}

func validateEnvelope(schemaVersion int, kind, poolID, expectedKind, expectedPoolID string) error {
	if schemaVersion != SchemaVersionV1 && schemaVersion != SchemaVersionV2 {
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
