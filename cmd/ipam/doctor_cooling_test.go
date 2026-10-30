// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/appvia/ipam"
	"github.com/appvia/ipam/storage/memory"
)

func TestDoctorFragmentationReportsCoolingSeparately(t *testing.T) {
	// Given one free /25 that is still cooling and one allocated /25.
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	client := ipam.New(memory.New(), ipam.WithClock(func() time.Time { return now }))

	_, err := client.CreatePool(t.Context(), ipam.PoolSpec{
		ID: "cooling", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
		Policy: ipam.PoolPolicy{Cooldown: ipam.Cooldown{Mode: ipam.CooldownStrict, Duration: time.Hour}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Allocate(t.Context(), "cooling", ipam.AllocationSpec{PrefixLength: 25}); err != nil {
		t.Fatal(err)
	}

	released, err := client.Allocate(t.Context(), "cooling", ipam.AllocationSpec{PrefixLength: 25})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.Release(t.Context(), "cooling", released.ID); err != nil {
		t.Fatal(err)
	}

	// When doctor computes fragmentation, independently of strict selection.
	check := checkFragmentation(t.Context(), client, "cooling")

	// Then allocatable space is unchanged and the cooling count is separate.
	if check.Status != statusPass || check.Stats == nil || check.Stats.Allocatable != "1" ||
		check.Stats.Capacity != "1" || check.Stats.FragmentationIndex != "0.00" ||
		!strings.HasSuffix(check.Detail, ", cooling 1") {
		t.Fatalf("fragmentation = %+v, want one allocatable and one cooling range", check)
	}

	_, err = client.Allocate(t.Context(), "cooling", ipam.AllocationSpec{PrefixLength: 25})
	if !errors.Is(err, ipam.ErrPoolExhausted) {
		t.Fatalf("strict Allocate() = %v, want ErrPoolExhausted despite allocatable > 0", err)
	}

	now = now.Add(time.Hour)

	check = checkFragmentation(t.Context(), client, "cooling")
	if strings.Contains(check.Detail, "cooling") {
		t.Fatalf("expired cooldown still reported: %s", check.Detail)
	}
}
