// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestCooldownUntilManyExpiriesRequiresLastRecord(t *testing.T) {
	// Given 256 distinct expiries, each blocking part of the only fitting /24.
	client, base, clock := newCooldownClient(t, CooldownStrict)
	reservations := readCooldownReservations(t, base)
	reservations.Released = make(map[netip.Prefix]time.Time)
	addr := netip.MustParseAddr("10.0.0.0")

	for index := range 256 {
		reservations.Released[netip.PrefixFrom(addr, 32)] = clock.now().Add(time.Duration(index+1) * time.Second)
		addr = addr.Next()
	}

	writeCooldownReservations(t, base, reservations)

	// When strict selection needs the entire parent.
	_, err := client.Allocate(t.Context(), "root", AllocationSpec{ID: testAllocationID, PrefixLength: 24})

	// Then the first fitting expiry is the last one, rounded up to a second.
	assertCooldownError(t, err, "allocate", "root", testAllocationID)

	want := clock.now().Add(256 * time.Second).Truncate(time.Second).Add(time.Second)
	if !strings.Contains(err.Error(), want.Format(time.RFC3339)) {
		t.Fatalf("error = %v, want last fitting expiry %s", err, want)
	}
}
