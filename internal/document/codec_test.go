// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package document

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodecNilLabelsEncodeAsObjects(t *testing.T) {
	t.Parallel()

	poolData, err := EncodePool(Pool{
		SchemaVersion: 1,
		Kind:          "pool",
		PoolID:        "prod-main",
		Incarnation:   "0123456789abcdef0123456789abcdef",
		Family:        4,
		CIDRs:         []string{"10.0.0.0/16"},
	})
	if err != nil {
		t.Fatalf("EncodePool() unexpected error: %v", err)
	}

	assertLabelsObject(t, poolData)
	assertForbiddenFieldsAbsent(t, poolData, `"etag"`, `"allocations"`, `"owner"`, `"key"`)

	reservationsData, err := EncodeReservations(Reservations{
		SchemaVersion: 1,
		Kind:          "reservations",
		PoolID:        "prod-main",
		Incarnation:   "0123456789abcdef0123456789abcdef",
		Allocations: map[string]Allocation{
			"018f22e2-1865-7be8-9f45-35d90607d0f7": {
				Request:   Request{Kind: "next", PrefixLength: 24},
				CIDR:      "10.0.0.0/24",
				CreatedAt: time.Date(2026, time.October, 29, 12, 30, 0, 0, time.UTC),
			},
		},
	})
	if err != nil {
		t.Fatalf("EncodeReservations() unexpected error: %v", err)
	}

	assertLabelsObject(t, reservationsData)
	assertForbiddenFieldsAbsent(t, reservationsData, `"etag"`, `"owner"`, `"key"`)
}

func TestCodecDecodeNormalizesNullAndAbsentLabels(t *testing.T) {
	t.Parallel()

	pool, err := DecodePool([]byte(`{
	  "schema_version": 1,
	  "kind": "pool",
	  "pool_id": "prod-main",
	  "incarnation": "0123456789abcdef0123456789abcdef",
	  "family": 4,
	  "cidrs": ["10.0.0.0/16"],
	  "parent": null,
	  "description": "",
	  "labels": null
	}`), "prod-main")
	if err != nil {
		t.Fatalf("DecodePool() unexpected error: %v", err)
	}

	if pool.Labels == nil || len(pool.Labels) != 0 {
		t.Fatalf("DecodePool() labels = %#v, want non-nil empty map", pool.Labels)
	}

	reservations, err := DecodeReservations([]byte(`{
	  "schema_version": 1,
	  "kind": "reservations",
	  "pool_id": "prod-main",
	  "incarnation": "0123456789abcdef0123456789abcdef",
	  "allocations": {
	    "018f22e2-1865-7be8-9f45-35d90607d0f7": {
	      "request": {"kind": "next", "prefix_length": 24},
	      "cidr": "10.0.0.0/24",
	      "description": "",
	      "created_at": "2026-10-29T12:30:00Z"
	    }
	  }
	}`), "prod-main")
	if err != nil {
		t.Fatalf("DecodeReservations() unexpected error: %v", err)
	}

	for id, allocation := range reservations.Allocations {
		if allocation.Labels == nil || len(allocation.Labels) != 0 {
			t.Fatalf("DecodeReservations() allocation %q labels = %#v, want non-nil empty map", id, allocation.Labels)
		}
	}
}

func TestCodecPoolRoundTrip(t *testing.T) {
	t.Parallel()

	given := Pool{
		SchemaVersion: 1,
		Kind:          "pool",
		PoolID:        "prod-main",
		Incarnation:   "0123456789abcdef0123456789abcdef",
		Family:        4,
		CIDRs:         []string{"10.0.0.0/16", "10.2.0.0/16"},
		Parent: &Parent{
			PoolID:       "global",
			AllocationID: "pool:prod-main:0123456789abcdef0123456789abcdef",
		},
		Description: "production",
		Labels:      map[string]string{"region": "eu-west-1", "team": "platform"},
	}

	encoded, err := EncodePool(given)
	if err != nil {
		t.Fatalf("EncodePool() unexpected error: %v", err)
	}

	decoded, err := DecodePool(encoded, "prod-main")
	if err != nil {
		t.Fatalf("DecodePool() unexpected error: %v", err)
	}

	reencoded, err := EncodePool(decoded)
	if err != nil {
		t.Fatalf("EncodePool() after decode unexpected error: %v", err)
	}

	if string(reencoded) != string(encoded) {
		t.Fatalf("pool round trip changed bytes:\nfirst:\n%s\nsecond:\n%s", encoded, reencoded)
	}
}

func assertLabelsObject(t *testing.T, data []byte) {
	t.Helper()

	if bytes.Contains(data, []byte(`"labels": null`)) {
		t.Fatalf("encoded labels are null:\n%s", data)
	}

	if !bytes.Contains(data, []byte(`"labels": {}`)) {
		t.Fatalf("encoded labels are not an object:\n%s", data)
	}
}

func assertForbiddenFieldsAbsent(t *testing.T, data []byte, fields ...string) {
	t.Helper()

	for _, field := range fields {
		if bytes.Contains(data, []byte(field)) {
			t.Fatalf("encoded document contains forbidden field %s:\n%s", field, data)
		}
	}
}

func TestCodecReservationsRoundTrip(t *testing.T) {
	t.Parallel()

	given := Reservations{
		SchemaVersion: 1,
		Kind:          "reservations",
		PoolID:        "prod-main",
		Incarnation:   "0123456789abcdef0123456789abcdef",
		Allocations: map[string]Allocation{
			"018f22e2-1865-7be8-9f45-35d90607d0f7": {
				Request: Request{
					Kind:         "next",
					PrefixLength: 24,
				},
				CIDR:        "10.0.0.0/24",
				Description: "application",
				Labels:      map[string]string{"environment": "prod"},
				CreatedAt:   time.Date(2026, time.October, 29, 12, 30, 0, 0, time.UTC),
			},
			"pool:child-1:fedcba9876543210fedcba9876543210": {
				Request: Request{
					Kind: "explicit",
					CIDR: "10.2.0.0/24",
				},
				CIDR:      "10.2.0.0/24",
				Labels:    map[string]string{},
				CreatedAt: time.Date(2026, time.October, 29, 12, 31, 0, 0, time.UTC),
			},
		},
	}

	encoded, err := EncodeReservations(given)
	if err != nil {
		t.Fatalf("EncodeReservations() unexpected error: %v", err)
	}

	decoded, err := DecodeReservations(encoded, "prod-main")
	if err != nil {
		t.Fatalf("DecodeReservations() unexpected error: %v", err)
	}

	reencoded, err := EncodeReservations(decoded)
	if err != nil {
		t.Fatalf("EncodeReservations() after decode unexpected error: %v", err)
	}

	if string(reencoded) != string(encoded) {
		t.Fatalf("reservations round trip changed bytes:\nfirst:\n%s\nsecond:\n%s", encoded, reencoded)
	}
}

func TestCodecGoldenFilesRoundTripByteIdentically(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		file   string
		decode func([]byte) ([]byte, error)
	}{
		{
			name: "pool v1",
			file: "pool_v1.json",
			decode: func(data []byte) ([]byte, error) {
				decoded, err := DecodePool(data, "prod-main")
				if err != nil {
					return nil, err
				}

				return EncodePool(decoded)
			},
		},
		{
			name: "reservations v1",
			file: "reservations_v1.json",
			decode: func(data []byte) ([]byte, error) {
				decoded, err := DecodeReservations(data, "prod-main")
				if err != nil {
					return nil, err
				}

				return EncodeReservations(decoded)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			golden, err := os.ReadFile(filepath.Join("testdata", test.file))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			encoded, err := test.decode(golden)
			if err != nil {
				t.Fatalf("round trip golden: %v", err)
			}

			if string(encoded) != string(golden) {
				t.Fatalf("golden changed after round trip:\nwant:\n%s\ngot:\n%s", golden, encoded)
			}
		})
	}
}

func TestCodecRejectsInvalidEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		data       string
		decodePool bool
		wantErr    error
	}{
		{
			name:       "unsupported pool schema",
			data:       `{"schema_version":2,"kind":"pool","pool_id":"prod-main"}`,
			decodePool: true,
			wantErr:    ErrUnsupportedSchema,
		},
		{
			name:       "missing schema",
			data:       `{"kind":"pool","pool_id":"prod-main"}`,
			decodePool: true,
			wantErr:    ErrInvalidDocument,
		},
		{
			name:       "wrong pool kind",
			data:       `{"schema_version":1,"kind":"reservations","pool_id":"prod-main"}`,
			decodePool: true,
			wantErr:    ErrInvalidDocument,
		},
		{
			name:       "mismatched pool key",
			data:       `{"schema_version":1,"kind":"pool","pool_id":"other"}`,
			decodePool: true,
			wantErr:    ErrInvalidDocument,
		},
		{
			name:    "unsupported reservations schema",
			data:    `{"schema_version":2,"kind":"reservations","pool_id":"prod-main"}`,
			wantErr: ErrUnsupportedSchema,
		},
		{
			name:    "truncated reservations JSON",
			data:    `{"schema_version":1,"kind":"reservations"`,
			wantErr: ErrInvalidDocument,
		},
		{
			name:    "wrong reservations kind",
			data:    `{"schema_version":1,"kind":"pool","pool_id":"prod-main"}`,
			wantErr: ErrInvalidDocument,
		},
		{
			name:    "mismatched reservations key",
			data:    `{"schema_version":1,"kind":"reservations","pool_id":"other"}`,
			wantErr: ErrInvalidDocument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var err error
			if test.decodePool {
				_, err = DecodePool([]byte(test.data), "prod-main")
			} else {
				_, err = DecodeReservations([]byte(test.data), "prod-main")
			}

			if !errors.Is(err, test.wantErr) {
				t.Fatalf("decode error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
