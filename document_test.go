// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestCodecRootDocumentsRoundTrip(t *testing.T) {
	t.Parallel()

	pool := poolDocument{
		Pool: Pool{
			ID:          "prod-main",
			Family:      4,
			CIDRs:       []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
			Description: "production",
			Labels:      map[string]string{"team": "platform"},
		},
		Incarnation: "0123456789abcdef0123456789abcdef",
	}

	poolData, err := encodePoolDocument(pool)
	if err != nil {
		t.Fatalf("encodePoolDocument() unexpected error: %v", err)
	}

	decodedPool, err := decodePoolDocument(poolData, "prod-main")
	if err != nil {
		t.Fatalf("decodePoolDocument() unexpected error: %v", err)
	}

	if decodedPool.CIDRs[0].String() != "10.0.0.0/16" {
		t.Fatalf("decoded pool CIDR = %q, want 10.0.0.0/16", decodedPool.CIDRs[0])
	}

	reservations := reservationsDocument{
		PoolID:      "prod-main",
		Incarnation: pool.Incarnation,
		Allocations: map[string]Allocation{
			"018f22e2-1865-7be8-9f45-35d90607d0f7": {
				ID:     "018f22e2-1865-7be8-9f45-35d90607d0f7",
				PoolID: "prod-main",
				Request: Request{
					Kind:         RequestNext,
					PrefixLength: 24,
				},
				CIDR:      netip.MustParsePrefix("10.0.0.0/24"),
				Labels:    map[string]string{},
				CreatedAt: time.Date(2026, time.October, 29, 12, 30, 0, 0, time.UTC),
			},
		},
	}

	reservationsData, err := encodeReservationsDocument(reservations)
	if err != nil {
		t.Fatalf("encodeReservationsDocument() unexpected error: %v", err)
	}

	decodedReservations, err := decodeReservationsDocument(reservationsData, "prod-main")
	if err != nil {
		t.Fatalf("decodeReservationsDocument() unexpected error: %v", err)
	}

	if err := validate(decodedPool, decodedReservations); err != nil {
		t.Fatalf("validate() decoded documents: %v", err)
	}
}

func TestCodecRootMapsUnsupportedSchema(t *testing.T) {
	t.Parallel()

	_, err := decodePoolDocument(
		[]byte(`{"schema_version":2,"kind":"pool","pool_id":"prod-main"}`),
		"prod-main",
	)
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("decodePoolDocument() error = %v, want %v", err, ErrUnsupportedSchema)
	}
}

func TestCodecIncarnationIsRandomLowercaseHex(t *testing.T) {
	t.Parallel()

	first, err := newIncarnation()
	if err != nil {
		t.Fatalf("newIncarnation() unexpected error: %v", err)
	}

	second, err := newIncarnation()
	if err != nil {
		t.Fatalf("newIncarnation() unexpected error: %v", err)
	}

	if !validIncarnation(first) || !validIncarnation(second) {
		t.Fatalf("newIncarnation() returned invalid values %q and %q", first, second)
	}

	if first == second {
		t.Fatalf("newIncarnation() repeated value %q", first)
	}
}

func TestCodecRootMapsInvalidDocumentToInvalidArgument(t *testing.T) {
	t.Parallel()

	decodePool := func(data []byte) error {
		_, err := decodePoolDocument(data, "prod-main")
		return err
	}
	decodeReservations := func(data []byte) error {
		_, err := decodeReservationsDocument(data, "prod-main")
		return err
	}

	tests := []struct {
		name   string
		decode func([]byte) error
		data   string
	}{
		{
			name:   "pool truncated JSON",
			decode: decodePool,
			data:   `{"schema_version":1,"kind":"pool","pool_id":"prod-main"`,
		},
		{
			name:   "pool wrong kind",
			decode: decodePool,
			data:   `{"schema_version":1,"kind":"reservations","pool_id":"prod-main"}`,
		},
		{
			name:   "pool mismatched pool_id",
			decode: decodePool,
			data:   `{"schema_version":1,"kind":"pool","pool_id":"other"}`,
		},
		{
			name:   "reservations truncated JSON",
			decode: decodeReservations,
			data:   `{"schema_version":1,"kind":"reservations","pool_id":"prod-main","allocations":{}`,
		},
		{
			name:   "reservations wrong kind",
			decode: decodeReservations,
			data:   `{"schema_version":1,"kind":"pool","pool_id":"prod-main","allocations":{}}`,
		},
		{
			name:   "reservations mismatched pool_id",
			decode: decodeReservations,
			data:   `{"schema_version":1,"kind":"reservations","pool_id":"other","allocations":{}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.decode([]byte(test.data))
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("decode error = %v, want %v", err, ErrInvalidArgument)
			}
		})
	}
}

func TestCodecRootReservationsRoundTripUUIDv7AllocationID(t *testing.T) {
	t.Parallel()

	id := NewAllocationID()
	reservations := reservationsDocument{
		PoolID:      "prod-main",
		Incarnation: "0123456789abcdef0123456789abcdef",
		Allocations: map[string]Allocation{
			id: {
				ID:        id,
				PoolID:    "prod-main",
				Request:   Request{Kind: RequestNext, PrefixLength: 24},
				CIDR:      netip.MustParsePrefix("10.0.0.0/24"),
				Labels:    map[string]string{},
				CreatedAt: time.Date(2026, time.October, 29, 12, 30, 0, 0, time.UTC),
			},
		},
	}

	data, err := encodeReservationsDocument(reservations)
	if err != nil {
		t.Fatalf("encodeReservationsDocument() unexpected error: %v", err)
	}

	decoded, err := decodeReservationsDocument(data, "prod-main")
	if err != nil {
		t.Fatalf("decodeReservationsDocument() unexpected error: %v", err)
	}

	allocation, ok := decoded.Allocations[id]
	if !ok || len(decoded.Allocations) != 1 {
		t.Fatalf("decoded allocations = %v, want single key %q", decoded.Allocations, id)
	}

	if allocation.ID != id {
		t.Fatalf("decoded allocation ID = %q, want %q", allocation.ID, id)
	}
}

func TestCodecRootReservedAllocationIDValidation(t *testing.T) {
	t.Parallel()

	valid := "pool:child-1:0123456789abcdef0123456789abcdef"
	base := reservationsDocument{
		PoolID:      "prod-main",
		Incarnation: "fedcba9876543210fedcba9876543210",
		Allocations: map[string]Allocation{
			valid: {
				ID:        valid,
				PoolID:    "prod-main",
				Request:   Request{Kind: RequestNext, PrefixLength: 24},
				CIDR:      netip.MustParsePrefix("10.0.0.0/24"),
				Labels:    map[string]string{},
				CreatedAt: time.Date(2026, time.October, 29, 12, 30, 0, 0, time.UTC),
			},
		},
	}

	data, err := encodeReservationsDocument(base)
	if err != nil {
		t.Fatalf("encode valid reserved allocation ID: %v", err)
	}

	if _, err := decodeReservationsDocument(data, "prod-main"); err != nil {
		t.Fatalf("decode valid reserved allocation ID: %v", err)
	}

	for _, invalid := range []string{
		"pool:child-1",
		"pool:child-1:0123456789abcdef",
		"pool:child-1:0123456789ABCDEF0123456789ABCDEF",
		"pool:child-1:0123456789abcdef0123456789abcdeg",
	} {
		t.Run(invalid, func(t *testing.T) {
			copy := base
			allocation := base.Allocations[valid]
			allocation.ID = invalid

			copy.Allocations = map[string]Allocation{invalid: allocation}
			if _, err := encodeReservationsDocument(copy); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("encode error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
