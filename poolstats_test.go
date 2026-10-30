// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/appvia/ipam/storage/memory"
)

func TestPoolStatsFragmentation(t *testing.T) {
	t.Parallel()

	base := memory.New()
	recorder := &recordingStore{Storage: base}
	client := newTestClient(recorder)
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	for index, length := range []int{24, 24, 26} {
		_, err := client.Allocate(t.Context(), "prod", AllocationSpec{
			ID:           "0190a000-0000-7000-8000-00000000000" + string(rune('1'+index)),
			PrefixLength: length,
		})
		assertNoError(t, err)
	}

	before := len(recorder.writeKeys())

	stats, err := client.PoolStats(t.Context(), "prod", 0)
	assertNoError(t, err)

	if after := len(recorder.writeKeys()); after != before {
		t.Fatalf("PoolStats() performed %d writes", after-before)
	}

	// 65536 - 256 - 256 - 64 addresses remain free.
	if stats.Free.Int64() != 65536-576 || stats.Total.Int64() != 65536 {
		t.Fatalf("Free/Total = %v/%v", stats.Free, stats.Total)
	}

	if !stats.Largest.IsValid() || stats.Largest.Bits() > 17 {
		t.Fatalf("Largest = %v, want a block of /17 or larger", stats.Largest)
	}

	if stats.ReferenceLength != 24 || stats.Allocations != 3 || stats.Cooling != 0 {
		t.Fatalf("ReferenceLength/Allocations/Cooling = %d/%d/%d", stats.ReferenceLength, stats.Allocations, stats.Cooling)
	}

	if stats.Capacity == nil || stats.Allocatable == nil || stats.FragmentationIndex == nil {
		t.Fatalf("reference metrics missing: %+v", stats)
	}
}

func TestPoolStatsReferenceTieBreaksToSmallerLength(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	for index, length := range []int{26, 24, 25} {
		_, err := client.Allocate(t.Context(), "prod", AllocationSpec{
			ID:           "0190a000-0000-7000-8000-00000000000" + string(rune('1'+index)),
			PrefixLength: length,
		})
		assertNoError(t, err)
	}

	stats, err := client.PoolStats(t.Context(), "prod", 0)
	assertNoError(t, err)

	if stats.ReferenceLength != 24 {
		t.Fatalf("ReferenceLength = %d, want 24", stats.ReferenceLength)
	}

	explicit, err := client.PoolStats(t.Context(), "prod", 28)
	assertNoError(t, err)

	if explicit.ReferenceLength != 28 {
		t.Fatalf("explicit ReferenceLength = %d, want 28", explicit.ReferenceLength)
	}
}

func TestPoolStatsNoReferenceWithoutNextAllocations(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	_, err := client.Allocate(t.Context(), "prod", AllocationSpec{
		ID:   testAllocationID,
		CIDR: netip.MustParsePrefix("10.0.1.0/24"),
	})
	assertNoError(t, err)

	stats, err := client.PoolStats(t.Context(), "prod", 0)
	assertNoError(t, err)

	if stats.ReferenceLength != 0 || stats.Allocatable != nil || stats.Capacity != nil || stats.FragmentationIndex != nil {
		t.Fatalf("unexpected reference metrics: %+v", stats)
	}
}

func TestPoolStatsErrors(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	if _, err := client.PoolStats(t.Context(), "missing", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown pool error = %v, want ErrNotFound", err)
	}

	for _, length := range []int{40, -1, 8, 33} {
		if _, err := client.PoolStats(t.Context(), "prod", length); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("reference %d error = %v, want ErrInvalidArgument", length, err)
		}
	}

	if _, err := client.PoolStats(t.Context(), "Bad ID", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad pool ID error = %v, want ErrInvalidArgument", err)
	}
}

func TestPoolStatsCountsOnlyUnexpiredCooling(t *testing.T) {
	t.Parallel()

	base := memory.New()
	now := time.Unix(1000, 0).UTC()
	recorder := &recordingStore{Storage: base}
	client := New(recorder, WithClock(func() time.Time { return now }), WithSleeper(noSleep))

	_, err := client.CreatePool(t.Context(), PoolSpec{
		ID:     "prod",
		CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
		Policy: PoolPolicy{Cooldown: Cooldown{Mode: CooldownStrict, Duration: time.Hour}},
	})
	assertNoError(t, err)

	poolData, _, err := base.Get(t.Context(), poolKey("prod"))
	assertNoError(t, err)
	pool, err := decodePoolDocument(poolData, "prod")
	assertNoError(t, err)

	reservationsData, version, err := base.Get(t.Context(), reservationsKey("prod"))
	assertNoError(t, err)
	reservations, err := decodeReservationsDocument(reservationsData, "prod")
	assertNoError(t, err)

	reservations.Incarnation = pool.Incarnation
	reservations.Released = map[netip.Prefix]time.Time{
		netip.MustParsePrefix("10.0.1.0/24"): now.Add(time.Minute),
		netip.MustParsePrefix("10.0.2.0/24"): now.Add(-time.Minute),
		netip.MustParsePrefix("10.0.3.0/24"): now,
	}

	data, err := encodeReservationsDocument(reservations)
	assertNoError(t, err)
	_, err = base.PutIfMatch(t.Context(), reservationsKey("prod"), data, version)
	assertNoError(t, err)

	stats, err := client.PoolStats(t.Context(), "prod", 0)
	assertNoError(t, err)

	if stats.Cooling != 1 {
		t.Fatalf("Cooling = %d, want 1 (expired and boundary records excluded)", stats.Cooling)
	}

	if stats.Policy.Cooldown.Mode != CooldownStrict {
		t.Fatalf("Policy.Cooldown = %+v", stats.Policy.Cooldown)
	}

	if writes := recorder.writeKeys(); len(writes) != 2 {
		t.Fatalf("writes = %v, want only the two CreatePool writes", writes)
	}

	now = now.Add(time.Hour)

	later, err := client.PoolStats(t.Context(), "prod", 0)
	assertNoError(t, err)

	if later.Cooling != 0 {
		t.Fatalf("Cooling after expiry = %d, want 0", later.Cooling)
	}

	// Pruning is never persisted: the stored document keeps every record.
	persisted, _, err := base.Get(t.Context(), reservationsKey("prod"))
	assertNoError(t, err)
	stored, err := decodeReservationsDocument(persisted, "prod")
	assertNoError(t, err)

	if len(stored.Released) != 3 {
		t.Fatalf("stored Released = %d records, want 3", len(stored.Released))
	}
}
