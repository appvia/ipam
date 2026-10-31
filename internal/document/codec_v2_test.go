// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package document

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodecV2DecodesFields(t *testing.T) {
	t.Parallel()

	poolData, err := os.ReadFile(filepath.Join("testdata", "pool_v2.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	pool, err := DecodePool(poolData, "prod-main")
	if err != nil {
		t.Fatalf("DecodePool() unexpected error: %v", err)
	}

	if pool.SchemaVersion != SchemaVersionV2 || pool.Strategy != "best-fit" {
		t.Fatalf("DecodePool() = version %d strategy %q, want 2 best-fit", pool.SchemaVersion, pool.Strategy)
	}

	reservationsData, err := os.ReadFile(filepath.Join("testdata", "reservations_v2.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	reservations, err := DecodeReservations(reservationsData, "prod-main")
	if err != nil {
		t.Fatalf("DecodeReservations() unexpected error: %v", err)
	}

	if reservations.SchemaVersion != SchemaVersionV2 {
		t.Fatalf("DecodeReservations() version = %d, want 2", reservations.SchemaVersion)
	}

	if reservations.Cooldown == nil || reservations.Cooldown.Mode != "strict" || reservations.Cooldown.Seconds != 86400 {
		t.Fatalf("DecodeReservations() cooldown = %#v, want strict 86400", reservations.Cooldown)
	}

	if got := reservations.Released["10.0.1.0/24"]; got != "2026-10-30T12:30:00Z" {
		t.Fatalf("DecodeReservations() released = %#v", reservations.Released)
	}
}

func TestCodecV2NullCooldownRoundTrips(t *testing.T) {
	t.Parallel()

	given := Reservations{
		SchemaVersion: SchemaVersionV2,
		Kind:          "reservations",
		PoolID:        "prod-main",
		Incarnation:   "0123456789abcdef0123456789abcdef",
		Allocations:   map[string]Allocation{},
		Released:      map[string]string{},
	}

	encoded, err := EncodeReservations(given)
	if err != nil {
		t.Fatalf("EncodeReservations() unexpected error: %v", err)
	}

	assertContains(t, encoded, `"cooldown": null`, `"released": {}`)

	decoded, err := DecodeReservations(encoded, "prod-main")
	if err != nil {
		t.Fatalf("DecodeReservations() unexpected error: %v", err)
	}

	if decoded.Cooldown != nil || decoded.Released == nil {
		t.Fatalf("DecodeReservations() = cooldown %#v released %#v", decoded.Cooldown, decoded.Released)
	}
}

func TestCodecV1BytesUnchangedByV2Fields(t *testing.T) {
	t.Parallel()

	data, err := EncodePool(Pool{
		SchemaVersion: SchemaVersionV1,
		Kind:          "pool",
		PoolID:        "prod-main",
		Incarnation:   "0123456789abcdef0123456789abcdef",
		Family:        4,
		CIDRs:         []string{"10.0.0.0/16"},
	})
	if err != nil {
		t.Fatalf("EncodePool() unexpected error: %v", err)
	}

	assertForbiddenFieldsAbsent(t, data, `"strategy"`)

	data, err = EncodeReservations(Reservations{
		SchemaVersion: SchemaVersionV1,
		Kind:          "reservations",
		PoolID:        "prod-main",
		Incarnation:   "0123456789abcdef0123456789abcdef",
		Allocations:   map[string]Allocation{},
	})
	if err != nil {
		t.Fatalf("EncodeReservations() unexpected error: %v", err)
	}

	assertForbiddenFieldsAbsent(t, data, `"cooldown"`, `"released"`)
}

func TestCodecEncodeRejectsInconsistentVersions(t *testing.T) {
	t.Parallel()

	pool := Pool{
		SchemaVersion: SchemaVersionV1,
		Kind:          "pool",
		PoolID:        "prod-main",
		Incarnation:   "0123456789abcdef0123456789abcdef",
		Family:        4,
		CIDRs:         []string{"10.0.0.0/16"},
	}
	reservations := Reservations{
		SchemaVersion: SchemaVersionV1,
		Kind:          "reservations",
		PoolID:        "prod-main",
		Incarnation:   "0123456789abcdef0123456789abcdef",
		Allocations:   map[string]Allocation{},
	}

	tests := []struct {
		name   string
		encode func() error
	}{
		{"v1 pool with strategy", func() error {
			given := pool
			given.Strategy = "lowest"
			_, err := EncodePool(given)

			return err
		}},
		{"v2 pool without strategy", func() error {
			given := pool
			given.SchemaVersion = SchemaVersionV2
			_, err := EncodePool(given)

			return err
		}},
		{"v1 reservations with cooldown", func() error {
			given := reservations
			given.Cooldown = &Cooldown{Mode: "strict", Seconds: 60}
			_, err := EncodeReservations(given)

			return err
		}},
		{"v1 reservations with released", func() error {
			given := reservations
			given.Released = map[string]string{}
			_, err := EncodeReservations(given)

			return err
		}},
		{"v2 reservations with nil released", func() error {
			given := reservations
			given.SchemaVersion = SchemaVersionV2
			_, err := EncodeReservations(given)

			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if err := test.encode(); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("encode error = %v, want %v", err, ErrInvalidDocument)
			}
		})
	}

	t.Run("schema 3", func(t *testing.T) {
		t.Parallel()

		given := pool
		given.SchemaVersion = 3

		if _, err := EncodePool(given); !errors.Is(err, ErrUnsupportedSchema) {
			t.Fatalf("EncodePool() error = %v, want %v", err, ErrUnsupportedSchema)
		}
	})
}

func assertContains(t *testing.T, data []byte, fragments ...string) {
	t.Helper()

	for _, fragment := range fragments {
		if !strings.Contains(string(data), fragment) {
			t.Fatalf("encoded document missing %s:\n%s", fragment, data)
		}
	}
}
