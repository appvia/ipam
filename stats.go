// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"math/big"
	"net/netip"

	"github.com/appvia/ipam/internal/alloc"
)

// FreeBlockCount is the number of maximal free blocks with one prefix length.
type FreeBlockCount struct{ PrefixLength, Count int }

// PoolStats describes a pool's usage and the fragmentation of its free space.
type PoolStats struct {
	PoolID string
	Policy PoolPolicy
	// Allocations counts the pool's allocation records; Cooling counts released
	// CIDRs whose cooldown has not expired at the client's clock.
	Allocations, Cooling int
	// Total and Free are address counts.
	Total, Free *big.Int
	// Largest is the largest free block; the zero value when nothing is free.
	Largest netip.Prefix
	// Histogram lists maximal free blocks by ascending prefix length.
	Histogram []FreeBlockCount
	// ReferenceLength is the prefix length used for the reference metrics, 0 for none.
	ReferenceLength int
	// Allocatable is the number of reference-sized prefixes obtainable from whole
	// free blocks; nil without a reference length.
	Allocatable *big.Int
	// Capacity is the number of reference-sized prefixes if the free space were
	// perfectly packed; nil without a reference length.
	Capacity *big.Int
	// FragmentationIndex is 1 - Allocatable/Capacity; nil without a reference
	// length or when Capacity is zero.
	FragmentationIndex *big.Rat
}

// PoolStats reports usage and fragmentation of a pool without writing to
// storage. A referenceLength of 0 uses the most frequent prefix length among
// the pool's RequestNext allocations (the smaller length on ties), or no
// reference when there are none. Otherwise it must lie between the pool's
// shortest CIDR length and the address width, else ErrInvalidArgument.
func (client *Client) PoolStats(
	ctx context.Context,
	poolID string,
	referenceLength int,
) (PoolStats, error) {
	if err := validateClient(client); err != nil {
		return PoolStats{}, err
	}

	if err := validatePoolID(poolID); err != nil {
		return PoolStats{}, &OpError{Op: "stats", Pool: poolID, Err: err}
	}

	pool, reservations, _, err := client.readPoolState(ctx, "stats", poolID)
	if err != nil {
		return PoolStats{}, err
	}

	if referenceLength == 0 {
		referenceLength = mostFrequentPrefixLength(reservations.Allocations)
	}

	used := make([]netip.Prefix, 0, len(reservations.Allocations))
	for _, allocation := range reservations.Allocations {
		used = append(used, allocation.CIDR)
	}

	computed, err := alloc.ComputeStats(pool.CIDRs, used, referenceLength)
	if err != nil {
		switch {
		case errors.Is(err, alloc.ErrInvalidArgument):
			err = ErrInvalidArgument
		case errors.Is(err, alloc.ErrInvalidCIDR):
			err = ErrInvalidCIDR
		}

		return PoolStats{}, &OpError{Op: "stats", Pool: poolID, Err: err}
	}

	now := client.clock()
	cooling := 0

	for _, expiry := range reservations.Released {
		if expiry.After(now) {
			cooling++
		}
	}

	policy := pool.Policy
	policy.Cooldown = reservations.Cooldown

	histogram := make([]FreeBlockCount, 0, len(computed.Histogram))
	for _, block := range computed.Histogram {
		histogram = append(histogram, FreeBlockCount{PrefixLength: block.Bits, Count: block.Count})
	}

	return PoolStats{
		PoolID:             poolID,
		Policy:             policy,
		Allocations:        len(reservations.Allocations),
		Cooling:            cooling,
		Total:              computed.Total,
		Free:               computed.Free,
		Largest:            computed.Largest,
		Histogram:          histogram,
		ReferenceLength:    computed.ReferenceLength,
		Allocatable:        computed.Allocatable,
		Capacity:           computed.Capacity,
		FragmentationIndex: computed.Index,
	}, nil
}

// mostFrequentPrefixLength returns the most common PrefixLength among
// RequestNext allocations, preferring the smaller length on ties; 0 if none.
func mostFrequentPrefixLength(allocations map[string]Allocation) int {
	counts := map[int]int{}

	for _, allocation := range allocations {
		if allocation.Request.Kind == RequestNext {
			counts[allocation.Request.PrefixLength]++
		}
	}

	best, bestCount := 0, 0

	for length, count := range counts {
		if count > bestCount || (count == bestCount && length < best) {
			best, bestCount = length, count
		}
	}

	return best
}
