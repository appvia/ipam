// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package alloc provides deterministic, in-memory CIDR allocation.
package alloc

import (
	"errors"
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
)

// Next returns the lowest available prefix of length from any eligible parent.
//
// IPv4-mapped IPv6 addresses are rejected rather than implicitly unmapped, so
// callers must choose an unambiguous address family before allocation.
func Next(parents, used []netip.Prefix, length int) (netip.Prefix, error) {
	if !validPrefixes(parents) || !validPrefixes(used) {
		return netip.Prefix{}, ErrInvalidCIDR
	}

	var (
		best     netip.Prefix
		eligible bool
	)

	for _, parent := range parents {
		if length < parent.Bits() || length > parent.Addr().BitLen() {
			continue
		}

		eligible = true

		var builder netipx.IPSetBuilder
		builder.AddPrefix(parent.Masked())

		for _, existing := range used {
			builder.RemovePrefix(existing.Masked())
		}

		free, err := builder.IPSet()
		if err != nil {
			return netip.Prefix{}, ErrInvalidCIDR
		}

		for _, block := range free.Prefixes() {
			if block.Bits() > length {
				continue
			}

			candidate := netip.PrefixFrom(block.Addr(), length).Masked()
			if !best.IsValid() || candidate.Addr().Compare(best.Addr()) < 0 {
				best = candidate
			}
		}
	}

	if !eligible {
		return netip.Prefix{}, ErrInvalidCIDR
	}

	if !best.IsValid() {
		return netip.Prefix{}, ErrExhausted
	}

	return best, nil
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
