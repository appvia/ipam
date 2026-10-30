// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/local"
	"github.com/appvia/ipam/storage/memory"
)

// gateStore blocks the first call matching an armed op/key until resumed.
type gateStore struct {
	storage.Storage
	mu    sync.Mutex
	gates map[string]*storeGate
}

type storeGate struct {
	reached chan struct{}
	resume  chan struct{}
	used    bool
}

func newGateStore(base storage.Storage) *gateStore {
	return &gateStore{Storage: base, gates: map[string]*storeGate{}}
}

func (store *gateStore) arm(op, key string) *storeGate {
	store.mu.Lock()
	defer store.mu.Unlock()

	armed := &storeGate{reached: make(chan struct{}), resume: make(chan struct{})}
	store.gates[op+" "+key] = armed

	return armed
}

func (store *gateStore) wait(ctx context.Context, op, key string) {
	store.mu.Lock()

	armed := store.gates[op+" "+key]
	if armed == nil || armed.used {
		store.mu.Unlock()
		return
	}

	armed.used = true
	store.mu.Unlock()
	close(armed.reached)

	select {
	case <-armed.resume:
	case <-ctx.Done():
	}
}

func (store *gateStore) Get(ctx context.Context, key string) ([]byte, storage.Version, error) {
	store.wait(ctx, "get", key)
	return store.Storage.Get(ctx, key)
}

func (store *gateStore) CreateIfAbsent(ctx context.Context, key string, data []byte) (storage.Version, error) {
	store.wait(ctx, "create", key)
	return store.Storage.CreateIfAbsent(ctx, key, data)
}

func (store *gateStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	store.wait(ctx, "put", key)
	return store.Storage.PutIfMatch(ctx, key, data, version)
}

func (store *gateStore) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	store.wait(ctx, "delete", key)
	return store.Storage.DeleteIfMatch(ctx, key, version)
}

// committedWriteStore records only writes that changed the store; a failed
// conditional write (for example losing CreateIfAbsent) changes nothing.
type committedWriteStore struct {
	storage.Storage
	mu   sync.Mutex
	keys []string
}

func (store *committedWriteStore) commit(key string, err error) {
	if err != nil {
		return
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	store.keys = append(store.keys, key)
}

func (store *committedWriteStore) committedKeys() []string {
	store.mu.Lock()
	defer store.mu.Unlock()

	return append([]string(nil), store.keys...)
}

func (store *committedWriteStore) CreateIfAbsent(ctx context.Context, key string, data []byte) (storage.Version, error) {
	version, err := store.Storage.CreateIfAbsent(ctx, key, data)
	store.commit(key, err)

	return version, err
}

func (store *committedWriteStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	next, err := store.Storage.PutIfMatch(ctx, key, data, version)
	store.commit(key, err)

	return next, err
}

func (store *committedWriteStore) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	err := store.Storage.DeleteIfMatch(ctx, key, version)
	store.commit(key, err)

	return err
}

// yieldSleeper lets other goroutines run between retries without wall-clock waits.
func yieldSleeper(ctx context.Context, _ time.Duration) error {
	runtime.Gosched()
	return ctx.Err()
}

func childPoolSpec(id string, length int) PoolSpec {
	return PoolSpec{ID: id, ParentPool: "root", Request: Request{Kind: RequestNext, PrefixLength: length}}
}

func carveOutIDs(t *testing.T, base storage.Storage, parent, child string) []string {
	t.Helper()

	data, _, err := base.Get(context.Background(), reservationsKey(parent))
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}

	assertNoError(t, err)

	document, err := decodeReservationsDocument(data, parent)
	assertNoError(t, err)

	var ids []string

	for id := range document.Allocations {
		if strings.HasPrefix(id, "pool:"+child+":") {
			ids = append(ids, id)
		}
	}

	return ids
}

func storedKeys(t *testing.T, base storage.Storage) []string {
	t.Helper()

	keys, err := base.List(context.Background(), "")
	assertNoError(t, err)

	return keys
}

func readIncarnation(t *testing.T, base storage.Storage, key, poolID string) string {
	t.Helper()

	data, _, err := base.Get(context.Background(), key)
	assertNoError(t, err)

	if strings.HasPrefix(key, poolsPrefix) {
		pool, decodeErr := decodePoolDocument(data, poolID)
		assertNoError(t, decodeErr)

		return pool.Incarnation
	}

	reservations, err := decodeReservationsDocument(data, poolID)
	assertNoError(t, err)

	return reservations.Incarnation
}

func rawDelete(t *testing.T, base storage.Storage, key string) {
	t.Helper()

	_, version, err := base.Get(context.Background(), key)
	assertNoError(t, err)
	assertNoError(t, base.DeleteIfMatch(context.Background(), key, version))
}

func rawCreateReservations(t *testing.T, base storage.Storage, poolID, incarnation string, allocations map[string]Allocation) {
	t.Helper()

	if allocations == nil {
		allocations = map[string]Allocation{}
	}

	data, err := encodeReservationsDocument(reservationsDocument{
		PoolID: poolID, Incarnation: incarnation, Allocations: allocations,
	})
	assertNoError(t, err)
	_, err = base.CreateIfAbsent(context.Background(), reservationsKey(poolID), data)
	assertNoError(t, err)
}

// R3-B1: a stale CreatePool and a stale Allocate that both read pool
// incarnation A before another client's DeletePool must not resurrect A's
// reservations; the parent must never hand the released space out twice.
func TestStaleCreateAndAllocateAfterDeleteNeverOverlap(t *testing.T) {
	ctx := t.Context()
	base := memory.New()
	admin := New(base, WithSleeper(noSleep))
	mustCreatePool(t, admin, "root", "10.0.0.0/16")

	spec := childPoolSpec("c", 24)
	_, err := admin.CreatePool(ctx, spec)
	assertNoError(t, err)

	creatorStore := newGateStore(base)
	creatorGate := creatorStore.arm("get", reservationsKey("c"))
	allocatorStore := newGateStore(base)
	allocatorGate := allocatorStore.arm("get", reservationsKey("c"))

	createDone := make(chan error, 1)

	go func() {
		_, createErr := New(creatorStore, WithSleeper(noSleep)).CreatePool(ctx, spec)
		createDone <- createErr
	}()

	<-creatorGate.reached

	type allocateResult struct {
		allocation Allocation
		err        error
	}

	allocateDone := make(chan allocateResult, 1)

	go func() {
		allocation, allocateErr := New(allocatorStore, WithSleeper(noSleep)).
			Allocate(ctx, "c", AllocationSpec{PrefixLength: 26})
		allocateDone <- allocateResult{allocation, allocateErr}
	}()

	<-allocatorGate.reached

	assertNoError(t, New(base, WithSleeper(noSleep)).DeletePool(ctx, "c"))

	close(creatorGate.resume)

	createErr := <-createDone

	close(allocatorGate.resume)

	stale := <-allocateDone

	rootAllocation, err := admin.Allocate(ctx, "root", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)

	if stale.err == nil && rootAllocation.CIDR.Overlaps(stale.allocation.CIDR) {
		t.Fatalf("OVERLAP: stale child allocation %s, root allocation %s (keys %v)",
			stale.allocation.CIDR, rootAllocation.CIDR, storedKeys(t, base))
	}

	if createErr == nil {
		child, getErr := admin.GetPool(ctx, "c")
		if getErr != nil {
			t.Fatalf("CreatePool(c) succeeded but GetPool(c) = %v", getErr)
		}

		if child.CIDRs[0].Overlaps(rootAllocation.CIDR) {
			t.Fatalf("OVERLAP: child %s, root allocation %s", child.CIDRs[0], rootAllocation.CIDR)
		}
	}

	assertLifecycleInvariants(t, base)
}

// R3-B2: two creators with different specs; the loser runs while the winner
// sits between its reservations create and its pool publication. The loser
// must write nothing and the winner's pool must stay allocatable.
func TestDifferentSpecCreateRaceLoserNeverWedgesWinner(t *testing.T) {
	ctx := t.Context()
	base := memory.New()

	winnerStore := newGateStore(base)
	winnerGate := winnerStore.arm("create", poolKey("p"))
	loserRecorder := &recordingStore{Storage: base}
	loserStore := newGateStore(loserRecorder)
	loserGate := loserStore.arm("put", reservationsKey("p"))

	winnerDone := make(chan error, 1)

	go func() {
		_, createErr := New(winnerStore, WithSleeper(noSleep)).CreatePool(ctx, PoolSpec{
			ID: "p", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
		})
		winnerDone <- createErr
	}()

	<-winnerGate.reached

	loserDone := make(chan error, 1)

	go func() {
		_, createErr := New(loserStore, WithSleeper(noSleep)).CreatePool(ctx, PoolSpec{
			ID: "p", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")},
		})
		loserDone <- createErr
	}()

	var winnerErr, loserErr error

	select {
	case <-loserGate.reached:
		close(winnerGate.resume)

		winnerErr = <-winnerDone

		close(loserGate.resume)

		loserErr = <-loserDone
	case loserErr = <-loserDone:
		close(winnerGate.resume)

		winnerErr = <-winnerDone
	}

	assertNoError(t, winnerErr)

	if !conflictReason(loserErr, "pool_exists") && !conflictReason(loserErr, "pool_incomplete") {
		t.Fatalf("loser CreatePool() error = %v, want pool_exists or pool_incomplete", loserErr)
	}

	if writes := loserRecorder.writeKeys(); len(writes) != 0 {
		t.Fatalf("loser wrote %v, want no writes", writes)
	}

	if _, err := New(base, WithSleeper(noSleep)).Allocate(ctx, "p", AllocationSpec{PrefixLength: 24}); err != nil {
		t.Fatalf("winner pool not allocatable after different-spec race: %v", err)
	}
}

// N1: operator Release of a carve-out whose CreatePool already wrote the child
// reservations document must be refused, so the child can never go live on
// released space.
func TestOperatorReleaseRefusedForInflightCreate(t *testing.T) {
	ctx := t.Context()
	base := memory.New()
	admin := New(base, WithSleeper(noSleep))
	mustCreatePool(t, admin, "root", "10.0.0.0/16")

	creatorStore := newGateStore(base)
	creatorGate := creatorStore.arm("create", poolKey("c"))
	done := make(chan error, 1)

	go func() {
		_, createErr := New(creatorStore, WithSleeper(noSleep)).CreatePool(ctx, childPoolSpec("c", 24))
		done <- createErr
	}()

	<-creatorGate.reached

	ids := carveOutIDs(t, base, "root", "c")
	if len(ids) != 1 {
		t.Fatalf("in-flight carve-outs = %v, want 1", ids)
	}

	releaseErr := admin.Release(ctx, "root", ids[0])

	close(creatorGate.resume)
	assertNoError(t, <-done)

	if !conflictReason(releaseErr, "pool_in_use") {
		t.Fatalf("operator Release(in-flight carve-out) = %v, want pool_in_use", releaseErr)
	}

	childAllocation, err := admin.Allocate(ctx, "c", AllocationSpec{PrefixLength: 26})
	assertNoError(t, err)
	rootAllocation, err := admin.Allocate(ctx, "root", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)

	if childAllocation.CIDR.Overlaps(rootAllocation.CIDR) {
		t.Fatalf("OVERLAP: child %s root %s", childAllocation.CIDR, rootAllocation.CIDR)
	}
}

// Many creators race on the local backend; equal specs all succeed, different
// specs produce exactly one winner and pool_exists for every loser.
func TestCreatePoolRaceLocalLoop(t *testing.T) {
	iterations := 300
	if configured, _ := strconv.Atoi(os.Getenv("GATE_CREATE_ITERATIONS")); configured > 0 {
		iterations = configured
	}

	for iteration := range iterations {
		store, err := local.New(filepath.Join(t.TempDir(), strconv.Itoa(iteration)))
		assertNoError(t, err)

		t.Cleanup(func() { _ = store.Close() })

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		results := make([]error, 8)
		pools := make([]Pool, 8)

		var wait sync.WaitGroup

		for worker := range 8 {
			wait.Add(1)

			go func() {
				defer wait.Done()

				cidr := "10.0.0.0/16"
				if iteration%2 == 1 && worker%2 == 1 {
					cidr = "10.1.0.0/16"
				}

				pools[worker], results[worker] = New(store, WithSleeper(yieldSleeper), WithMaxAttempts(0)).
					CreatePool(ctx, PoolSpec{ID: "p", CIDRs: []netip.Prefix{netip.MustParsePrefix(cidr)}})
			}()
		}

		wait.Wait()
		cancel()

		winners := map[netip.Prefix]bool{}

		for worker, result := range results {
			switch {
			case result == nil:
				winners[pools[worker].CIDRs[0]] = true
			case iteration%2 == 1 && conflictReason(result, "pool_exists"):
			default:
				t.Fatalf("iteration %d worker %d: CreatePool() = %v", iteration, worker, result)
			}
		}

		if len(winners) != 1 {
			t.Fatalf("iteration %d: winners %v, want exactly one", iteration, winners)
		}

		if _, err := New(store, WithSleeper(noSleep)).Allocate(t.Context(), "p", AllocationSpec{PrefixLength: 24}); err != nil {
			t.Fatalf("iteration %d: Allocate after create race: %v", iteration, err)
		}
	}
}

func TestCreatePoolExistingDifferentSpecWritesNothing(t *testing.T) {
	base := memory.New()
	admin := New(base, WithSleeper(noSleep))
	mustCreatePool(t, admin, "root", "10.0.0.0/16")
	mustCreatePool(t, admin, "other", "10.1.0.0/16")
	mustCreatePool(t, admin, "prod", "10.2.0.0/16")
	_, err := admin.CreatePool(t.Context(), childPoolSpec("c", 24))
	assertNoError(t, err)

	for _, spec := range []PoolSpec{
		{ID: "c", ParentPool: "other", Request: Request{Kind: RequestNext, PrefixLength: 24}},
		childPoolSpec("c", 25),
		{ID: "prod", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/16")}},
	} {
		recorder := &recordingStore{Storage: base}

		_, err := New(recorder, WithSleeper(noSleep)).CreatePool(t.Context(), spec)
		assertConflictReason(t, err, "pool_exists")

		if writes := recorder.writeKeys(); len(writes) != 0 {
			t.Fatalf("CreatePool(%+v) wrote %v, want no writes", spec, writes)
		}
	}

	if ids := carveOutIDs(t, base, "other", "c"); len(ids) != 0 {
		t.Fatalf("conflicting create left carve-outs %v in other", ids)
	}
}

func TestCreatePoolIncompleteStatesRetryThenReportWithoutWrites(t *testing.T) {
	shapes := []struct {
		name  string
		shape func(t *testing.T, base storage.Storage)
	}{
		{"pool document without reservations", func(t *testing.T, base storage.Storage) {
			rawDelete(t, base, reservationsKey("prod"))
		}},
		{"reservations without pool document", func(t *testing.T, base storage.Storage) {
			rawDelete(t, base, poolKey("prod"))
		}},
		{"mismatched incarnations", func(t *testing.T, base storage.Storage) {
			rawDelete(t, base, reservationsKey("prod"))
			rawCreateReservations(t, base, "prod", strings.Repeat("b", 32), nil)
		}},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			base := memory.New()
			admin := New(base, WithSleeper(noSleep))
			original := mustCreatePool(t, admin, "prod", "10.0.0.0/16")
			shape.shape(t, base)

			recorder := &recordingStore{Storage: base}
			sleeps := 0
			client := New(recorder, WithMaxAttempts(3), WithSleeper(func(ctx context.Context, _ time.Duration) error {
				sleeps++
				return ctx.Err()
			}))

			_, err := client.CreatePool(t.Context(), PoolSpec{ID: "prod", CIDRs: original.CIDRs})
			assertConflictReason(t, err, "pool_incomplete")

			if writes := recorder.writeKeys(); len(writes) != 0 {
				t.Fatalf("CreatePool() on incomplete pool wrote %v", writes)
			}

			if sleeps != 2 {
				t.Fatalf("backoff sleeps = %d, want 2 (attempt bound 3)", sleeps)
			}

			if _, err := admin.Allocate(t.Context(), "prod", AllocationSpec{PrefixLength: 24}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Allocate() on incomplete pool = %v, want ErrNotFound", err)
			}

			assertNoError(t, admin.DeletePool(t.Context(), "prod"))

			if keys := storedKeys(t, base); len(keys) != 0 {
				t.Fatalf("DeletePool() left %v", keys)
			}

			mustCreatePool(t, admin, "prod", "10.0.0.0/16")

			if _, err := admin.Allocate(t.Context(), "prod", AllocationSpec{PrefixLength: 24}); err != nil {
				t.Fatalf("Allocate() after recreate = %v", err)
			}
		})
	}
}

func TestCreatePoolIncompleteResolvesWhenCreatorFinishes(t *testing.T) {
	base := memory.New()
	creatorStore := newGateStore(base)
	creatorGate := creatorStore.arm("create", poolKey("p"))
	spec := PoolSpec{ID: "p", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")}}
	done := make(chan error, 1)

	go func() {
		_, createErr := New(creatorStore, WithSleeper(noSleep)).CreatePool(t.Context(), spec)
		done <- createErr
	}()

	<-creatorGate.reached

	recorder := &recordingStore{Storage: base}
	resumed := false
	waiter := New(recorder, WithSleeper(func(ctx context.Context, _ time.Duration) error {
		if !resumed {
			resumed = true

			close(creatorGate.resume)
			assertNoError(t, <-done)
		}

		return ctx.Err()
	}))

	pool, err := waiter.CreatePool(t.Context(), spec)
	assertNoError(t, err)

	if !resumed || pool.CIDRs[0] != spec.CIDRs[0] {
		t.Fatalf("CreatePool() = %+v resumed=%v", pool, resumed)
	}

	if writes := recorder.writeKeys(); len(writes) != 0 {
		t.Fatalf("waiting creator wrote %v", writes)
	}
}

func TestDeletePoolCleansEveryResidueShape(t *testing.T) {
	setup := func(t *testing.T) (*memory.Store, *Client, Pool) {
		t.Helper()

		base := memory.New()
		admin := New(base, WithSleeper(noSleep))
		mustCreatePool(t, admin, "root", "10.0.0.0/16")
		child, err := admin.CreatePool(t.Context(), childPoolSpec("c", 24))
		assertNoError(t, err)

		return base, admin, child
	}

	assertClean := func(t *testing.T, base storage.Storage, admin *Client) {
		t.Helper()

		for _, key := range storedKeys(t, base) {
			if key != poolKey("root") && key != reservationsKey("root") {
				t.Fatalf("residue %q left after DeletePool (keys %v)", key, storedKeys(t, base))
			}
		}

		if ids := carveOutIDs(t, base, "root", "c"); len(ids) != 0 {
			t.Fatalf("carve-outs left: %v", ids)
		}

		if err := admin.DeletePool(t.Context(), "c"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second DeletePool() = %v, want ErrNotFound", err)
		}
	}

	t.Run("a complete pool", func(t *testing.T) {
		base, admin, _ := setup(t)
		assertNoError(t, admin.DeletePool(t.Context(), "c"))
		assertClean(t, base, admin)
	})

	t.Run("b pool document without reservations", func(t *testing.T) {
		base, admin, _ := setup(t)
		rawDelete(t, base, reservationsKey("c"))
		assertNoError(t, admin.DeletePool(t.Context(), "c"))
		assertClean(t, base, admin)
	})

	t.Run("b pool document with foreign reservations", func(t *testing.T) {
		base, admin, _ := setup(t)
		rawDelete(t, base, reservationsKey("c"))
		rawCreateReservations(t, base, "c", strings.Repeat("e", 32), nil)
		assertNoError(t, admin.DeletePool(t.Context(), "c"))
		assertClean(t, base, admin)
	})

	t.Run("c empty reservations without pool document", func(t *testing.T) {
		base, admin, child := setup(t)
		rawDelete(t, base, poolKey("c"))
		assertConflictReason(t, admin.Release(t.Context(), "root", child.Parent.AllocationID), "pool_in_use")
		assertNoError(t, admin.DeletePool(t.Context(), "c"))
		assertNoError(t, admin.Release(t.Context(), "root", child.Parent.AllocationID))
		assertClean(t, base, admin)
	})

	t.Run("c non-empty reservations without pool document", func(t *testing.T) {
		base := memory.New()
		admin := New(base, WithSleeper(noSleep))
		mustCreatePool(t, admin, "c", "10.0.0.0/16")
		_, err := admin.Allocate(t.Context(), "c", AllocationSpec{PrefixLength: 24})
		assertNoError(t, err)
		rawDelete(t, base, poolKey("c"))

		recorder := &recordingStore{Storage: base}
		assertConflictReason(t, New(recorder, WithSleeper(noSleep)).DeletePool(t.Context(), "c"), "pool_not_empty")

		if writes := recorder.writeKeys(); len(writes) != 0 {
			t.Fatalf("DeletePool() wrote %v for non-empty residue", writes)
		}
	})

	t.Run("nothing", func(t *testing.T) {
		admin := New(memory.New(), WithSleeper(noSleep))
		if err := admin.DeletePool(t.Context(), "c"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("DeletePool() = %v, want ErrNotFound", err)
		}
	})
}

func TestUnusablePoolIsNotFoundWithoutWrites(t *testing.T) {
	base := memory.New()
	admin := New(base, WithSleeper(noSleep))
	mustCreatePool(t, admin, "prod", "10.0.0.0/16")
	rawDelete(t, base, reservationsKey("prod"))
	rawCreateReservations(t, base, "prod", strings.Repeat("d", 32), nil)

	recorder := &recordingStore{Storage: base}
	client := New(recorder, WithSleeper(noSleep))

	if _, err := client.Allocate(t.Context(), "prod", AllocationSpec{PrefixLength: 24}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Allocate() = %v, want ErrNotFound", err)
	}

	if _, err := client.GetPool(t.Context(), "prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPool() = %v, want ErrNotFound", err)
	}

	if err := client.Release(t.Context(), "prod", testAllocationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Release() = %v, want ErrNotFound", err)
	}

	if writes := recorder.writeKeys(); len(writes) != 0 {
		t.Fatalf("operations on unusable pool wrote %v", writes)
	}
}

// A creator whose in-flight reservations document is removed by a concurrent
// DeletePool must notice it after publishing (post-check), withdraw its pool
// document and carve-out, and either retry or report pool_create_raced.
func TestCreatePoolPostCheckAbortsAfterReservationsDeleted(t *testing.T) {
	for _, attempts := range []int{1, 8} {
		t.Run(fmt.Sprintf("attempts=%d", attempts), func(t *testing.T) {
			ctx := t.Context()
			base := memory.New()
			admin := New(base, WithSleeper(noSleep))
			mustCreatePool(t, admin, "root", "10.0.0.0/16")

			creatorStore := newGateStore(base)
			creatorGate := creatorStore.arm("create", poolKey("c"))

			type createResult struct {
				pool Pool
				err  error
			}

			done := make(chan createResult, 1)

			go func() {
				pool, createErr := New(creatorStore, WithSleeper(noSleep), WithMaxAttempts(attempts)).
					CreatePool(ctx, childPoolSpec("c", 24))
				done <- createResult{pool, createErr}
			}()

			<-creatorGate.reached

			inflight := readIncarnation(t, base, reservationsKey("c"), "c")
			assertNoError(t, admin.DeletePool(ctx, "c"))
			close(creatorGate.resume)

			result := <-done

			if attempts == 1 {
				assertConflictReason(t, result.err, "pool_create_raced")

				for _, key := range storedKeys(t, base) {
					if strings.Contains(key, "/c.json") {
						t.Fatalf("aborted create left %q", key)
					}
				}

				if ids := carveOutIDs(t, base, "root", "c"); len(ids) != 0 {
					t.Fatalf("aborted create left carve-outs %v", ids)
				}

				return
			}

			assertNoError(t, result.err)

			if result.pool.Parent.AllocationID == reservedAllocationID("c", inflight) {
				t.Fatalf("retried create reused the deleted incarnation %s", inflight)
			}

			if ids := carveOutIDs(t, base, "root", "c"); len(ids) != 1 || ids[0] != result.pool.Parent.AllocationID {
				t.Fatalf("carve-outs = %v, want only %s", ids, result.pool.Parent.AllocationID)
			}

			if _, err := admin.Allocate(ctx, "c", AllocationSpec{PrefixLength: 26}); err != nil {
				t.Fatalf("Allocate(c) after retried create = %v", err)
			}

			assertLifecycleInvariants(t, base)
		})
	}
}

// A creator that loses the reservations create must release exactly the
// carve-out it created itself, never the in-flight winner's.
func TestChildCreateLoserReleasesOnlyItsOwnCarveOut(t *testing.T) {
	ctx := t.Context()
	base := memory.New()
	admin := New(base, WithSleeper(noSleep))
	mustCreatePool(t, admin, "root", "10.0.0.0/16")

	loserStore := newGateStore(base)
	loserGate := loserStore.arm("create", reservationsKey("c"))
	loserDone := make(chan error, 1)

	go func() {
		_, createErr := New(loserStore, WithSleeper(noSleep), WithMaxAttempts(3)).CreatePool(ctx, childPoolSpec("c", 24))
		loserDone <- createErr
	}()

	<-loserGate.reached

	winnerStore := newGateStore(base)
	winnerGate := winnerStore.arm("create", poolKey("c"))

	type createResult struct {
		pool Pool
		err  error
	}

	winnerDone := make(chan createResult, 1)

	go func() {
		pool, createErr := New(winnerStore, WithSleeper(noSleep)).CreatePool(ctx, childPoolSpec("c", 24))
		winnerDone <- createResult{pool, createErr}
	}()

	<-winnerGate.reached
	close(loserGate.resume)

	loserErr := <-loserDone

	close(winnerGate.resume)

	winner := <-winnerDone
	assertNoError(t, winner.err)

	if !conflictReason(loserErr, "pool_incomplete") && loserErr != nil {
		t.Fatalf("loser CreatePool() = %v, want pool_incomplete or success", loserErr)
	}

	ids := carveOutIDs(t, base, "root", "c")
	if len(ids) != 1 || ids[0] != winner.pool.Parent.AllocationID {
		t.Fatalf("carve-outs = %v, want only the winner's %s", ids, winner.pool.Parent.AllocationID)
	}

	assertLifecycleInvariants(t, base)

	rootAllocation, err := admin.Allocate(ctx, "root", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)

	if rootAllocation.CIDR.Overlaps(winner.pool.CIDRs[0]) {
		t.Fatalf("OVERLAP: child %s root %s", winner.pool.CIDRs[0], rootAllocation.CIDR)
	}
}

// deadAfterStore simulates a process that dies at its Nth store call: that
// call fails (optionally after committing a write) and every later call fails.
type deadAfterStore struct {
	storage.Storage
	mu    sync.Mutex
	n     int
	calls int
	after bool
}

func (store *deadAfterStore) step() (dead, crashNow bool) {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.calls++

	return store.calls > store.n, store.calls == store.n
}

func (store *deadAfterStore) Get(ctx context.Context, key string) ([]byte, storage.Version, error) {
	if dead, crash := store.step(); dead || crash {
		return nil, "", errInjectedCrash
	}

	return store.Storage.Get(ctx, key)
}

func (store *deadAfterStore) List(ctx context.Context, prefix string) ([]string, error) {
	if dead, crash := store.step(); dead || crash {
		return nil, errInjectedCrash
	}

	return store.Storage.List(ctx, prefix)
}

func (store *deadAfterStore) CreateIfAbsent(ctx context.Context, key string, data []byte) (storage.Version, error) {
	dead, crash := store.step()
	if crash && store.after {
		_, _ = store.Storage.CreateIfAbsent(ctx, key, data)
	}

	if dead || crash {
		return "", errInjectedCrash
	}

	return store.Storage.CreateIfAbsent(ctx, key, data)
}

func (store *deadAfterStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	dead, crash := store.step()
	if crash && store.after {
		_, _ = store.Storage.PutIfMatch(ctx, key, data, version)
	}

	if dead || crash {
		return "", errInjectedCrash
	}

	return store.Storage.PutIfMatch(ctx, key, data, version)
}

func (store *deadAfterStore) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	dead, crash := store.step()
	if crash && store.after {
		_ = store.Storage.DeleteIfMatch(ctx, key, version)
	}

	if dead || crash {
		return errInjectedCrash
	}

	return store.Storage.DeleteIfMatch(ctx, key, version)
}

// A process dying at every store call of create/delete/allocate leaves only
// states a healthy client can recover: CreatePool either succeeds or reports
// pool_incomplete without writing, after which DeletePool + CreatePool succeed.
func TestCrashAtEveryStoreCallThenHealthyRecovery(t *testing.T) {
	incompleteSeen := 0

	for _, operation := range []string{"create", "delete", "allocate"} {
		for _, after := range []bool{false, true} {
			for call := 1; call <= 16; call++ {
				name := fmt.Sprintf("%s/after=%v/call=%d", operation, after, call)
				base := memory.New()
				healthy := New(base, WithSleeper(noSleep), WithMaxAttempts(3))
				mustCreatePool(t, healthy, "root", "10.0.0.0/16")

				if operation != "create" {
					_, err := healthy.CreatePool(t.Context(), childPoolSpec("c", 24))
					assertNoError(t, err)
				}

				dying := New(&deadAfterStore{Storage: base, n: call, after: after}, WithSleeper(noSleep))

				switch operation {
				case "create":
					_, _ = dying.CreatePool(t.Context(), childPoolSpec("c", 24))
				case "delete":
					_ = dying.DeletePool(t.Context(), "c")
				case "allocate":
					_, _ = dying.Allocate(t.Context(), "c", AllocationSpec{ID: testAllocationID, PrefixLength: 26})
				}

				assertLifecycleInvariants(t, base)

				recorder := &recordingStore{Storage: base}

				_, err := New(recorder, WithSleeper(noSleep), WithMaxAttempts(3)).CreatePool(t.Context(), childPoolSpec("c", 24))
				switch {
				case err == nil:
				case conflictReason(err, "pool_incomplete"):
					incompleteSeen++

					if writes := recorder.writeKeys(); len(writes) != 0 {
						t.Fatalf("%s: pool_incomplete CreatePool wrote %v", name, writes)
					}

					if deleteErr := healthy.DeletePool(t.Context(), "c"); deleteErr != nil {
						t.Fatalf("%s: DeletePool on residue = %v", name, deleteErr)
					}

					if _, err := healthy.CreatePool(t.Context(), childPoolSpec("c", 24)); err != nil {
						t.Fatalf("%s: CreatePool after DeletePool = %v", name, err)
					}
				default:
					t.Fatalf("%s: healthy CreatePool = %v", name, err)
				}

				child, err := healthy.GetPool(t.Context(), "c")
				assertNoError(t, err)

				carve, err := healthy.GetAllocation(t.Context(), "root", child.Parent.AllocationID)
				assertNoError(t, err)

				if carve.CIDR != child.CIDRs[0] {
					t.Fatalf("%s: carve %s != child %s", name, carve.CIDR, child.CIDRs[0])
				}

				if _, err := healthy.Allocate(t.Context(), "c", AllocationSpec{PrefixLength: 28}); err != nil {
					t.Fatalf("%s: Allocate(c) after recovery = %v", name, err)
				}

				assertLifecycleInvariants(t, base)
			}
		}
	}

	if incompleteSeen == 0 {
		t.Fatal("no crash point produced a pool_incomplete residue; test does not exercise recovery")
	}
}
