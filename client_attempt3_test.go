// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

func TestIncarnationKeyedCarveOutProtectsRecreatedChild(t *testing.T) {
	base := memory.New()
	client := New(base, WithSleeper(noSleep))
	mustCreatePool(t, client, "root", "10.0.0.0/16")

	spec := PoolSpec{
		ID:         "child",
		ParentPool: "root",
		Request:    Request{Kind: RequestNext, PrefixLength: 24},
	}
	first, err := client.CreatePool(t.Context(), spec)
	assertNoError(t, err)

	firstID := first.Parent.AllocationID
	if !strings.HasPrefix(firstID, "pool:child:") {
		t.Fatalf("first carve-out ID = %q, want incarnation-keyed ID", firstID)
	}

	assertNoError(t, client.DeletePool(t.Context(), "child"))
	second, err := client.CreatePool(t.Context(), spec)
	assertNoError(t, err)

	secondID := second.Parent.AllocationID
	if firstID == secondID {
		t.Fatalf("recreated child reused carve-out ID %q", secondID)
	}

	// This is the final action of a stale DeletePool that retained incarnation A.
	// It may only touch A's carve-out and must not release B's.
	assertNoError(t, client.Release(t.Context(), "root", firstID))

	if err := client.Release(t.Context(), "root", secondID); !conflictReason(err, "pool_in_use") {
		t.Fatalf("Release(live carve-out) error = %v, want pool_in_use", err)
	}

	childAllocation, err := client.Allocate(t.Context(), "child", AllocationSpec{PrefixLength: 26})
	assertNoError(t, err)
	rootAllocation, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)

	if childAllocation.CIDR.Overlaps(rootAllocation.CIDR) {
		t.Fatalf("child allocation %s overlaps root allocation %s", childAllocation.CIDR, rootAllocation.CIDR)
	}
}

func TestPoolDocumentsNeverContainLifecycleMarkers(t *testing.T) {
	base := memory.New()
	client := New(base, WithSleeper(noSleep))
	mustCreatePool(t, client, "root", "10.0.0.0/16")

	crash := &preWriteFailureStore{
		Storage: base,
		op:      "put",
		key:     poolKey("child"),
		err:     errors.New("crash before lifecycle activation"),
	}
	_, _ = New(crash, WithSleeper(noSleep)).CreatePool(t.Context(), PoolSpec{
		ID:         "child",
		ParentPool: "root",
		Request:    Request{Kind: RequestNext, PrefixLength: 24},
	})

	for _, key := range []string{poolKey("root"), poolKey("child")} {
		data, _, err := base.Get(t.Context(), key)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}

		assertNoError(t, err)

		if strings.Contains(string(data), `"creating"`) || strings.Contains(string(data), `"deleting"`) {
			t.Fatalf("%s contains lifecycle marker:\n%s", key, data)
		}
	}

	pools, err := client.ListPools(t.Context())
	assertNoError(t, err)

	if len(pools) == 0 {
		t.Fatal("ListPools() returned no unaffected pools")
	}

	if _, err := client.GetPool(t.Context(), "root"); err != nil {
		t.Fatalf("GetPool(root) after crashed child create: %v", err)
	}

	if _, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 28}); err != nil {
		t.Fatalf("Allocate(root) after crashed child create: %v", err)
	}
}

func TestOtherPoolsUnaffectedByCrashedDeleteResidue(t *testing.T) {
	base := memory.New()
	client := New(base, WithSleeper(noSleep))
	mustCreatePool(t, client, "other", "10.1.0.0/16")
	mustCreatePool(t, client, "root", "10.0.0.0/16")
	_, err := client.CreatePool(t.Context(), PoolSpec{
		ID:         "child",
		ParentPool: "root",
		Request:    Request{Kind: RequestNext, PrefixLength: 24},
	})
	assertNoError(t, err)

	crash := &failWriteStore{
		Storage: base,
		op:      "delete-after",
		key:     reservationsKey("child"),
		err:     errors.New("crash after reservations delete"),
	}
	if err := New(crash, WithSleeper(noSleep)).DeletePool(t.Context(), "child"); err == nil {
		t.Fatal("DeletePool(child) error = nil, want injected crash")
	}

	if _, err := client.ListPools(t.Context()); err != nil {
		t.Fatalf("ListPools() after crashed delete: %v", err)
	}

	if _, err := client.GetPool(t.Context(), "other"); err != nil {
		t.Fatalf("GetPool(other) after crashed delete: %v", err)
	}

	if _, err := client.Allocate(t.Context(), "other", AllocationSpec{PrefixLength: 24}); err != nil {
		t.Fatalf("Allocate(other) after crashed delete: %v", err)
	}
}

type preWriteFailureStore struct {
	storage.Storage
	op, key string
	err     error
	once    sync.Once
}

func (store *preWriteFailureStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	fail := false

	if store.op == "put" && key == store.key {
		store.once.Do(func() { fail = true })
	}

	if fail {
		return "", store.err
	}

	return store.Storage.PutIfMatch(ctx, key, data, version)
}

type getErrorStore struct {
	storage.Storage
	key string
	err error
}

func (store *getErrorStore) Get(
	ctx context.Context,
	key string,
) ([]byte, storage.Version, error) {
	if key == store.key {
		return nil, "", store.err
	}

	return store.Storage.Get(ctx, key)
}

func TestCreatePoolParentReadErrorsRemainUnchanged(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "access denied", err: storage.ErrAccessDenied},
		{name: "canceled", err: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := memory.New()
			mustCreatePool(t, New(base, WithSleeper(noSleep)), "root", "10.0.0.0/16")
			client := New(
				&getErrorStore{Storage: base, key: poolKey("root"), err: test.err},
				WithSleeper(noSleep),
			)

			_, err := client.CreatePool(t.Context(), PoolSpec{
				ID:         "child",
				ParentPool: "root",
				Request:    Request{Kind: RequestNext, PrefixLength: 24},
			})
			if !errors.Is(err, test.err) {
				t.Fatalf("CreatePool() error = %v, want %v", err, test.err)
			}

			if conflictReason(err, "parent_carve_missing") {
				t.Fatalf("CreatePool() converted parent read error to conflict: %v", err)
			}
		})
	}
}

func TestCreatePoolNeverAdoptsIncarnationOfIncompletePool(t *testing.T) {
	base := memory.New()
	client := New(base, WithSleeper(noSleep))
	mustCreatePool(t, client, "root", "10.0.0.0/16")

	spec := PoolSpec{
		ID:         "child",
		ParentPool: "root",
		Request:    Request{Kind: RequestNext, PrefixLength: 24},
	}
	original, err := client.CreatePool(t.Context(), spec)
	assertNoError(t, err)

	_, version, err := base.Get(t.Context(), reservationsKey("child"))
	assertNoError(t, err)
	assertNoError(t, base.DeleteIfMatch(t.Context(), reservationsKey("child"), version))

	_, err = client.CreatePool(t.Context(), spec)
	assertConflictReason(t, err, "pool_incomplete")

	if _, _, err := base.Get(t.Context(), reservationsKey("child")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("CreatePool() recreated reservations of incomplete pool: %v", err)
	}

	assertNoError(t, client.DeletePool(t.Context(), "child"))

	recreated, err := client.CreatePool(t.Context(), spec)
	assertNoError(t, err)

	if recreated.Parent.AllocationID == original.Parent.AllocationID {
		t.Fatalf("recreated child reused incarnation-keyed carve-out %s", original.Parent.AllocationID)
	}

	if _, err := client.GetAllocation(t.Context(), "root", original.Parent.AllocationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old carve-out still held after DeletePool: %v", err)
	}

	carve, err := client.GetAllocation(t.Context(), "root", recreated.Parent.AllocationID)
	assertNoError(t, err)

	if carve.CIDR != recreated.CIDRs[0] {
		t.Fatalf("carve CIDR = %s, child CIDR = %s", carve.CIDR, recreated.CIDRs[0])
	}
}

type nthWriteCrashStore struct {
	storage.Storage
	n     int64
	after bool
	count atomic.Int64
}

var errInjectedCrash = errors.New("injected crash")

func (store *nthWriteCrashStore) hit() bool {
	return store.count.Add(1) == store.n
}

func (store *nthWriteCrashStore) CreateIfAbsent(
	ctx context.Context,
	key string,
	data []byte,
) (storage.Version, error) {
	if store.hit() {
		if store.after {
			_, _ = store.Storage.CreateIfAbsent(ctx, key, data)
		}

		return "", errInjectedCrash
	}

	return store.Storage.CreateIfAbsent(ctx, key, data)
}

func (store *nthWriteCrashStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	if store.hit() {
		if store.after {
			_, _ = store.Storage.PutIfMatch(ctx, key, data, version)
		}

		return "", errInjectedCrash
	}

	return store.Storage.PutIfMatch(ctx, key, data, version)
}

func (store *nthWriteCrashStore) DeleteIfMatch(
	ctx context.Context,
	key string,
	version storage.Version,
) error {
	if store.hit() {
		if store.after {
			_ = store.Storage.DeleteIfMatch(ctx, key, version)
		}

		return errInjectedCrash
	}

	return store.Storage.DeleteIfMatch(ctx, key, version)
}

func TestCreateAndDeleteChildCrashAtEveryWriteConverges(t *testing.T) {
	spec := PoolSpec{
		ID:         "child",
		ParentPool: "root",
		Request:    Request{Kind: RequestNext, PrefixLength: 24},
	}

	for _, operation := range []string{"create", "delete"} {
		for _, after := range []bool{false, true} {
			for write := int64(1); write <= 12; write++ {
				name := fmt.Sprintf("%s/after=%v/write=%d", operation, after, write)
				t.Run(name, func(t *testing.T) {
					base := memory.New()
					healthy := New(base, WithSleeper(noSleep))
					mustCreatePool(t, healthy, "root", "10.0.0.0/16")

					if operation == "delete" {
						_, err := healthy.CreatePool(t.Context(), spec)
						assertNoError(t, err)
					}

					crashing := New(
						&nthWriteCrashStore{Storage: base, n: write, after: after},
						WithSleeper(noSleep),
					)
					if operation == "create" {
						_, _ = crashing.CreatePool(t.Context(), spec)

						_, err := healthy.CreatePool(t.Context(), spec)
						if conflictReason(err, "pool_incomplete") {
							assertNoError(t, healthy.DeletePool(t.Context(), "child"))
							_, err = healthy.CreatePool(t.Context(), spec)
						}

						assertNoError(t, err)
						child, err := healthy.GetPool(t.Context(), "child")
						assertNoError(t, err)
						carve, err := healthy.GetAllocation(
							t.Context(),
							"root",
							child.Parent.AllocationID,
						)
						assertNoError(t, err)

						if carve.CIDR != child.CIDRs[0] {
							t.Fatalf("carve CIDR = %s, child CIDR = %s", carve.CIDR, child.CIDRs[0])
						}

						return
					}

					_ = crashing.DeletePool(t.Context(), "child")

					err := healthy.DeletePool(t.Context(), "child")
					if err != nil && !errors.Is(err, ErrNotFound) {
						t.Fatalf("DeletePool() retry error = %v", err)
					}

					if _, err := healthy.GetPool(t.Context(), "child"); !errors.Is(err, ErrNotFound) {
						t.Fatalf("child remains after delete retry: %v", err)
					}
				})
			}
		}
	}
}

type callCountingStore struct {
	storage.Storage
	mu     sync.Mutex
	counts map[string]int
}

func (store *callCountingStore) count(operation string) {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.counts[operation]++
}

func (store *callCountingStore) reset() {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.counts = make(map[string]int)
}

func (store *callCountingStore) Get(
	ctx context.Context,
	key string,
) ([]byte, storage.Version, error) {
	store.count("get")

	return store.Storage.Get(ctx, key)
}

func (store *callCountingStore) CreateIfAbsent(
	ctx context.Context,
	key string,
	data []byte,
) (storage.Version, error) {
	store.count("create")

	return store.Storage.CreateIfAbsent(ctx, key, data)
}

func (store *callCountingStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	store.count("put")

	return store.Storage.PutIfMatch(ctx, key, data, version)
}

func (store *callCountingStore) DeleteIfMatch(
	ctx context.Context,
	key string,
	version storage.Version,
) error {
	store.count("delete")

	return store.Storage.DeleteIfMatch(ctx, key, version)
}

func (store *callCountingStore) List(
	ctx context.Context,
	prefix string,
) ([]string, error) {
	store.count("list")

	return store.Storage.List(ctx, prefix)
}

// seedFullPool writes a pool document and a reservations document with equal
// incarnations directly to the store: one create per document, and a reservations
// document holding count disjoint /28 allocations inside 10.0.0.0/8.
func seedFullPool(t *testing.T, store storage.Storage, poolID string, count int) {
	t.Helper()

	incarnation := strings.Repeat("ab", 16)
	root := netip.MustParsePrefix("10.0.0.0/8")

	poolData, err := encodePoolDocument(poolDocument{
		Pool:        Pool{ID: poolID, Family: 4, CIDRs: []netip.Prefix{root}},
		Incarnation: incarnation,
	})
	assertNoError(t, err)

	allocations := make(map[string]Allocation, count)
	addr := root.Addr()

	for range count {
		id := NewAllocationID()
		allocations[id] = Allocation{
			ID:      id,
			PoolID:  poolID,
			Request: Request{Kind: RequestNext, PrefixLength: 28},
			CIDR:    netip.PrefixFrom(addr, 28),
		}

		for range 16 {
			addr = addr.Next()
		}
	}

	reservationsData, err := encodeReservationsDocument(reservationsDocument{
		PoolID:      poolID,
		Incarnation: incarnation,
		Allocations: allocations,
	})
	assertNoError(t, err)

	_, err = store.CreateIfAbsent(t.Context(), poolKey(poolID), poolData)
	assertNoError(t, err)
	_, err = store.CreateIfAbsent(t.Context(), reservationsKey(poolID), reservationsData)
	assertNoError(t, err)
}

func TestAllocateLatencyAndCallCountAtFiveThousand(t *testing.T) {
	counting := &callCountingStore{
		Storage: memory.New(),
		counts:  make(map[string]int),
	}
	seedFullPool(t, counting, "root", 5000)

	client := New(counting, WithSleeper(noSleep))
	counting.reset()

	start := time.Now()
	allocation, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
	elapsed := time.Since(start)

	assertNoError(t, err)

	if want := netip.MustParsePrefix("10.1.57.0/24"); allocation.CIDR != want {
		t.Fatalf("Allocate() CIDR = %s, want %s", allocation.CIDR, want)
	}

	if counting.counts["get"] != 2 || counting.counts["put"] != 1 ||
		counting.counts["create"] != 0 || counting.counts["delete"] != 0 ||
		counting.counts["list"] != 0 {
		t.Fatalf("Allocate() calls = %v, want 2 gets and 1 put", counting.counts)
	}

	if elapsed > 10*time.Second {
		t.Fatalf("Allocate at 5000 allocations took %s, want under 10s", elapsed)
	}

	t.Logf("Allocate at 5000 allocations: %s calls=%v", elapsed, counting.counts)
}

func TestAllocateRampThreeHundredThroughCAS(t *testing.T) {
	client := New(memory.New(), WithSleeper(noSleep))
	mustCreatePool(t, client, "root", "10.0.0.0/8")

	for range 300 {
		_, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 28})
		assertNoError(t, err)
	}

	allocations, err := client.ListAllocations(t.Context(), "root")
	assertNoError(t, err)

	if len(allocations) != 300 {
		t.Fatalf("ListAllocations() = %d allocations, want 300", len(allocations))
	}
}
