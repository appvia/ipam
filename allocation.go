// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/appvia/ipam/internal/alloc"
	"github.com/appvia/ipam/storage"
)

// AllocationSpec describes a next-available or explicit allocation.
type AllocationSpec struct {
	ID           string
	PrefixLength int
	CIDR         netip.Prefix
	Description  string
	Labels       map[string]string
}

// Allocate reserves one CIDR. An empty ID is generated once before retrying.
func (client *Client) Allocate(
	ctx context.Context,
	poolID string,
	spec AllocationSpec,
) (Allocation, error) {
	return client.allocate(ctx, poolID, spec, false)
}

func (client *Client) allocate(
	ctx context.Context,
	poolID string,
	spec AllocationSpec,
	allowChildID bool,
) (Allocation, error) {
	allocation, _, err := client.allocateWithStatus(ctx, poolID, spec, allowChildID)

	return allocation, err
}

func (client *Client) allocateWithStatus(
	ctx context.Context,
	poolID string,
	spec AllocationSpec,
	allowChildID bool,
) (Allocation, bool, error) {
	if err := validateClient(client); err != nil {
		return Allocation{}, false, err
	}

	if err := validatePoolID(poolID); err != nil {
		return Allocation{}, false, &OpError{Op: "allocate", Pool: poolID, Err: err}
	}

	if spec.ID == "" {
		spec.ID = client.idGenerator()
	}

	if err := validateAllocationID(spec.ID); err != nil {
		return Allocation{}, false, &OpError{Op: "allocate", Pool: poolID, ID: spec.ID, Err: err}
	}

	if strings.HasPrefix(spec.ID, "pool:") && !allowChildID {
		return Allocation{}, false, &OpError{
			Op: "allocate", Pool: poolID, ID: spec.ID,
			Err: fmt.Errorf("reserved allocation ID: %w", ErrInvalidArgument),
		}
	}

	request, err := canonicalAllocationRequest(spec)
	if err != nil {
		return Allocation{}, false, &OpError{Op: "allocate", Pool: poolID, ID: spec.ID, Err: err}
	}

	lock := client.poolLock(poolID)
	if err := lock.LockContext(ctx); err != nil {
		return Allocation{}, false, err
	}
	defer lock.Unlock()

	var last error

	for attempt := 1; ; attempt++ {
		allocation, created, retry, err := client.allocateAttempt(ctx, poolID, spec, request)
		if err == nil || !retry {
			return allocation, created, err
		}

		last = err
		if client.attemptsExhausted(attempt) {
			return Allocation{}, false, contentionError("allocate", poolID, spec.ID, last)
		}

		if err := client.waitRetry(ctx, attempt-1); err != nil {
			return Allocation{}, false, err
		}
	}
}

func (client *Client) allocateAttempt(
	ctx context.Context,
	poolID string,
	spec AllocationSpec,
	request Request,
) (Allocation, bool, bool, error) {
	pool, reservations, version, err := client.readPoolState(ctx, "allocate", poolID)
	if err != nil {
		if conflictReason(err, "incarnation_mismatch") {
			return Allocation{}, false, true, err
		}

		return Allocation{}, false, false, err
	}

	if existing, ok := reservations.Allocations[spec.ID]; ok {
		if requestsEqual(existing.Request, request) {
			return cloneAllocation(existing), false, false, nil
		}

		return Allocation{}, false, false, &OpError{
			Op: "allocate", Pool: poolID, ID: spec.ID, Reason: "id_reused",
			Err: fmt.Errorf(
				"existing request %s differs from requested %s: %w",
				describeRequest(existing.Request),
				describeRequest(request),
				ErrConflict,
			),
		}
	}

	now := client.clock().UTC()
	pruneReleased(&reservations, now)

	used := make([]netip.Prefix, 0, len(reservations.Allocations))
	for _, allocation := range reservations.Allocations {
		used = append(used, allocation.CIDR)
	}

	var cidr netip.Prefix

	switch request.Kind {
	case RequestNext:
		input := alloc.Input{
			Parents: pool.CIDRs, Used: used, Length: request.PrefixLength,
		}
		if pool.Policy.Strategy == StrategyBestFit {
			input.Strategy = alloc.StrategyBestFit
		}

		switch reservations.Cooldown.Mode {
		case CooldownNone:
		case CooldownStrict:
			input.CooldownMode = alloc.CooldownStrict
		case CooldownFitAware:
			input.CooldownMode = alloc.CooldownFitAware
		}

		if reservations.Cooldown.Mode != CooldownNone {
			input.Cooling = coolingPrefixes(reservations.Released, now)
		}

		cidr, err = alloc.Select(input)
		if errors.Is(err, alloc.ErrCooling) {
			until := cooldownUntil(input, reservations.Released)

			return Allocation{}, false, false, &OpError{
				Op: "allocate", Pool: poolID, ID: spec.ID, Reason: "cooldown",
				Err: fmt.Errorf("only cooling space fits until %s: %w", until, ErrPoolExhausted),
			}
		}
	case RequestExplicit:
		cidr = request.CIDR.Masked()
		err = alloc.CheckExplicit(pool.CIDRs, used, cidr)
	default:
		err = alloc.ErrInvalidCIDR
	}

	if err != nil {
		if errors.Is(err, alloc.ErrInvalidCIDR) {
			if reason := invalidRequestReason(pool.Pool, request); reason != "" {
				return Allocation{}, false, false, &OpError{
					Op: "allocate", Pool: poolID, ID: spec.ID,
					Err: fmt.Errorf("%w: %s", ErrInvalidCIDR, reason),
				}
			}
		}

		return Allocation{}, false, false, mapAllocatorError("allocate", poolID, spec.ID, err)
	}

	allocation := Allocation{
		ID:          spec.ID,
		PoolID:      poolID,
		Request:     request,
		CIDR:        cidr,
		Description: spec.Description,
		Labels:      cloneLabels(spec.Labels),
		CreatedAt:   now,
	}
	reservations.Allocations[spec.ID] = allocation

	data, err := encodeReservationsDocument(reservations)
	if err != nil {
		return Allocation{}, false, false, err
	}

	// The successful CAS is the linearization point. The in-process mutex only
	// reduces contention; separate clients remain correct because every stale
	// version loses this write and recomputes from the winning document.
	_, err = client.store.PutIfMatch(ctx, reservationsKey(poolID), data, version)
	if err == nil {
		return cloneAllocation(allocation), true, false, nil
	}

	if errors.Is(err, storage.ErrUnknownOutcome) {
		observed, observeErr := client.observeAllocation(ctx, poolID, spec.ID, request)
		switch {
		case observeErr == nil && observed != nil:
			return cloneAllocation(*observed), false, false, nil
		case observeErr != nil && !errors.Is(observeErr, ErrNotFound):
			return Allocation{}, false, false, observeErr
		default:
			return Allocation{}, false, true, err
		}
	}

	if errors.Is(err, storage.ErrPreconditionFailed) {
		return Allocation{}, false, true, err
	}

	return Allocation{}, false, false, mapStorageError("allocate", poolID, spec.ID, err)
}

// pruneReleased drops records at or before now, independently of current policy.
func pruneReleased(reservations *reservationsDocument, now time.Time) {
	for cidr, expiry := range reservations.Released {
		if !expiry.After(now) {
			delete(reservations.Released, cidr)
		}
	}
}

func coolingPrefixes(released map[netip.Prefix]time.Time, after time.Time) []netip.Prefix {
	cooling := make([]netip.Prefix, 0, len(released))
	for cidr, expiry := range released {
		if expiry.After(after) {
			cooling = append(cooling, cidr)
		}
	}

	return cooling
}

// cooldownUntil is called only after strict selection found that cooling space
// fits. Removing every record therefore guarantees a successful selection.
func cooldownUntil(input alloc.Input, released map[netip.Prefix]time.Time) string {
	expiries := make([]time.Time, 0, len(released))
	for _, expiry := range released {
		expiries = append(expiries, expiry)
	}

	slices.SortFunc(expiries, time.Time.Compare)
	expiries = slices.CompactFunc(expiries, time.Time.Equal)
	input.CooldownMode = alloc.CooldownStrict

	var until time.Time

	for _, expiry := range expiries {
		until = expiry
		input.Cooling = coolingPrefixes(released, expiry)

		if _, err := alloc.Select(input); err == nil {
			break
		}
	}

	if until.Nanosecond() != 0 {
		until = until.Truncate(time.Second).Add(time.Second)
	}

	return until.UTC().Format(time.RFC3339)
}

// GetAllocation returns one allocation.
func (client *Client) GetAllocation(
	ctx context.Context,
	poolID, id string,
) (Allocation, error) {
	if err := validateAllocationLookup(poolID, id); err != nil {
		return Allocation{}, &OpError{Op: "get allocation", Pool: poolID, ID: id, Err: err}
	}

	_, reservations, _, err := client.readPoolState(ctx, "get allocation", poolID)
	if err != nil {
		return Allocation{}, err
	}

	allocation, found := reservations.Allocations[id]
	if !found {
		return Allocation{}, &OpError{Op: "get allocation", Pool: poolID, ID: id, Err: ErrNotFound}
	}

	return cloneAllocation(allocation), nil
}

// ListAllocations returns a pool's allocations sorted by ID.
func (client *Client) ListAllocations(ctx context.Context, poolID string) ([]Allocation, error) {
	if err := validatePoolID(poolID); err != nil {
		return nil, &OpError{Op: "list allocations", Pool: poolID, Err: err}
	}

	_, reservations, _, err := client.readPoolState(ctx, "list allocations", poolID)
	if err != nil {
		return nil, err
	}

	allocations := make([]Allocation, 0, len(reservations.Allocations))
	for _, allocation := range reservations.Allocations {
		allocations = append(allocations, cloneAllocation(allocation))
	}

	slices.SortFunc(allocations, func(left, right Allocation) int {
		return strings.Compare(left.ID, right.ID)
	})

	return allocations, nil
}

// Release removes an allocation. A missing allocation in an existing pool is a no-op.
//
// A child-pool carve-out pool:<child>:<incarnation> can be released only while
// no pool or reservations document of that child incarnation exists
// (otherwise ErrConflict, Reason pool_in_use). Operators use this to recover a
// carve-out leaked by an interrupted CreatePool or DeletePool. It is only safe
// when no CreatePool for that child can still be running: a creator that has
// committed the carve-out but not yet written the child's reservations
// document is invisible to the pool_in_use check, and would publish the child
// on released space.
func (client *Client) Release(ctx context.Context, poolID, id string) error {
	return client.release(ctx, poolID, id, true)
}

// release retains the carve-out guard even for unpublished CreatePool rollback,
// which is the only caller that disables release recording.
func (client *Client) release(ctx context.Context, poolID, id string, recordRelease bool) error {
	if err := validateClient(client); err != nil {
		return err
	}

	if err := validateAllocationLookup(poolID, id); err != nil {
		return &OpError{Op: "release", Pool: poolID, ID: id, Err: err}
	}

	if childID, incarnation, reserved := parseReservedAllocationID(id); reserved {
		claimed, err := client.childIncarnationExists(ctx, childID, incarnation)
		if err != nil {
			return err
		}

		if claimed {
			return &OpError{
				Op: "release", Pool: poolID, ID: id,
				Reason: "pool_in_use", Err: ErrConflict,
			}
		}
	}

	lock := client.poolLock(poolID)
	if err := lock.LockContext(ctx); err != nil {
		return err
	}
	defer lock.Unlock()

	var last error

	for attempt := 1; ; attempt++ {
		_, reservations, version, err := client.readPoolState(ctx, "release", poolID)
		if err != nil {
			if conflictReason(err, "incarnation_mismatch") {
				last = err
			} else {
				return err
			}
		} else {
			allocation, found := reservations.Allocations[id]
			if !found {
				return nil
			}

			now := client.clock().UTC()
			pruneReleased(&reservations, now)
			delete(reservations.Allocations, id)

			if recordRelease && reservations.Cooldown.Mode != CooldownNone {
				if reservations.Released == nil {
					reservations.Released = make(map[netip.Prefix]time.Time)
				}

				expiry := now.Add(reservations.Cooldown.Duration)
				if expiry.After(reservations.Released[allocation.CIDR]) {
					reservations.Released[allocation.CIDR] = expiry
				}
			}

			data, encodeErr := encodeReservationsDocument(reservations)
			if encodeErr != nil {
				return encodeErr
			}

			_, err = client.store.PutIfMatch(ctx, reservationsKey(poolID), data, version)
			if err == nil {
				return nil
			}

			if errors.Is(err, storage.ErrUnknownOutcome) {
				_, observed, _, readErr := client.readPoolState(ctx, "release", poolID)
				if readErr == nil {
					if _, present := observed.Allocations[id]; !present {
						return nil
					}
				} else if !conflictReason(readErr, "incarnation_mismatch") {
					return readErr
				}
			}

			if !errors.Is(err, storage.ErrPreconditionFailed) &&
				!errors.Is(err, storage.ErrUnknownOutcome) {
				return mapStorageError("release", poolID, id, err)
			}

			last = err
		}

		if client.attemptsExhausted(attempt) {
			return contentionError("release", poolID, id, last)
		}

		if err := client.waitRetry(ctx, attempt-1); err != nil {
			return err
		}
	}
}

func (client *Client) childIncarnationExists(
	ctx context.Context,
	childID, incarnation string,
) (bool, error) {
	documents, err := client.readPoolDocuments(ctx, childID)
	if err != nil {
		return false, err
	}

	return (documents.pool != nil && documents.pool.Incarnation == incarnation) ||
		(documents.reservations != nil && documents.reservations.Incarnation == incarnation), nil
}

// UpdateAllocationMetadata changes only description and labels.
func (client *Client) UpdateAllocationMetadata(
	ctx context.Context,
	poolID, id, description string,
	labels map[string]string,
) error {
	if err := validateClient(client); err != nil {
		return err
	}

	if err := validateAllocationLookup(poolID, id); err != nil {
		return &OpError{Op: "update allocation", Pool: poolID, ID: id, Err: err}
	}

	if strings.HasPrefix(id, "pool:") {
		return &OpError{
			Op: "update allocation", Pool: poolID, ID: id,
			Err: fmt.Errorf("reserved allocation ID: %w", ErrInvalidArgument),
		}
	}

	lock := client.poolLock(poolID)
	if err := lock.LockContext(ctx); err != nil {
		return err
	}
	defer lock.Unlock()

	var last error

	for attempt := 1; ; attempt++ {
		_, reservations, version, err := client.readPoolState(ctx, "update allocation", poolID)
		if err != nil {
			if conflictReason(err, "incarnation_mismatch") {
				last = err
			} else {
				return err
			}
		} else {
			allocation, found := reservations.Allocations[id]
			if !found {
				return &OpError{Op: "update allocation", Pool: poolID, ID: id, Err: ErrNotFound}
			}

			allocation.Description = description
			allocation.Labels = cloneLabels(labels)
			reservations.Allocations[id] = allocation

			pruneReleased(&reservations, client.clock().UTC())

			data, encodeErr := encodeReservationsDocument(reservations)
			if encodeErr != nil {
				return encodeErr
			}

			_, err = client.store.PutIfMatch(ctx, reservationsKey(poolID), data, version)
			if err == nil {
				return nil
			}

			if errors.Is(err, storage.ErrUnknownOutcome) {
				observed, observeErr := client.GetAllocation(ctx, poolID, id)
				if observeErr == nil && observed.Description == description && maps.Equal(observed.Labels, labels) {
					return nil
				}
			}

			if !errors.Is(err, storage.ErrPreconditionFailed) &&
				!errors.Is(err, storage.ErrUnknownOutcome) {
				return mapStorageError("update allocation", poolID, id, err)
			}

			last = err
		}

		if client.attemptsExhausted(attempt) {
			return contentionError("update allocation", poolID, id, last)
		}

		if err := client.waitRetry(ctx, attempt-1); err != nil {
			return err
		}
	}
}

func (client *Client) readPoolState(
	ctx context.Context,
	op, poolID string,
) (poolDocument, reservationsDocument, storage.Version, error) {
	poolData, _, err := client.store.Get(ctx, poolKey(poolID))
	if err != nil {
		return poolDocument{}, reservationsDocument{}, "", mapStorageError(op, poolID, "", err)
	}

	pool, err := decodePoolDocument(poolData, poolID)
	if err != nil {
		return poolDocument{}, reservationsDocument{}, "", err
	}

	reservationsData, version, err := client.store.Get(ctx, reservationsKey(poolID))
	if err != nil {
		return poolDocument{}, reservationsDocument{}, "", mapStorageError(op, poolID, "", err)
	}

	reservations, err := decodeReservationsDocument(reservationsData, poolID)
	if err != nil {
		return poolDocument{}, reservationsDocument{}, "", err
	}

	if err := validate(pool, reservations); err != nil {
		return poolDocument{}, reservationsDocument{}, "", err
	}

	return pool, reservations, version, nil
}

func (client *Client) observeAllocation(
	ctx context.Context,
	poolID, id string,
	request Request,
) (*Allocation, error) {
	_, reservations, _, err := client.readPoolState(ctx, "allocate", poolID)
	if err != nil {
		return nil, err
	}

	existing, found := reservations.Allocations[id]
	if !found {
		return nil, &OpError{Op: "allocate", Pool: poolID, ID: id, Err: ErrNotFound}
	}

	if !requestsEqual(existing.Request, request) {
		return nil, &OpError{
			Op: "allocate", Pool: poolID, ID: id, Reason: "id_reused", Err: ErrConflict,
		}
	}

	return &existing, nil
}

func canonicalAllocationRequest(spec AllocationSpec) (Request, error) {
	if spec.CIDR.IsValid() {
		if spec.PrefixLength != 0 {
			return Request{}, fmt.Errorf("CIDR and prefix length are mutually exclusive: %w", ErrInvalidArgument)
		}

		cidr, err := canonicalNetPrefix(spec.CIDR)
		if err != nil {
			return Request{}, err
		}

		return Request{Kind: RequestExplicit, CIDR: cidr}, nil
	}

	if spec.PrefixLength < 0 || spec.PrefixLength > 128 {
		return Request{}, fmt.Errorf("prefix length %d: %w", spec.PrefixLength, ErrInvalidCIDR)
	}

	return Request{Kind: RequestNext, PrefixLength: spec.PrefixLength}, nil
}

// invalidRequestReason says why the allocator refused request as an invalid
// CIDR: an explicit CIDR outside every pool CIDR, or a prefix length shorter
// than the pool's largest block (its shortest prefix) or longer than the
// address. It returns "" when none of these applies. The text holds only the
// pool ID and CIDRs, never labels or the description. A pool has one family,
// so the first CIDR's address length applies to all of them.
func invalidRequestReason(pool Pool, request Request) string {
	cidrs := make([]string, 0, len(pool.CIDRs))
	for _, cidr := range pool.CIDRs {
		cidrs = append(cidrs, cidr.Masked().String())
	}

	switch request.Kind {
	case RequestExplicit:
		cidr := request.CIDR.Masked()
		for _, parent := range pool.CIDRs {
			if parent.Bits() <= cidr.Bits() && parent.Masked().Contains(cidr.Addr()) {
				return ""
			}
		}

		return fmt.Sprintf("cidr %s is outside pool %s (%s)", cidr, pool.ID, strings.Join(cidrs, ", "))
	case RequestNext:
		if len(pool.CIDRs) == 0 {
			return ""
		}

		bitLen := pool.CIDRs[0].Addr().BitLen()
		shortest := bitLen

		for _, parent := range pool.CIDRs {
			if request.PrefixLength >= parent.Bits() && request.PrefixLength <= bitLen {
				return ""
			}

			shortest = min(shortest, parent.Bits())
		}

		if request.PrefixLength > bitLen {
			return fmt.Sprintf("prefix length %d is longer than the /%d addresses of pool %s",
				request.PrefixLength, bitLen, pool.ID)
		}

		if len(pool.CIDRs) > 1 {
			return fmt.Sprintf("prefix length %d is shorter than the largest block of pool %s (/%d)",
				request.PrefixLength, pool.ID, shortest)
		}

		return fmt.Sprintf("prefix length %d is shorter than pool %s (/%d)", request.PrefixLength, pool.ID, shortest)
	}

	return ""
}

func mapAllocatorError(op, poolID, id string, err error) error {
	switch {
	case errors.Is(err, alloc.ErrInvalidArgument):
		return &OpError{Op: op, Pool: poolID, ID: id, Err: ErrInvalidArgument}
	case errors.Is(err, alloc.ErrInvalidCIDR):
		return &OpError{Op: op, Pool: poolID, ID: id, Err: ErrInvalidCIDR}
	case errors.Is(err, alloc.ErrExhausted):
		return &OpError{Op: op, Pool: poolID, ID: id, Err: ErrPoolExhausted}
	case errors.Is(err, alloc.ErrOverlap):
		return &OpError{Op: op, Pool: poolID, ID: id, Reason: "overlap", Err: ErrConflict}
	default:
		return err
	}
}

func validateAllocationLookup(poolID, id string) error {
	if err := validatePoolID(poolID); err != nil {
		return err
	}

	return validateAllocationID(id)
}

func requestsEqual(left, right Request) bool {
	return left.Kind == right.Kind &&
		left.PrefixLength == right.PrefixLength &&
		left.CIDR == right.CIDR
}

func describeRequest(request Request) string {
	if request.Kind == RequestExplicit {
		return "explicit " + request.CIDR.String()
	}

	return fmt.Sprintf("next /%d", request.PrefixLength)
}

func cloneAllocation(allocation Allocation) Allocation {
	allocation.Labels = cloneLabels(allocation.Labels)
	return allocation
}
