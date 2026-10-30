// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
)

var poolIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NewAllocationID returns a canonical lowercase UUIDv7 allocation ID.
func NewAllocationID() string {
	return uuid.Must(uuid.NewV7()).String()
}

func canonicalPrefix(raw string) (netip.Prefix, error) {
	if strings.Contains(raw, "%") {
		return netip.Prefix{}, fmt.Errorf("parse prefix %q: zones are not allowed: %w", raw, ErrInvalidCIDR)
	}

	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("parse prefix %q: %w", raw, ErrInvalidCIDR)
	}

	return canonicalNetPrefix(prefix)
}

func canonicalNetPrefix(prefix netip.Prefix) (netip.Prefix, error) {
	if !prefix.IsValid() || prefix.Addr().Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("canonicalize prefix %q: %w", prefix, ErrInvalidCIDR)
	}

	if prefix.Addr().Is4In6() {
		if prefix.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("mapped prefix %q is shorter than /96: %w", prefix, ErrInvalidCIDR)
		}

		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}

	return prefix.Masked(), nil
}

func canonicalPool(pool Pool) (Pool, error) {
	if err := validatePoolID(pool.ID); err != nil {
		return Pool{}, err
	}

	if len(pool.CIDRs) == 0 {
		return Pool{}, fmt.Errorf("pool %q has no CIDRs: %w", pool.ID, ErrInvalidCIDR)
	}

	canonical := pool

	canonical.CIDRs = make([]netip.Prefix, len(pool.CIDRs))
	for index, prefix := range pool.CIDRs {
		parsed, err := canonicalNetPrefix(prefix)
		if err != nil {
			return Pool{}, fmt.Errorf("pool %q CIDR %d: %w", pool.ID, index, err)
		}

		canonical.CIDRs[index] = parsed
	}

	family := prefixFamily(canonical.CIDRs[0])
	if pool.Family != 0 && pool.Family != family {
		return Pool{}, fmt.Errorf("pool %q family %d does not match CIDRs: %w", pool.ID, pool.Family, ErrInvalidCIDR)
	}

	canonical.Family = family

	slices.SortFunc(canonical.CIDRs, func(left, right netip.Prefix) int {
		if comparison := left.Addr().Compare(right.Addr()); comparison != 0 {
			return comparison
		}

		return left.Bits() - right.Bits()
	})

	for index, prefix := range canonical.CIDRs {
		if prefixFamily(prefix) != family {
			return Pool{}, fmt.Errorf("pool %q contains mixed address families: %w", pool.ID, ErrInvalidCIDR)
		}

		if index > 0 && canonical.CIDRs[index-1].Contains(prefix.Addr()) {
			return Pool{}, fmt.Errorf(
				"pool %q CIDRs %q and %q overlap: %w",
				pool.ID,
				canonical.CIDRs[index-1],
				prefix,
				ErrInvalidCIDR,
			)
		}
	}

	if pool.Parent != nil {
		if err := validatePoolID(pool.Parent.PoolID); err != nil {
			return Pool{}, fmt.Errorf("pool %q parent: %w", pool.ID, err)
		}

		childID, _, ok := parseReservedAllocationID(pool.Parent.AllocationID)
		if !ok || childID != pool.ID {
			return Pool{}, fmt.Errorf("pool %q parent allocation %q: %w", pool.ID, pool.Parent.AllocationID, ErrInvalidArgument)
		}

		parent := *pool.Parent
		canonical.Parent = &parent
	}

	canonical.Labels = cloneLabels(pool.Labels)

	return canonical, nil
}

func validatePoolID(id string) error {
	if !poolIDPattern.MatchString(id) {
		return fmt.Errorf("pool ID %q: %w", id, ErrInvalidArgument)
	}

	return nil
}

func validateAllocationID(id string) error {
	if _, _, found := parseReservedAllocationID(id); found {
		return nil
	}

	if strings.HasPrefix(id, "pool:") {
		return fmt.Errorf("allocation ID %q: %w", id, ErrInvalidArgument)
	}

	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return fmt.Errorf("allocation ID %q: %w", id, ErrInvalidArgument)
	}

	return nil
}

func parseReservedAllocationID(id string) (childID, incarnation string, ok bool) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] != "pool" ||
		validatePoolID(parts[1]) != nil || !validIncarnation(parts[2]) {
		return "", "", false
	}

	return parts[1], parts[2], true
}

func newIncarnation() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate incarnation: %w", err)
	}

	return hex.EncodeToString(value[:]), nil
}

func prefixFamily(prefix netip.Prefix) int {
	if prefix.Addr().Is4() {
		return 4
	}

	return 6
}

func cloneLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}

	return cloned
}
