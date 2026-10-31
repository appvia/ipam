// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"fmt"
	"net/netip"
	"slices"
	"time"
)

const (
	minCooldown = time.Second
	maxCooldown = 8760 * time.Hour
)

// validatePolicy checks a policy given by a caller. StrategyDefault is
// accepted only on create, where it resolves to StrategyBestFit.
func validatePolicy(policy PoolPolicy, forCreate bool) error {
	switch policy.Strategy {
	case StrategyLowest, StrategyBestFit:
	case StrategyDefault:
		if !forCreate {
			return fmt.Errorf("policy strategy is required: %w", ErrInvalidArgument)
		}
	default:
		return fmt.Errorf("policy strategy %q is not lowest or best-fit: %w", policy.Strategy, ErrInvalidArgument)
	}

	return validateCooldown(policy.Cooldown)
}

func validateCooldown(cooldown Cooldown) error {
	switch cooldown.Mode {
	case CooldownNone:
		if cooldown.Duration != 0 {
			return fmt.Errorf("cooldown duration requires a cooldown mode: %w", ErrInvalidArgument)
		}

		return nil
	case CooldownStrict, CooldownFitAware:
	default:
		return fmt.Errorf("cooldown mode %q is not strict or fit-aware: %w", cooldown.Mode, ErrInvalidArgument)
	}

	if cooldown.Duration%time.Second != 0 {
		return fmt.Errorf("cooldown duration must be whole seconds: %w", ErrInvalidArgument)
	}

	if cooldown.Duration < minCooldown || cooldown.Duration > maxCooldown {
		return fmt.Errorf("cooldown duration must be between 1s and 8760h: %w", ErrInvalidArgument)
	}

	return nil
}

func validate(pool poolDocument, reservations reservationsDocument) error {
	canonical, err := canonicalPool(pool.Pool)
	if err != nil {
		return fmt.Errorf("validate pool document: %w", err)
	}

	if !validIncarnation(pool.Incarnation) || !validIncarnation(reservations.Incarnation) {
		return fmt.Errorf("validate incarnation: %w", ErrInvalidArgument)
	}

	if reservations.PoolID != canonical.ID {
		return fmt.Errorf(
			"reservations pool ID %q does not match pool %q: %w",
			reservations.PoolID,
			canonical.ID,
			ErrInvalidArgument,
		)
	}

	if reservations.Incarnation != pool.Incarnation {
		return &OpError{
			Op:     "validate",
			Pool:   canonical.ID,
			Reason: "incarnation_mismatch",
			Err:    ErrConflict,
		}
	}

	allocated := make([]netip.Prefix, 0, len(reservations.Allocations))
	for key, allocation := range reservations.Allocations {
		if err := validateAllocation(canonical, key, allocation); err != nil {
			return err
		}

		allocated = append(allocated, allocation.CIDR)
	}

	slices.SortFunc(allocated, func(left, right netip.Prefix) int {
		if comparison := left.Addr().Compare(right.Addr()); comparison != 0 {
			return comparison
		}

		return left.Bits() - right.Bits()
	})

	for index := 1; index < len(allocated); index++ {
		if allocated[index-1].Contains(allocated[index].Addr()) {
			return fmt.Errorf(
				"allocations %q and %q overlap: %w",
				allocated[index-1],
				allocated[index],
				ErrInvalidCIDR,
			)
		}
	}

	return nil
}

func validateAllocation(pool Pool, key string, allocation Allocation) error {
	if key != allocation.ID {
		return fmt.Errorf("allocation map key %q does not match ID %q: %w", key, allocation.ID, ErrInvalidArgument)
	}

	if err := validateAllocationID(allocation.ID); err != nil {
		return err
	}

	if allocation.PoolID != pool.ID {
		return fmt.Errorf(
			"allocation %q pool ID %q does not match pool %q: %w",
			allocation.ID,
			allocation.PoolID,
			pool.ID,
			ErrInvalidArgument,
		)
	}

	cidr, err := canonicalNetPrefix(allocation.CIDR)
	if err != nil || cidr != allocation.CIDR {
		return fmt.Errorf("allocation %q CIDR %q is not canonical: %w", allocation.ID, allocation.CIDR, ErrInvalidCIDR)
	}

	if prefixFamily(cidr) != pool.Family || !insidePool(pool.CIDRs, cidr) {
		return fmt.Errorf("allocation %q CIDR %q is outside pool %q: %w", allocation.ID, cidr, pool.ID, ErrInvalidCIDR)
	}

	if err := validateRequest(allocation.Request, cidr); err != nil {
		return fmt.Errorf("allocation %q request: %w", allocation.ID, err)
	}

	return nil
}

func validateRequest(request Request, allocated netip.Prefix) error {
	switch request.Kind {
	case RequestNext:
		if request.PrefixLength != allocated.Bits() || request.CIDR.IsValid() {
			return fmt.Errorf("next request does not match allocated CIDR %q: %w", allocated, ErrInvalidArgument)
		}
	case RequestExplicit:
		explicit, err := canonicalNetPrefix(request.CIDR)
		if err != nil || explicit != request.CIDR || explicit != allocated || request.PrefixLength != 0 {
			return fmt.Errorf("explicit request does not match allocated CIDR %q: %w", allocated, ErrInvalidCIDR)
		}
	default:
		return fmt.Errorf("request kind %q: %w", request.Kind, ErrInvalidArgument)
	}

	return nil
}

func insidePool(parents []netip.Prefix, child netip.Prefix) bool {
	for _, parent := range parents {
		if prefixFamily(parent) == prefixFamily(child) &&
			parent.Bits() <= child.Bits() &&
			parent.Contains(child.Addr()) {
			return true
		}
	}

	return false
}

func validIncarnation(incarnation string) bool {
	if len(incarnation) != 32 {
		return false
	}

	for _, character := range incarnation {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}

	return true
}
