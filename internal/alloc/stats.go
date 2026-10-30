// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package alloc

import (
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"sort"

	"go4.org/netipx"
)

// BlockCount is the number of maximal free blocks with the given prefix length.
type BlockCount struct{ Bits, Count int }

// Stats describes the free space of a set of parents.
type Stats struct {
	// Total and Free are address counts. Free is computed per parent, so
	// adjacent parents never merge into a larger block.
	Total, Free *big.Int
	// Largest is the free block with the fewest Bits, lowest address on ties;
	// it is the zero value when nothing is free.
	Largest netip.Prefix
	// Histogram lists maximal free blocks by ascending Bits, omitting empty
	// lengths.
	Histogram []BlockCount
	// ReferenceLength is the requested reference prefix length; 0 means none.
	ReferenceLength int
	// Allocatable is the number of reference-sized prefixes obtainable from
	// whole free blocks; nil without a reference length.
	Allocatable *big.Int
	// Capacity is the number of reference-sized prefixes if the free space were
	// perfectly packed; nil without a reference length.
	Capacity *big.Int
	// Index is the fragmentation index 1 - Allocatable/Capacity; nil without a
	// reference length or when Capacity is zero.
	Index *big.Rat
}

// ComputeStats measures fragmentation of the free space left in parents after
// removing used. A referenceLength of 0 disables the reference metrics;
// otherwise it must lie within [shortest parent length, address width].
// Reference metrics require every parent to share one address family.
func ComputeStats(parents, used []netip.Prefix, referenceLength int) (Stats, error) {
	if !validPrefixes(parents) || !validPrefixes(used) {
		return Stats{}, fmt.Errorf("compute stats prefixes: %w", ErrInvalidCIDR)
	}

	width, err := referenceWidth(parents, referenceLength)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{
		Total:           new(big.Int),
		Free:            new(big.Int),
		ReferenceLength: referenceLength,
	}
	counts := map[int]int{}
	allocatable := new(big.Int)

	for _, parent := range parents {
		parent = parent.Masked()
		stats.Total.Add(stats.Total, blockSize(parent))

		var builder netipx.IPSetBuilder
		builder.AddPrefix(parent)

		for _, existing := range used {
			builder.RemovePrefix(existing.Masked())
		}

		free, err := builder.IPSet()
		if err != nil {
			return Stats{}, fmt.Errorf("compute stats free space: %w", errors.Join(ErrInvalidCIDR, err))
		}

		for _, block := range free.Prefixes() {
			stats.Free.Add(stats.Free, blockSize(block))
			counts[block.Bits()]++

			if !stats.Largest.IsValid() || block.Bits() < stats.Largest.Bits() ||
				(block.Bits() == stats.Largest.Bits() && block.Addr().Less(stats.Largest.Addr())) {
				stats.Largest = block
			}

			if referenceLength != 0 && block.Bits() <= referenceLength {
				allocatable.Add(allocatable, new(big.Int).Lsh(big.NewInt(1), uint(referenceLength-block.Bits())))
			}
		}
	}

	for bits, count := range counts {
		stats.Histogram = append(stats.Histogram, BlockCount{Bits: bits, Count: count})
	}

	sort.Slice(stats.Histogram, func(i, j int) bool { return stats.Histogram[i].Bits < stats.Histogram[j].Bits })

	if referenceLength == 0 {
		return stats, nil
	}

	stats.Allocatable = allocatable
	stats.Capacity = new(big.Int).Rsh(stats.Free, uint(width-referenceLength))

	if stats.Capacity.Sign() > 0 {
		stats.Index = new(big.Rat).Sub(big.NewRat(1, 1), new(big.Rat).SetFrac(allocatable, stats.Capacity))
	}

	return stats, nil
}

// referenceWidth validates referenceLength and returns the shared address width.
func referenceWidth(parents []netip.Prefix, referenceLength int) (int, error) {
	if referenceLength == 0 {
		return 0, nil
	}

	if len(parents) == 0 {
		return 0, fmt.Errorf("compute stats reference length: %w", ErrInvalidArgument)
	}

	width := parents[0].Addr().BitLen()
	shortest := parents[0].Bits()

	for _, parent := range parents[1:] {
		if parent.Addr().BitLen() != width {
			return 0, fmt.Errorf("compute stats mixed address families: %w", ErrInvalidArgument)
		}

		shortest = min(shortest, parent.Bits())
	}

	if referenceLength < shortest || referenceLength > width {
		return 0, fmt.Errorf("compute stats reference length: %w", ErrInvalidArgument)
	}

	return width, nil
}

func blockSize(prefix netip.Prefix) *big.Int {
	return new(big.Int).Lsh(big.NewInt(1), uint(prefix.Addr().BitLen()-prefix.Bits()))
}
