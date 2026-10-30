// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/netip"
	"time"

	"github.com/appvia/ipam"
	"github.com/appvia/ipam/storage/memory"
)

// fixedIDs returns an IDGenerator that hands out ids in order, so example
// output is deterministic. Real callers keep the default UUIDv7 generator.
func fixedIDs(ids ...string) ipam.IDGenerator {
	return func() string {
		id := ids[0]
		ids = ids[1:]

		return id
	}
}

func ExampleOpen() {
	ctx := context.Background()

	// mem:// is an in-memory backend for tests. Production URLs are s3://,
	// azblob:// or file:///absolute/path.
	client, err := ipam.Open(ctx, "mem://")
	if err != nil {
		log.Fatal(err)
	}

	pool, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:    "global",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(pool.ID, pool.Family, pool.CIDRs)
	// Output: global 4 [10.0.0.0/8]
}

func ExampleClient_Allocate() {
	ctx := context.Background()
	client := ipam.New(memory.New(),
		ipam.WithIDGenerator(fixedIDs("018f22e2-1865-7be8-9f45-35d90607d0f7")),
		ipam.WithClock(func() time.Time { return time.Date(2026, 10, 29, 12, 30, 0, 0, time.UTC) }),
	)

	if _, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:    "prod",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
	}); err != nil {
		log.Fatal(err)
	}

	// An empty ID is generated once, before any retry.
	next, err := client.Allocate(ctx, "prod", ipam.AllocationSpec{PrefixLength: 24, Description: "app"})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(next.ID, next.CIDR, next.Request.Kind, next.CreatedAt.Format(time.RFC3339))

	// Repeating a request with the same ID returns the same allocation.
	again, err := client.Allocate(ctx, "prod", ipam.AllocationSpec{ID: next.ID, PrefixLength: 24, Description: "app"})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(again.CIDR == next.CIDR)

	// An explicit CIDR is a reservation of exactly that range.
	explicit, err := client.Allocate(ctx, "prod", ipam.AllocationSpec{
		ID:   "018f22e2-1865-7be8-9f45-35d90607d0f8",
		CIDR: netip.MustParsePrefix("10.0.200.0/24"),
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(explicit.CIDR, explicit.Request.Kind)
	// Output:
	// 018f22e2-1865-7be8-9f45-35d90607d0f7 10.0.0.0/24 next 2026-10-29T12:30:00Z
	// true
	// 10.0.200.0/24 explicit
}

func ExampleClient_CreatePool_child() {
	ctx := context.Background()
	client := ipam.New(memory.New())

	if _, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:    "global",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}); err != nil {
		log.Fatal(err)
	}

	// A child pool is carved from its parent as a pool:<child>:<incarnation>
	// allocation.
	child, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:         "prod",
		ParentPool: "global",
		Request:    ipam.Request{Kind: ipam.RequestNext, PrefixLength: 16},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(child.ID, child.CIDRs, child.Parent.PoolID)

	// The parent still has allocations, so it cannot be deleted yet.
	err = client.DeletePool(ctx, "global")

	var opErr *ipam.OpError
	if errors.As(err, &opErr) {
		fmt.Println(errors.Is(err, ipam.ErrConflict), opErr.Reason)
	}

	// Deleting the child releases its carve-out; then the parent can go.
	if err := client.DeletePool(ctx, "prod"); err != nil {
		log.Fatal(err)
	}

	fmt.Println(client.DeletePool(ctx, "global"))
	// Output:
	// prod [10.0.0.0/16] global
	// true pool_not_empty
	// <nil>
}

func ExampleClient_Release() {
	ctx := context.Background()
	client := ipam.New(memory.New())

	if _, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:    "small",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/24")},
	}); err != nil {
		log.Fatal(err)
	}

	first, err := client.Allocate(ctx, "small", ipam.AllocationSpec{PrefixLength: 24})
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.Allocate(ctx, "small", ipam.AllocationSpec{PrefixLength: 24})
	fmt.Println(errors.Is(err, ipam.ErrPoolExhausted))

	if err := client.Release(ctx, "small", first.ID); err != nil {
		log.Fatal(err)
	}

	// Releasing an ID that is no longer in the pool is a no-op.
	fmt.Println(client.Release(ctx, "small", first.ID))

	reused, err := client.Allocate(ctx, "small", ipam.AllocationSpec{PrefixLength: 24})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(reused.CIDR)
	// Output:
	// true
	// <nil>
	// 192.168.0.0/24
}

// A long-running caller such as a Terraform provider retries CAS conflicts
// until its own deadline instead of giving up after the default 8 attempts.
func ExampleWithMaxAttempts() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := ipam.New(memory.New(), ipam.WithMaxAttempts(0))

	pool, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:    "v6",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("fd00::/48")},
	})
	if err != nil {
		log.Fatal(err)
	}

	allocation, err := client.Allocate(ctx, pool.ID, ipam.AllocationSpec{PrefixLength: 64})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(pool.Family, allocation.CIDR)
	// Output: 6 fd00::/64
}

// Both pools hold the same used block, 10.0.0.128/26. The lowest pool takes
// the bottom free run; best fit takes the tight /26 hole at the top.
func ExampleClient_CreatePool_bestFit() {
	ctx := context.Background()
	client := ipam.New(memory.New())

	pools := []struct {
		id     string
		policy ipam.PoolPolicy
	}{
		{"lowest", ipam.PoolPolicy{Strategy: ipam.StrategyLowest}},
		{"best", ipam.PoolPolicy{Strategy: ipam.StrategyBestFit}},
	}

	for _, p := range pools {
		pool, err := client.CreatePool(ctx, ipam.PoolSpec{
			ID:     p.id,
			CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
			Policy: p.policy,
		})
		if err != nil {
			log.Fatal(err)
		}

		if _, err := client.Allocate(ctx, pool.ID, ipam.AllocationSpec{CIDR: netip.MustParsePrefix("10.0.0.128/26")}); err != nil {
			log.Fatal(err)
		}

		next, err := client.Allocate(ctx, pool.ID, ipam.AllocationSpec{PrefixLength: 26})
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(pool.ID, pool.Policy.Strategy, next.CIDR)
	}
	// Output:
	// lowest lowest 10.0.0.0/26
	// best best-fit 10.0.0.192/26
}

// A released CIDR is held back for the strict cooldown. The fake clock makes
// the refusal and the expiry deterministic.
func ExampleClient_Release_cooldown() {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	client := ipam.New(memory.New(), ipam.WithClock(func() time.Time { return now }))

	if _, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:    "small",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/24")},
		Policy: ipam.PoolPolicy{
			Cooldown: ipam.Cooldown{Mode: ipam.CooldownStrict, Duration: time.Hour},
		},
	}); err != nil {
		log.Fatal(err)
	}

	first, err := client.Allocate(ctx, "small", ipam.AllocationSpec{PrefixLength: 24})
	if err != nil {
		log.Fatal(err)
	}

	if err := client.Release(ctx, "small", first.ID); err != nil {
		log.Fatal(err)
	}

	_, err = client.Allocate(ctx, "small", ipam.AllocationSpec{PrefixLength: 24})

	var opErr *ipam.OpError
	if !errors.As(err, &opErr) {
		log.Fatal(err)
	}

	fmt.Println(errors.Is(err, ipam.ErrPoolExhausted), opErr.Reason)

	now = now.Add(time.Hour)

	reused, err := client.Allocate(ctx, "small", ipam.AllocationSpec{PrefixLength: 24})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(reused.CIDR)
	// Output:
	// true cooldown
	// 192.168.0.0/24
}

func ExampleClient_UpdatePoolPolicy() {
	ctx := context.Background()
	client := ipam.New(memory.New())

	if _, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:    "edge",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
	}); err != nil {
		log.Fatal(err)
	}

	if err := client.UpdatePoolPolicy(ctx, "edge", ipam.PoolPolicy{
		Strategy: ipam.StrategyLowest,
		Cooldown: ipam.Cooldown{Mode: ipam.CooldownFitAware, Duration: 30 * time.Minute},
	}); err != nil {
		log.Fatal(err)
	}

	stats, err := client.PoolStats(ctx, "edge", 0)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("strategy=%s cooldown=%q %s\n", stats.Policy.Strategy, stats.Policy.Cooldown.Mode, stats.Policy.Cooldown.Duration)

	// Omitting the cooldown removes it; the change applies to the next allocation.
	if err := client.UpdatePoolPolicy(ctx, "edge", ipam.PoolPolicy{Strategy: ipam.StrategyLowest}); err != nil {
		log.Fatal(err)
	}

	stats, err = client.PoolStats(ctx, "edge", 0)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("strategy=%s cooldown=%q %s\n", stats.Policy.Strategy, stats.Policy.Cooldown.Mode, stats.Policy.Cooldown.Duration)
	// Output:
	// strategy=lowest cooldown="fit-aware" 30m0s
	// strategy=lowest cooldown="" 0s
}

// Two used blocks leave 256 free addresses split into two /25 runs, so no /24
// fits: the pool is half used, and the free space is fragmented.
func ExampleClient_PoolStats() {
	ctx := context.Background()
	client := ipam.New(memory.New())

	if _, err := client.CreatePool(ctx, ipam.PoolSpec{
		ID:    "prod",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/23")},
	}); err != nil {
		log.Fatal(err)
	}

	for _, cidr := range []string{"10.0.0.0/25", "10.0.1.128/25"} {
		if _, err := client.Allocate(ctx, "prod", ipam.AllocationSpec{CIDR: netip.MustParsePrefix(cidr)}); err != nil {
			log.Fatal(err)
		}
	}

	stats, err := client.PoolStats(ctx, "prod", 24)
	if err != nil {
		log.Fatal(err)
	}

	used := new(big.Int).Sub(stats.Total, stats.Free)
	utilisation := new(big.Rat).SetFrac(used, stats.Total)

	fmt.Println("utilisation", utilisation.FloatString(2))
	fmt.Println("largest free", stats.Largest)
	fmt.Println("allocatable /24", stats.Allocatable)
	fmt.Println("fragmentation index", stats.FragmentationIndex.FloatString(2))
	// Output:
	// utilisation 0.50
	// largest free 10.0.0.128/25
	// allocatable /24 0
	// fragmentation index 1.00
}
