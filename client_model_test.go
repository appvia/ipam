// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/local"
	"github.com/appvia/ipam/storage/memory"
)

type lifecycleFaultStore struct {
	storage.Storage
	mu      sync.Mutex
	random  *rand.Rand
	enabled bool
}

func (store *lifecycleFaultStore) setEnabled(enabled bool) {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.enabled = enabled
}

func (store *lifecycleFaultStore) roll() int {
	store.mu.Lock()
	defer store.mu.Unlock()

	if !store.enabled {
		return 99
	}

	return store.random.Intn(100)
}

func (store *lifecycleFaultStore) CreateIfAbsent(
	ctx context.Context,
	key string,
	data []byte,
) (storage.Version, error) {
	switch roll := store.roll(); {
	case roll < 5:
		return "", fmt.Errorf("injected before create: %w", storage.ErrUnknownOutcome)
	case roll < 12:
		version, err := store.Storage.CreateIfAbsent(ctx, key, data)
		if err == nil {
			return "", fmt.Errorf("injected after create: %w", storage.ErrUnknownOutcome)
		}

		return version, err
	case roll < 16:
		_, _ = store.Storage.CreateIfAbsent(ctx, key, data)

		return "", errInjectedCrash
	default:
		return store.Storage.CreateIfAbsent(ctx, key, data)
	}
}

func (store *lifecycleFaultStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	switch roll := store.roll(); {
	case roll < 8:
		return "", fmt.Errorf("injected put conflict: %w", storage.ErrPreconditionFailed)
	case roll < 14:
		return "", fmt.Errorf("injected before put: %w", storage.ErrUnknownOutcome)
	case roll < 22:
		_, err := store.Storage.PutIfMatch(ctx, key, data, version)
		if err == nil {
			return "", fmt.Errorf("injected after put: %w", storage.ErrUnknownOutcome)
		}

		return "", errors.Join(storage.ErrUnknownOutcome, err)
	case roll < 25:
		_, _ = store.Storage.PutIfMatch(ctx, key, data, version)

		return "", errInjectedCrash
	default:
		return store.Storage.PutIfMatch(ctx, key, data, version)
	}
}

func (store *lifecycleFaultStore) DeleteIfMatch(
	ctx context.Context,
	key string,
	version storage.Version,
) error {
	switch roll := store.roll(); {
	case roll < 5:
		return fmt.Errorf("injected before delete: %w", storage.ErrUnknownOutcome)
	case roll < 12:
		if err := store.Storage.DeleteIfMatch(ctx, key, version); err != nil {
			return err
		}

		return fmt.Errorf("injected after delete: %w", storage.ErrUnknownOutcome)
	case roll < 16:
		_ = store.Storage.DeleteIfMatch(ctx, key, version)

		return errInjectedCrash
	default:
		return store.Storage.DeleteIfMatch(ctx, key, version)
	}
}

func assertLifecycleInvariants(t *testing.T, store storage.Storage) {
	t.Helper()

	ctx := t.Context()
	pools := make(map[string]poolDocument)
	reservations := make(map[string]reservationsDocument)

	poolKeys, err := store.List(ctx, poolsPrefix)
	assertNoError(t, err)

	for _, key := range poolKeys {
		id, ok := idFromKey(key, poolsPrefix)
		if !ok {
			t.Fatalf("invalid pool key %q", key)
		}

		data, _, getErr := store.Get(ctx, key)
		assertNoError(t, getErr)

		pool, decodeErr := decodePoolDocument(data, id)
		assertNoError(t, decodeErr)

		pools[id] = pool
	}

	reservationKeys, err := store.List(ctx, reservationsPrefix)
	assertNoError(t, err)

	for _, key := range reservationKeys {
		id, ok := idFromKey(key, reservationsPrefix)
		if !ok {
			t.Fatalf("invalid reservations key %q", key)
		}

		data, _, getErr := store.Get(ctx, key)
		assertNoError(t, getErr)

		document, decodeErr := decodeReservationsDocument(data, id)
		assertNoError(t, decodeErr)

		reservations[id] = document
		if pool, found := pools[id]; found && pool.Incarnation == document.Incarnation {
			if validateErr := validate(pool, document); validateErr != nil {
				t.Fatalf("pool %q violates allocation invariants: %v", id, validateErr)
			}
		} else if len(document.Allocations) != 0 {
			t.Fatalf("reservations %q has allocations without a pool document of its incarnation", id)
		}
	}

	type leaf struct {
		owner string
		cidr  netip.Prefix
	}

	var leaves []leaf

	for poolID, document := range reservations {
		for id, allocation := range document.Allocations {
			if !strings.HasPrefix(id, "pool:") {
				leaves = append(leaves, leaf{poolID + "/" + id, allocation.CIDR})
			}
		}
	}

	for left := range leaves {
		for right := left + 1; right < len(leaves); right++ {
			if leaves[left].cidr.Overlaps(leaves[right].cidr) {
				t.Fatalf("OVERLAP %s %s and %s %s", leaves[left].owner, leaves[left].cidr,
					leaves[right].owner, leaves[right].cidr)
			}
		}
	}

	allocationOwners := make(map[string]string)

	for poolID, document := range reservations {
		for id := range document.Allocations {
			if owner, duplicate := allocationOwners[id]; duplicate {
				t.Fatalf("allocation ID %q appears in pools %q and %q", id, owner, poolID)
			}

			allocationOwners[id] = poolID
		}
	}

	for childID, child := range pools {
		if child.Parent == nil {
			continue
		}

		parentReservations, found := reservations[child.Parent.PoolID]
		if !found {
			t.Fatalf("child %q has no parent reservations document", childID)
		}

		carve, found := parentReservations.Allocations[child.Parent.AllocationID]
		if !found {
			t.Fatalf("child %q exists without carve-out %q (reservations=%v)",
				childID, child.Parent.AllocationID, reservations[childID])
		}

		if len(child.CIDRs) != 1 || child.CIDRs[0] != carve.CIDR {
			t.Fatalf("child %q CIDRs %v do not match carve-out %s", childID, child.CIDRs, carve.CIDR)
		}
	}

	for parentID, document := range reservations {
		for id, carve := range document.Allocations {
			childID, _, childCarve := parseReservedAllocationIDForTest(id)
			if !childCarve {
				continue
			}

			child, exists := pools[childID]
			if !exists {
				continue
			}

			if child.Parent == nil || child.Parent.AllocationID != id {
				// A different incarnation of the same child may leave this safe
				// abandoned carve-out behind.
				continue
			}

			if child.Parent.PoolID != parentID ||
				len(child.CIDRs) != 1 || child.CIDRs[0] != carve.CIDR {
				t.Fatalf("carve-out %q in %q does not exactly match child", id, parentID)
			}
		}
	}
}

func parseReservedAllocationIDForTest(id string) (string, string, bool) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] != "pool" {
		return "", "", false
	}

	return parts[1], parts[2], true
}

func TestLifecycleModelInvariantsAfterEverySequenceStep(t *testing.T) {
	base := memory.New()
	client := New(base, WithSleeper(noSleep))
	mustCreatePool(t, client, "root", "10.0.0.0/16")

	random := rand.New(rand.NewSource(7))
	owned := make(map[string]string)

	for step := range 200 {
		childID := "child-" + strconv.Itoa(random.Intn(4))

		switch random.Intn(5) {
		case 0:
			_, _ = client.CreatePool(t.Context(), PoolSpec{
				ID:         childID,
				ParentPool: "root",
				Request:    Request{Kind: RequestNext, PrefixLength: 22},
			})
		case 1:
			poolID := "root"
			if random.Intn(2) == 0 {
				poolID = childID
			}

			allocation, err := client.Allocate(t.Context(), poolID, AllocationSpec{PrefixLength: 28})
			if err == nil {
				owned[allocation.ID] = poolID
			}
		case 2:
			for id, poolID := range owned {
				if err := client.Release(t.Context(), poolID, id); err == nil || errors.Is(err, ErrNotFound) {
					delete(owned, id)
				}

				break
			}
		case 3:
			for id, poolID := range owned {
				if poolID == childID {
					_ = client.Release(t.Context(), poolID, id)
					delete(owned, id)
				}
			}

			_ = client.DeletePool(t.Context(), childID)
		case 4:
			_, _ = client.Allocate(t.Context(), "root", AllocationSpec{
				CIDR: netip.MustParsePrefix(fmt.Sprintf("10.0.%d.0/24", 200+step%20)),
			})
		}

		assertLifecycleInvariants(t, base)
	}
}

func runLifecycleStress(t *testing.T, seed int64, base storage.Storage, disableLocks bool) {
	t.Helper()

	faults := &lifecycleFaultStore{
		Storage: base,
		random:  rand.New(rand.NewSource(seed)),
	}
	setup := New(base, WithSleeper(noSleep))
	mustCreatePool(t, setup, "root", "10.0.0.0/16")
	faults.setEnabled(true)

	clients := make([]*Client, 6)
	for index := range clients {
		options := []Option{
			WithSleeper(noSleep),
			WithMaxAttempts(20),
			WithRand(rand.New(rand.NewSource(seed*1000 + int64(index) + 1))),
		}
		if disableLocks {
			options = append(options, withoutPoolLocks())
		}

		clients[index] = New(
			faults,
			options...,
		)
	}

	var wait sync.WaitGroup

	for worker := range 6 {
		wait.Add(1)

		go func() {
			defer wait.Done()

			random := rand.New(rand.NewSource(seed*100 + int64(worker)))

			for range 40 {
				childID := "child-" + strconv.Itoa(random.Intn(4))

				switch random.Intn(5) {
				case 0:
					_, _ = clients[worker].CreatePool(t.Context(), PoolSpec{
						ID:         childID,
						ParentPool: "root",
						Request:    Request{Kind: RequestNext, PrefixLength: 22},
					})
				case 1:
					_, _ = clients[worker].Allocate(
						t.Context(),
						"root",
						AllocationSpec{PrefixLength: 28},
					)
				case 2:
					_, _ = clients[worker].Allocate(
						t.Context(),
						childID,
						AllocationSpec{PrefixLength: 28},
					)
				case 3:
					_ = clients[worker].DeletePool(t.Context(), childID)
				case 4:
					ctx, cancel := context.WithCancel(t.Context())
					cancel()

					_, _ = clients[worker].Allocate(ctx, "root", AllocationSpec{PrefixLength: 28})
				}
			}
		}()
	}

	wait.Wait()
	faults.setEnabled(false)

	assertLifecycleInvariants(t, base)
	assertHealthyProgress(t, seed, setup, base)

	for index := range 4 {
		childID := "child-" + strconv.Itoa(index)
		converged := false

		for range 3 {
			allocations, err := setup.ListAllocations(t.Context(), childID)
			if err == nil {
				for _, allocation := range allocations {
					if releaseErr := setup.Release(t.Context(), childID, allocation.ID); releaseErr != nil {
						t.Fatalf(
							"seed %d release %q from %q did not converge: %v",
							seed,
							allocation.ID,
							childID,
							releaseErr,
						)
					}
				}
			} else if !errors.Is(err, ErrNotFound) {
				t.Fatalf("seed %d list %q did not converge: %v", seed, childID, err)
			}

			deleteErr := setup.DeletePool(t.Context(), childID)
			if deleteErr == nil || errors.Is(deleteErr, ErrNotFound) {
				converged = true
				break
			}

			if !conflictReason(deleteErr, "pool_not_empty") {
				t.Fatalf("seed %d delete %q did not converge: %v", seed, childID, deleteErr)
			}
		}

		if !converged {
			t.Fatalf("seed %d delete %q did not converge after retries", seed, childID)
		}
	}

	assertLifecycleInvariants(t, base)
}

// assertHealthyProgress proves no reachable state wedges a pool: a healthy
// client can allocate in every pool, or delete its residue and recreate it.
func assertHealthyProgress(t *testing.T, seed int64, healthy *Client, base storage.Storage) {
	t.Helper()

	for _, poolID := range []string{"root", "child-0", "child-1", "child-2", "child-3"} {
		allocation, err := healthy.Allocate(t.Context(), poolID, AllocationSpec{PrefixLength: 30})

		switch {
		case err == nil:
			assertUsableDocuments(t, seed, base, poolID)
			assertNoError(t, healthy.Release(t.Context(), poolID, allocation.ID))

			continue
		case errors.Is(err, ErrPoolExhausted):
			continue
		case !errors.Is(err, ErrNotFound):
			t.Fatalf("seed %d: healthy Allocate(%s) = %v", seed, poolID, err)
		}

		if deleteErr := healthy.DeletePool(t.Context(), poolID); deleteErr != nil && !errors.Is(deleteErr, ErrNotFound) {
			t.Fatalf("seed %d: healthy DeletePool(%s) on unusable pool = %v", seed, poolID, deleteErr)
		}

		if _, createErr := healthy.CreatePool(t.Context(), PoolSpec{
			ID:         poolID,
			ParentPool: "root",
			Request:    Request{Kind: RequestNext, PrefixLength: 22},
		}); createErr != nil {
			t.Fatalf("seed %d: healthy CreatePool(%s) after delete = %v", seed, poolID, createErr)
		}

		allocation, err = healthy.Allocate(t.Context(), poolID, AllocationSpec{PrefixLength: 30})
		if err != nil {
			t.Fatalf("seed %d: healthy Allocate(%s) after recreate = %v", seed, poolID, err)
		}

		assertUsableDocuments(t, seed, base, poolID)
		assertNoError(t, healthy.Release(t.Context(), poolID, allocation.ID))
	}
}

func assertUsableDocuments(t *testing.T, seed int64, base storage.Storage, poolID string) {
	t.Helper()

	poolData, _, err := base.Get(t.Context(), poolKey(poolID))
	assertNoError(t, err)
	pool, err := decodePoolDocument(poolData, poolID)
	assertNoError(t, err)
	reservationsData, _, err := base.Get(t.Context(), reservationsKey(poolID))
	assertNoError(t, err)
	reservations, err := decodeReservationsDocument(reservationsData, poolID)
	assertNoError(t, err)

	if pool.Incarnation != reservations.Incarnation {
		t.Fatalf("seed %d: usable pool %s has incarnations %s/%s", seed, poolID, pool.Incarnation, reservations.Incarnation)
	}
}

func TestLifecycleStressMemory(t *testing.T) {
	seeds := 20
	if configured, _ := strconv.Atoi(os.Getenv("GATE_SEEDS")); configured > 0 {
		seeds = configured
	}

	start, _ := strconv.Atoi(os.Getenv("GATE_OFFSET"))

	for offset := range seeds {
		seed := start + offset
		runLifecycleStress(t, int64(seed), memory.New(), false)
	}
}

func TestLifecycleStressMemoryNoLocks(t *testing.T) {
	seeds := 20
	if configured, _ := strconv.Atoi(os.Getenv("GATE_SEEDS")); configured > 0 {
		seeds = configured
	}

	start, _ := strconv.Atoi(os.Getenv("GATE_OFFSET"))

	for offset := range seeds {
		seed := start + offset
		runLifecycleStress(t, int64(seed), memory.New(), true)
	}
}

func TestLifecycleStressLocal(t *testing.T) {
	seeds := 3
	if configured, _ := strconv.Atoi(os.Getenv("GATE_LOCAL_SEEDS")); configured > 0 {
		seeds = configured
	}

	start, _ := strconv.Atoi(os.Getenv("GATE_LOCAL_START"))

	for offset := range seeds {
		seed := start + offset
		store, err := local.New(filepath.Join(t.TempDir(), strconv.Itoa(seed)))
		assertNoError(t, err)
		runLifecycleStress(t, int64(1000+seed), store, false)
	}
}
