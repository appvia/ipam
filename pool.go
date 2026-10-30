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

	"github.com/appvia/ipam/storage"
)

const (
	poolsPrefix        = "pools/"
	reservationsPrefix = "reservations/"
	documentSuffix     = ".json"
)

// PoolSpec describes a root pool or a child carved from ParentPool.
type PoolSpec struct {
	ID          string
	CIDRs       []netip.Prefix
	ParentPool  string
	Request     Request
	Description string
	Labels      map[string]string
	// Policy is the pool's own allocation policy; a child never inherits its
	// parent's. StrategyDefault creates a best-fit pool. On a re-run against
	// an existing pool, an unset strategy or cooldown matches any stored value.
	Policy PoolPolicy
}

func poolKey(poolID string) string         { return poolsPrefix + poolID + documentSuffix }
func reservationsKey(poolID string) string { return reservationsPrefix + poolID + documentSuffix }

// CreatePool creates a root pool or carves a child pool from its parent.
//
// Every call generates a fresh incarnation and writes it only with
// CreateIfAbsent: first the parent carve-out pool:<child>:<incarnation> (child
// pools only), then the empty reservations document, and last the pool
// document, which publishes the pool. A pool is usable only when both
// documents exist with the same incarnation. CreatePool never adopts or
// rewrites documents it did not create in this call, so a stale or losing
// creator can never revive a deleted pool or touch a winner's documents.
//
// An identical complete pool is returned without writes. A different pool
// with the same ID is ErrConflict (Reason pool_exists) without writes. Residue
// of an interrupted create or delete is retried with backoff and then reported
// as ErrConflict (Reason pool_incomplete), again without writes; DeletePool
// clears it. A crash after the carve-out is committed but before the pool is
// published leaves that carve-out reserved: it is safe because the space stays
// reserved, and an operator may release it (see Release).
func (client *Client) CreatePool(ctx context.Context, spec PoolSpec) (Pool, error) {
	if err := validateClient(client); err != nil {
		return Pool{}, err
	}

	if err := validatePoolSpec(spec); err != nil {
		return Pool{}, &OpError{Op: "create pool", Pool: spec.ID, Err: err}
	}

	for attempt := 1; ; attempt++ {
		pool, outcome, err := client.createPoolAttempt(ctx, spec)
		if outcome == createFinished {
			return clonePool(pool), err
		}

		if client.attemptsExhausted(attempt) {
			return Pool{}, createRetryError(spec.ID, outcome, err)
		}

		if waitErr := client.waitRetry(ctx, attempt-1); waitErr != nil {
			return Pool{}, waitErr
		}
	}
}

type createOutcome int

const (
	createFinished createOutcome = iota
	createIncomplete
	createRaced
	createContended
)

func createRetryError(poolID string, outcome createOutcome, last error) error {
	switch outcome {
	case createIncomplete:
		return &OpError{
			Op: "create pool", Pool: poolID, Reason: "pool_incomplete",
			Err: fmt.Errorf(
				"%w: %v; run DeletePool to clear the residue of an interrupted create or delete, then retry",
				ErrConflict, last,
			),
		}
	case createRaced:
		return &OpError{
			Op: "create pool", Pool: poolID, Reason: "pool_create_raced",
			Err: errors.Join(ErrConflict, last),
		}
	default:
		return contentionError("create pool", poolID, "", last)
	}
}

var (
	errReservationsWithoutPool = errors.New("reservations document exists without its pool document")
	errPoolWithoutReservations = errors.New("pool document exists without reservations of its incarnation")
	errCreateRaced             = errors.New("reservations document changed while the pool was being published")
)

func (client *Client) createPoolAttempt(
	ctx context.Context,
	spec PoolSpec,
) (Pool, createOutcome, error) {
	lock := client.poolLock(spec.ID)
	if err := lock.LockContext(ctx); err != nil {
		return Pool{}, createFinished, err
	}

	documents, err := client.readPoolDocuments(ctx, spec.ID)
	lock.Unlock()

	if err != nil {
		return Pool{}, createFinished, err
	}

	switch {
	case documents.pool != nil && !poolMatchesSpec(*documents.pool, documents.reservations, spec):
		return Pool{}, createFinished, poolExistsError(spec.ID)
	case documents.usable():
		return documents.publicPool(), createFinished, nil
	case documents.pool != nil:
		return Pool{}, createIncomplete, errPoolWithoutReservations
	case documents.reservations != nil:
		return Pool{}, createIncomplete, errReservationsWithoutPool
	}

	incarnation, err := client.newIncarnation()
	if err != nil {
		return Pool{}, createFinished, &OpError{Op: "create pool", Pool: spec.ID, Err: err}
	}

	pool := Pool{
		ID:          spec.ID,
		CIDRs:       append([]netip.Prefix(nil), spec.CIDRs...),
		Description: spec.Description,
		Labels:      cloneLabels(spec.Labels),
		Policy:      spec.Policy,
	}
	if pool.Policy.Strategy == StrategyDefault {
		pool.Policy.Strategy = StrategyBestFit
	}

	if spec.ParentPool != "" {
		carveID := reservedAllocationID(spec.ID, incarnation)

		allocation, _, allocateErr := client.allocateWithStatus(
			ctx,
			spec.ParentPool,
			AllocationSpec{
				ID:           carveID,
				PrefixLength: spec.Request.PrefixLength,
				CIDR:         spec.Request.CIDR,
				Description:  spec.Description,
				Labels:       spec.Labels,
			},
			true,
		)
		if allocateErr != nil {
			return Pool{}, createFinished, &OpError{Op: "create pool", Pool: spec.ID, Err: allocateErr}
		}

		pool.CIDRs = []netip.Prefix{allocation.CIDR}
		pool.Parent = &ParentRef{PoolID: spec.ParentPool, AllocationID: carveID}
	}

	canonical, err := canonicalPool(pool)
	if err == nil {
		if err = lock.LockContext(ctx); err == nil {
			var outcome createOutcome

			outcome, err = client.publishPool(ctx, poolDocument{Pool: canonical, Incarnation: incarnation})
			lock.Unlock()

			if outcome == createFinished && err == nil {
				return canonical, createFinished, nil
			}

			if outcome != createFinished {
				return Pool{}, outcome, client.releaseOwnCarveOut(ctx, pool.Parent, err)
			}
		}
	}

	return Pool{}, createFinished, client.releaseOwnCarveOut(ctx, pool.Parent, err)
}

// releaseOwnCarveOut withdraws the carve-out this CreatePool call created. The
// ID is keyed by this call's incarnation, so it can never release another
// incarnation's space; Release still refuses while any document of that
// incarnation exists, which keeps a published child's carve-out in place.
func (client *Client) releaseOwnCarveOut(ctx context.Context, parent *ParentRef, cause error) error {
	if parent == nil {
		return cause
	}

	releaseErr := client.Release(ctx, parent.PoolID, parent.AllocationID)
	if releaseErr == nil || errors.Is(releaseErr, ErrNotFound) || conflictReason(releaseErr, "pool_in_use") {
		return cause
	}

	return errors.Join(cause, releaseErr)
}

// publishPool writes this call's reservations and then its pool document, both
// with CreateIfAbsent, and verifies afterwards that the reservations document
// still carries this incarnation. Any document it created and cannot keep is
// removed again, conditionally on its own version.
func (client *Client) publishPool(ctx context.Context, proposed poolDocument) (createOutcome, error) {
	reservationsData, err := encodeReservationsDocument(reservationsDocument{
		PoolID: proposed.ID, Incarnation: proposed.Incarnation, Allocations: map[string]Allocation{},
		Cooldown: proposed.Policy.Cooldown,
	})
	if err != nil {
		return createFinished, err
	}

	reservationsVersion, owned, err := client.createOwnDocument(
		ctx, reservationsKey(proposed.ID), proposed.ID, reservationsData, proposed.Incarnation,
	)
	if err != nil {
		return createFinished, err
	}

	if !owned {
		return createIncomplete, errReservationsWithoutPool
	}

	poolData, err := encodePoolDocument(proposed)
	if err == nil {
		var poolVersion storage.Version

		poolVersion, owned, err = client.createOwnDocument(
			ctx, poolKey(proposed.ID), proposed.ID, poolData, proposed.Incarnation,
		)
		if err == nil && owned {
			return client.verifyPublishedPool(ctx, proposed, poolVersion)
		}
	}

	cleanupErr := client.deleteOwnDocument(
		ctx, reservationsKey(proposed.ID), proposed.ID, reservationsVersion, proposed.Incarnation,
	)
	if err != nil || cleanupErr != nil {
		return createFinished, errors.Join(err, cleanupErr)
	}

	return createRaced, poolExistsError(proposed.ID)
}

func (client *Client) verifyPublishedPool(
	ctx context.Context,
	proposed poolDocument,
	poolVersion storage.Version,
) (createOutcome, error) {
	data, _, err := client.store.Get(ctx, reservationsKey(proposed.ID))
	if err == nil {
		incarnation, decodeErr := documentIncarnation(reservationsKey(proposed.ID), proposed.ID, data)
		if decodeErr != nil {
			return createFinished, decodeErr
		}

		if incarnation == proposed.Incarnation {
			return createFinished, nil
		}
	} else if !errors.Is(err, storage.ErrNotFound) {
		return createFinished, err
	}

	if cleanupErr := client.deleteOwnDocument(
		ctx, poolKey(proposed.ID), proposed.ID, poolVersion, proposed.Incarnation,
	); cleanupErr != nil {
		return createFinished, errors.Join(errCreateRaced, cleanupErr)
	}

	return createRaced, errCreateRaced
}

// createOwnDocument reports whether the document at key now holds this call's
// incarnation because this call created it.
func (client *Client) createOwnDocument(
	ctx context.Context,
	key, poolID string,
	data []byte,
	incarnation string,
) (storage.Version, bool, error) {
	version, err := client.store.CreateIfAbsent(ctx, key, data)

	switch {
	case err == nil:
		return version, true, nil
	case errors.Is(err, storage.ErrPreconditionFailed):
		return "", false, nil
	case !errors.Is(err, storage.ErrUnknownOutcome):
		return "", false, err
	}

	current, version, getErr := client.store.Get(ctx, key)
	if errors.Is(getErr, storage.ErrNotFound) {
		return "", false, nil
	}

	if getErr != nil {
		return "", false, getErr
	}

	observed, decodeErr := documentIncarnation(key, poolID, current)
	if decodeErr != nil {
		return "", false, decodeErr
	}

	return version, observed == incarnation, nil
}

// deleteOwnDocument removes a document of this incarnation. It never deletes a
// document that now carries another incarnation.
func (client *Client) deleteOwnDocument(
	ctx context.Context,
	key, poolID string,
	version storage.Version,
	incarnation string,
) error {
	for attempt := 1; ; attempt++ {
		err := client.store.DeleteIfMatch(ctx, key, version)
		if err == nil || errors.Is(err, storage.ErrNotFound) {
			return nil
		}

		if !errors.Is(err, storage.ErrPreconditionFailed) && !errors.Is(err, storage.ErrUnknownOutcome) {
			return err
		}

		data, current, getErr := client.store.Get(ctx, key)
		if errors.Is(getErr, storage.ErrNotFound) {
			return nil
		}

		if getErr != nil {
			return getErr
		}

		observed, decodeErr := documentIncarnation(key, poolID, data)
		if decodeErr != nil {
			return decodeErr
		}

		if observed != incarnation {
			return nil
		}

		version = current

		if client.attemptsExhausted(attempt) {
			return contentionError("create pool", poolID, "", err)
		}

		if waitErr := client.waitRetry(ctx, attempt-1); waitErr != nil {
			return waitErr
		}
	}
}

func documentIncarnation(key, poolID string, data []byte) (string, error) {
	if strings.HasPrefix(key, poolsPrefix) {
		pool, err := decodePoolDocument(data, poolID)
		return pool.Incarnation, err
	}

	reservations, err := decodeReservationsDocument(data, poolID)

	return reservations.Incarnation, err
}

// poolDocuments is one read of both documents of a pool; nil means absent.
type poolDocuments struct {
	pool                *poolDocument
	poolVersion         storage.Version
	reservations        *reservationsDocument
	reservationsVersion storage.Version
}

func (documents poolDocuments) usable() bool {
	return documents.pool != nil && documents.reservations != nil &&
		documents.pool.Incarnation == documents.reservations.Incarnation
}

// publicPool returns a usable pool with its concrete policy: the strategy
// comes from the pool document and the cooldown from the reservations document.
func (documents poolDocuments) publicPool() Pool {
	pool := clonePool(documents.pool.Pool)
	pool.Policy.Cooldown = documents.reservations.Cooldown

	return pool
}

func (client *Client) readPoolDocuments(ctx context.Context, poolID string) (poolDocuments, error) {
	var documents poolDocuments

	data, version, err := client.store.Get(ctx, poolKey(poolID))
	switch {
	case errors.Is(err, storage.ErrNotFound):
	case err != nil:
		return poolDocuments{}, err
	default:
		pool, decodeErr := decodePoolDocument(data, poolID)
		if decodeErr != nil {
			return poolDocuments{}, decodeErr
		}

		documents.pool, documents.poolVersion = &pool, version
	}

	data, version, err = client.store.Get(ctx, reservationsKey(poolID))
	switch {
	case errors.Is(err, storage.ErrNotFound):
	case err != nil:
		return poolDocuments{}, err
	default:
		reservations, decodeErr := decodeReservationsDocument(data, poolID)
		if decodeErr != nil {
			return poolDocuments{}, decodeErr
		}

		documents.reservations, documents.reservationsVersion = &reservations, version
	}

	return documents, nil
}

func validatePoolSpec(spec PoolSpec) error {
	if err := validatePoolID(spec.ID); err != nil {
		return err
	}

	if err := validatePolicy(spec.Policy, true); err != nil {
		return err
	}

	if spec.ParentPool == "" {
		if spec.Request.Kind != "" || spec.Request.CIDR.IsValid() || spec.Request.PrefixLength != 0 {
			return fmt.Errorf("root pool cannot specify parent request: %w", ErrInvalidArgument)
		}

		_, err := canonicalPool(Pool{ID: spec.ID, CIDRs: spec.CIDRs})

		return err
	}

	if spec.ParentPool == spec.ID {
		return fmt.Errorf("pool cannot be its own parent: %w", ErrInvalidArgument)
	}

	if err := validatePoolID(spec.ParentPool); err != nil {
		return err
	}

	if len(spec.CIDRs) != 0 {
		return fmt.Errorf("child pool cannot specify CIDRs: %w", ErrInvalidArgument)
	}

	request, err := canonicalAllocationRequest(AllocationSpec{
		PrefixLength: spec.Request.PrefixLength,
		CIDR:         spec.Request.CIDR,
	})
	if err != nil || (spec.Request.Kind != "" && spec.Request.Kind != request.Kind) {
		return fmt.Errorf("invalid child pool request: %w", ErrInvalidArgument)
	}

	return nil
}

func reservedAllocationID(childID, incarnation string) string {
	return "pool:" + childID + ":" + incarnation
}

// poolMatchesSpec reports whether a create of spec is satisfied by the stored
// pool. reservations is the stored reservations document, if any; the
// cooldown is compared only when it belongs to the stored pool's incarnation,
// so residue still takes the pool_incomplete path.
func poolMatchesSpec(stored poolDocument, reservations *reservationsDocument, spec PoolSpec) bool {
	if stored.Description != spec.Description ||
		!maps.Equal(stored.Labels, cloneLabels(spec.Labels)) ||
		!policyMatchesSpec(stored, reservations, spec.Policy) {
		return false
	}

	if spec.ParentPool == "" {
		expected, err := canonicalPool(Pool{
			ID:          spec.ID,
			CIDRs:       append([]netip.Prefix(nil), spec.CIDRs...),
			Description: spec.Description,
			Labels:      cloneLabels(spec.Labels),
			// The policy was matched above, where unset fields match any value.
			Policy: stored.Policy,
		})

		return err == nil && poolsEqual(stored.Pool, expected)
	}

	if stored.Parent == nil || stored.Parent.PoolID != spec.ParentPool ||
		len(stored.CIDRs) != 1 {
		return false
	}

	childID, incarnation, ok := parseReservedAllocationID(stored.Parent.AllocationID)
	if !ok || childID != spec.ID || incarnation != stored.Incarnation {
		return false
	}

	request, err := canonicalAllocationRequest(AllocationSpec{
		PrefixLength: spec.Request.PrefixLength,
		CIDR:         spec.Request.CIDR,
	})
	if err != nil {
		return false
	}

	if request.Kind == RequestNext {
		return stored.CIDRs[0].Bits() == request.PrefixLength
	}

	return stored.CIDRs[0] == request.CIDR
}

// policyMatchesSpec reports whether every policy field set in the spec equals
// the stored value; unset fields match any stored value.
func policyMatchesSpec(stored poolDocument, reservations *reservationsDocument, policy PoolPolicy) bool {
	if policy.Strategy != StrategyDefault && policy.Strategy != stored.Policy.Strategy {
		return false
	}

	if policy.Cooldown.Mode == CooldownNone ||
		reservations == nil || reservations.Incarnation != stored.Incarnation {
		return true
	}

	return policy.Cooldown == reservations.Cooldown
}

func poolExistsError(poolID string) error {
	return &OpError{
		Op: "create pool", Pool: poolID, Reason: "pool_exists", Err: ErrConflict,
	}
}

// GetPool returns one usable pool definition. A pool document without a
// reservations document of the same incarnation is residue, not a pool, and
// is reported as ErrNotFound.
func (client *Client) GetPool(ctx context.Context, poolID string) (Pool, error) {
	if err := validateClient(client); err != nil {
		return Pool{}, err
	}

	if err := validatePoolID(poolID); err != nil {
		return Pool{}, &OpError{Op: "get pool", Pool: poolID, Err: err}
	}

	documents, err := client.readPoolDocuments(ctx, poolID)
	if err != nil {
		return Pool{}, err
	}

	if !documents.usable() {
		return Pool{}, &OpError{Op: "get pool", Pool: poolID, Err: ErrNotFound}
	}

	return documents.publicPool(), nil
}

// ListPools returns all pool definitions sorted by ID.
func (client *Client) ListPools(ctx context.Context) ([]Pool, error) {
	if err := validateClient(client); err != nil {
		return nil, err
	}

	keys, err := client.store.List(ctx, poolsPrefix)
	if err != nil {
		return nil, err
	}

	pools := make([]Pool, 0, len(keys))
	for _, key := range keys {
		poolID, ok := idFromKey(key, poolsPrefix)
		if !ok {
			continue
		}

		pool, getErr := client.GetPool(ctx, poolID)
		if errors.Is(getErr, ErrNotFound) {
			continue
		}

		if getErr != nil {
			return nil, getErr
		}

		pools = append(pools, pool)
	}

	slices.SortFunc(pools, func(left, right Pool) int { return strings.Compare(left.ID, right.ID) })

	return pools, nil
}

// UpdatePoolMetadata changes only a pool's description and labels.
func (client *Client) UpdatePoolMetadata(
	ctx context.Context,
	poolID, description string,
	labels map[string]string,
) error {
	if err := validateClient(client); err != nil {
		return err
	}

	if err := validatePoolID(poolID); err != nil {
		return &OpError{Op: "update pool", Pool: poolID, Err: err}
	}

	lock := client.poolLock(poolID)
	if err := lock.LockContext(ctx); err != nil {
		return err
	}
	defer lock.Unlock()

	var last error

	for attempt := 1; ; attempt++ {
		data, version, err := client.store.Get(ctx, poolKey(poolID))
		if err != nil {
			return mapStorageError("update pool", poolID, "", err)
		}

		stored, err := decodePoolDocument(data, poolID)
		if err != nil {
			return err
		}

		stored.Description = description
		stored.Labels = cloneLabels(labels)

		next, err := encodePoolDocument(stored)
		if err != nil {
			return err
		}

		_, err = client.store.PutIfMatch(ctx, poolKey(poolID), next, version)
		if err == nil {
			return nil
		}

		if errors.Is(err, storage.ErrUnknownOutcome) {
			current, _, getErr := client.store.Get(ctx, poolKey(poolID))
			if getErr == nil {
				observed, decodeErr := decodePoolDocument(current, poolID)
				if decodeErr == nil && observed.Description == description &&
					maps.Equal(observed.Labels, labels) {
					return nil
				}
			}
		}

		if !errors.Is(err, storage.ErrPreconditionFailed) &&
			!errors.Is(err, storage.ErrUnknownOutcome) {
			return mapStorageError("update pool", poolID, "", err)
		}

		last = err
		if client.attemptsExhausted(attempt) {
			return contentionError("update pool", poolID, "", last)
		}

		if err := client.waitRetry(ctx, attempt-1); err != nil {
			return err
		}
	}
}

// DeletePool removes a pool and releases its incarnation-keyed carve-out. It
// is also the recovery path for every residue an interrupted create or delete
// can leave: (a) a complete empty pool is deleted reservations first, then the
// pool document, then the carve-out; (b) a pool document without reservations
// of its incarnation is deleted and its carve-out released; (c) an empty
// reservations document without its pool document is deleted (a non-empty one
// is ErrConflict with Reason pool_not_empty). An in-flight creator whose
// reservations document is removed this way withdraws its own pool document
// after publishing it. If no document exists, DeletePool returns ErrNotFound.
func (client *Client) DeletePool(ctx context.Context, poolID string) error {
	if err := validateClient(client); err != nil {
		return err
	}

	if err := validatePoolID(poolID); err != nil {
		return &OpError{Op: "delete pool", Pool: poolID, Err: err}
	}

	foundAny := false

	for attempt := 1; ; attempt++ {
		found, retry, err := client.deletePoolAttempt(ctx, poolID)
		foundAny = foundAny || found

		if err == nil {
			if foundAny {
				return nil
			}

			return &OpError{Op: "delete pool", Pool: poolID, Err: ErrNotFound}
		}

		if !retry {
			return err
		}

		if client.attemptsExhausted(attempt) {
			return contentionError("delete pool", poolID, "", err)
		}

		if waitErr := client.waitRetry(ctx, attempt-1); waitErr != nil {
			return waitErr
		}
	}
}

func (client *Client) deletePoolAttempt(
	ctx context.Context,
	poolID string,
) (found, retry bool, result error) {
	lock := client.poolLock(poolID)
	if err := lock.LockContext(ctx); err != nil {
		return false, false, err
	}

	documents, err := client.readPoolDocuments(ctx, poolID)
	if err != nil {
		lock.Unlock()
		return false, false, err
	}

	if documents.pool == nil && documents.reservations == nil {
		lock.Unlock()
		return false, false, nil
	}

	if reservations := documents.reservations; reservations != nil && len(reservations.Allocations) != 0 {
		lock.Unlock()

		if documents.usable() {
			if validateErr := validate(*documents.pool, *reservations); validateErr != nil {
				return true, false, validateErr
			}
		}

		return true, false, &OpError{
			Op: "delete pool", Pool: poolID, Reason: "pool_not_empty", Err: ErrConflict,
		}
	}

	if documents.reservations != nil {
		if retry, err := client.deletePoolDocument(
			ctx, reservationsKey(poolID), documents.reservationsVersion,
		); err != nil {
			lock.Unlock()
			return true, retry, err
		}

		if documents.pool == nil {
			// Shape (c): a creator of this incarnation may have published its pool
			// document meanwhile; finish the delete as shape (b) in that case.
			deletedIncarnation := documents.reservations.Incarnation

			documents, err = client.readPoolDocuments(ctx, poolID)
			if err != nil || documents.pool == nil || documents.pool.Incarnation != deletedIncarnation {
				lock.Unlock()
				return true, false, err
			}
		}
	}

	pool := documents.pool
	if retry, err := client.deletePoolDocument(ctx, poolKey(poolID), documents.poolVersion); err != nil {
		lock.Unlock()
		return true, retry, err
	}

	lock.Unlock()

	if pool.Parent != nil {
		releaseErr := client.Release(ctx, pool.Parent.PoolID, pool.Parent.AllocationID)
		if releaseErr != nil && !errors.Is(releaseErr, ErrNotFound) {
			return true, false, releaseErr
		}
	}

	return true, false, nil
}

// deletePoolDocument deletes one document at the version read. A precondition
// failure or an unknown outcome that left the document in place asks the
// caller to re-read and retry.
func (client *Client) deletePoolDocument(
	ctx context.Context,
	key string,
	version storage.Version,
) (retry bool, result error) {
	err := client.store.DeleteIfMatch(ctx, key, version)

	switch {
	case err == nil, errors.Is(err, storage.ErrNotFound):
		return false, nil
	case errors.Is(err, storage.ErrPreconditionFailed):
		return true, err
	case errors.Is(err, storage.ErrUnknownOutcome):
		if _, _, getErr := client.store.Get(ctx, key); errors.Is(getErr, storage.ErrNotFound) {
			return false, nil
		} else if getErr != nil {
			return false, getErr
		}

		return true, err
	default:
		return false, err
	}
}

func idFromKey(key, prefix string) (string, bool) {
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, documentSuffix) {
		return "", false
	}

	id := strings.TrimSuffix(strings.TrimPrefix(key, prefix), documentSuffix)

	return id, !strings.Contains(id, "/") && validatePoolID(id) == nil
}

func clonePool(pool Pool) Pool {
	pool.CIDRs = append([]netip.Prefix(nil), pool.CIDRs...)

	pool.Labels = cloneLabels(pool.Labels)
	if pool.Parent != nil {
		parent := *pool.Parent
		pool.Parent = &parent
	}

	return pool
}

func poolsEqual(left, right Pool) bool {
	return left.ID == right.ID &&
		left.Family == right.Family &&
		slices.Equal(left.CIDRs, right.CIDRs) &&
		parentEqual(left.Parent, right.Parent) &&
		left.Description == right.Description &&
		maps.Equal(left.Labels, right.Labels) &&
		left.Policy == right.Policy
}

func parentEqual(left, right *ParentRef) bool {
	if left == nil || right == nil {
		return left == right
	}

	return *left == *right
}
