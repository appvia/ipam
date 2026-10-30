// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package alloc provides deterministic, in-memory CIDR allocation.
package alloc

import (
	"errors"
	"fmt"
	"net/netip"

	"go4.org/netipx"
)

var (
	// ErrInvalidCIDR reports an invalid prefix, prefix length, or containment.
	ErrInvalidCIDR = errors.New("alloc: invalid CIDR")
	// ErrExhausted reports that no prefix of the requested length is available.
	ErrExhausted = errors.New("alloc: pool exhausted")
	// ErrOverlap reports that an explicit prefix intersects an existing allocation.
	ErrOverlap = errors.New("alloc: CIDR overlaps an existing allocation")
	// ErrInvalidArgument reports an unknown strategy or cooldown mode.
	ErrInvalidArgument = errors.New("alloc: invalid argument")
	// ErrCooling reports that only cooling space fits the requested length.
	ErrCooling = errors.New("alloc: only cooling space fits")
)

// Strategy determines how free blocks are ranked.
type Strategy uint8

const (
	StrategyLowest Strategy = iota
	StrategyBestFit
)

// CooldownMode determines whether cooling prefixes may be reused.
type CooldownMode uint8

const (
	CooldownNone CooldownMode = iota
	CooldownStrict
	CooldownFitAware
)

// Input describes a selection without changing any of its prefix slices.
type Input struct {
	Parents, Used, Cooling []netip.Prefix
	Length                 int
	Strategy               Strategy
	CooldownMode           CooldownMode
}

// Next is the lowest-address compatibility wrapper for Select, without cooldown.
func Next(parents, used []netip.Prefix, length int) (netip.Prefix, error) {
	return Select(Input{Parents: parents, Used: used, Length: length})
}

// Select returns a prefix from one eligible parent using the requested strategy.
// Best fit selects the tightest maximal free block, with lowest-address ties.
// Strict excludes cooling space; fit-aware avoids it only in an original free
// block of the same size as the choice without cooldown.
//
// IPv4-mapped IPv6 addresses are rejected rather than implicitly unmapped, so
// callers must choose an unambiguous address family before allocation.
func Select(in Input) (netip.Prefix, error) {
	if !validPrefixes(in.Parents) || !validPrefixes(in.Used) || !validPrefixes(in.Cooling) {
		return netip.Prefix{}, fmt.Errorf("select prefixes: %w", ErrInvalidCIDR)
	}

	switch in.Strategy {
	case StrategyLowest, StrategyBestFit:
	default:
		return netip.Prefix{}, fmt.Errorf("select strategy: %w", ErrInvalidArgument)
	}

	switch in.CooldownMode {
	case CooldownNone, CooldownStrict, CooldownFitAware:
	default:
		return netip.Prefix{}, fmt.Errorf("select cooldown mode: %w", ErrInvalidArgument)
	}

	var freeBlocks, cleanBlocks []netip.Prefix

	var originalBits []int

	eligible := false

	for _, parent := range in.Parents {
		if in.Length < parent.Bits() || in.Length > parent.Addr().BitLen() {
			continue
		}

		eligible = true

		var builder netipx.IPSetBuilder
		builder.AddPrefix(parent.Masked())

		for _, existing := range in.Used {
			builder.RemovePrefix(existing.Masked())
		}

		free, err := builder.IPSet()
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("select free space: %w", errors.Join(ErrInvalidCIDR, err))
		}

		blocks := free.Prefixes()
		freeBlocks = append(freeBlocks, blocks...)

		if in.CooldownMode == CooldownNone {
			continue
		}

		// Subtract cooling once per parent, retaining the original free blocks
		// so fit-aware can compare their sizes rather than the split pieces.
		for _, cooling := range in.Cooling {
			builder.RemovePrefix(cooling.Masked())
		}

		clean, err := builder.IPSet()
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("select non-cooling space: %w", errors.Join(ErrInvalidCIDR, err))
		}

		original := 0

		// Both prefix lists are address-sorted, and subtraction cannot join
		// separate free blocks, so every clean block has one containing block.
		for _, block := range clean.Prefixes() {
			for !blocks[original].Contains(block.Addr()) {
				original++
			}

			cleanBlocks = append(cleanBlocks, block)
			originalBits = append(originalBits, blocks[original].Bits())
		}
	}

	if !eligible {
		return netip.Prefix{}, fmt.Errorf("select length: %w", ErrInvalidCIDR)
	}

	best, bestBits := choose(freeBlocks, in.Length, in.Strategy)
	if !best.IsValid() {
		return netip.Prefix{}, fmt.Errorf("select free space: %w", ErrExhausted)
	}

	if in.CooldownMode == CooldownNone {
		return best, nil
	}

	if in.CooldownMode == CooldownFitAware {
		fitting := cleanBlocks[:0]

		for index, block := range cleanBlocks {
			if originalBits[index] == bestBits {
				fitting = append(fitting, block)
			}
		}

		cleanBlocks = fitting
	}

	clean, _ := choose(cleanBlocks, in.Length, in.Strategy)
	if clean.IsValid() {
		return clean, nil
	}

	if in.CooldownMode == CooldownStrict {
		return netip.Prefix{}, fmt.Errorf("select non-cooling space: %w", ErrCooling)
	}

	return best, nil
}

func choose(blocks []netip.Prefix, length int, strategy Strategy) (netip.Prefix, int) {
	var best netip.Prefix

	bestBits := -1

	for _, block := range blocks {
		if block.Bits() > length {
			continue
		}

		candidate := netip.PrefixFrom(block.Addr(), length)
		better := !best.IsValid() || candidate.Addr().Less(best.Addr())

		if strategy == StrategyBestFit && best.IsValid() && block.Bits() != bestBits {
			better = block.Bits() > bestBits
		}

		if better {
			best = candidate
			bestBits = block.Bits()
		}
	}

	return best, bestBits
}

// CheckExplicit validates that cidr belongs to one parent and is unallocated.
//
// IPv4-mapped IPv6 addresses are invalid; they are not silently converted to
// IPv4. Unaligned prefixes retain normal netip semantics and are compared by
// their masked address range.
func CheckExplicit(parents, used []netip.Prefix, cidr netip.Prefix) error {
	if !validPrefix(cidr) || !validPrefixes(parents) || !validPrefixes(used) {
		return ErrInvalidCIDR
	}

	cidr = cidr.Masked()
	contained := false

	for _, parent := range parents {
		parent = parent.Masked()
		if sameFamily(parent, cidr) &&
			parent.Bits() <= cidr.Bits() &&
			parent.Contains(cidr.Addr()) {
			contained = true
			break
		}
	}

	if !contained {
		return ErrInvalidCIDR
	}

	for _, existing := range used {
		existing = existing.Masked()
		if sameFamily(existing, cidr) &&
			(existing.Contains(cidr.Addr()) || cidr.Contains(existing.Addr())) {
			return ErrOverlap
		}
	}

	return nil
}

func validPrefixes(prefixes []netip.Prefix) bool {
	if len(prefixes) == 0 {
		return true
	}

	for _, prefix := range prefixes {
		if !validPrefix(prefix) {
			return false
		}
	}

	return true
}

func validPrefix(prefix netip.Prefix) bool {
	addr := prefix.Addr()

	return prefix.IsValid() &&
		addr.Zone() == "" &&
		!addr.Is4In6()
}

func sameFamily(left, right netip.Prefix) bool {
	return left.Addr().BitLen() == right.Addr().BitLen()
}
