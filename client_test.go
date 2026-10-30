// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	mathrand "math/rand"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

const (
	testAllocationID  = "01900000-0000-7000-8000-000000000001"
	testAllocationID2 = "01900000-0000-7000-8000-000000000002"
)

var testIncarnationSeed atomic.Uint32

func TestClientPoolAndAllocationAPI(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())
	ctx := t.Context()
	spec := PoolSpec{
		ID:          "prod",
		CIDRs:       []netip.Prefix{netip.MustParsePrefix("10.0.0.5/16")},
		Description: "production",
		Labels:      map[string]string{"env": "prod"},
	}

	created, err := client.CreatePool(ctx, spec)
	assertNoError(t, err)

	if created.CIDRs[0] != netip.MustParsePrefix("10.0.0.0/16") {
		t.Fatalf("CreatePool() CIDRs = %v", created.CIDRs)
	}

	got, err := client.GetPool(ctx, "prod")
	assertNoError(t, err)

	if got.ID != created.ID || got.Description != "production" {
		t.Fatalf("GetPool() = %#v", got)
	}

	pools, err := client.ListPools(ctx)
	assertNoError(t, err)

	if len(pools) != 1 || pools[0].ID != "prod" {
		t.Fatalf("ListPools() = %#v", pools)
	}

	allocation, err := client.Allocate(ctx, "prod", AllocationSpec{
		ID:           testAllocationID,
		PrefixLength: 24,
		Description:  "network",
		Labels:       map[string]string{"team": "platform"},
	})
	assertNoError(t, err)

	gotAllocation, err := client.GetAllocation(ctx, "prod", testAllocationID)
	assertNoError(t, err)

	if !allocationsEqual(gotAllocation, allocation) {
		t.Fatalf("GetAllocation() = %#v, want %#v", gotAllocation, allocation)
	}

	allocations, err := client.ListAllocations(ctx, "prod")
	assertNoError(t, err)

	if len(allocations) != 1 || !allocationsEqual(allocations[0], allocation) {
		t.Fatalf("ListAllocations() = %#v", allocations)
	}

	assertNoError(t, client.UpdatePoolMetadata(ctx, "prod", "updated", map[string]string{"x": "y"}))
	updatedPool, err := client.GetPool(ctx, "prod")
	assertNoError(t, err)

	if updatedPool.Description != "updated" || updatedPool.Labels["x"] != "y" {
		t.Fatalf("updated pool = %#v", updatedPool)
	}

	assertNoError(t, client.UpdateAllocationMetadata(ctx, "prod", testAllocationID, "updated", map[string]string{"a": "b"}))
	updatedAllocation, err := client.GetAllocation(ctx, "prod", testAllocationID)
	assertNoError(t, err)

	if updatedAllocation.Description != "updated" || updatedAllocation.Labels["a"] != "b" {
		t.Fatalf("updated allocation = %#v", updatedAllocation)
	}

	if updatedAllocation.CIDR != allocation.CIDR || updatedAllocation.ID != allocation.ID ||
		updatedAllocation.Request != allocation.Request || !updatedAllocation.CreatedAt.Equal(allocation.CreatedAt) {
		t.Fatalf("metadata update changed immutable fields: before %#v after %#v", allocation, updatedAllocation)
	}

	assertNoError(t, client.Release(ctx, "prod", testAllocationID))
	assertNoError(t, client.Release(ctx, "prod", testAllocationID))

	if _, err := client.GetAllocation(ctx, "prod", testAllocationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAllocation() after release error = %v", err)
	}

	assertNoError(t, client.DeletePool(ctx, "prod"))

	if _, err := client.GetPool(ctx, "prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPool() after delete error = %v", err)
	}
}

func TestAllocateConcurrentGeneratedIDs(t *testing.T) {
	t.Parallel()

	store := memory.New()

	var next atomic.Int64

	client := New(
		store,
		WithClock(func() time.Time { return time.Unix(1, 0).UTC() }),
		WithIDGenerator(func() string {
			n := next.Add(1)
			return fmt.Sprintf("01900000-0000-7000-8000-%012d", n)
		}),
		WithSleeper(noSleep),
	)
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	results := runConcurrentAllocations(t, 8, func() (Allocation, error) {
		return client.Allocate(t.Context(), "prod", AllocationSpec{PrefixLength: 24})
	})

	cidrs := make([]netip.Prefix, 0, len(results))

	ids := make(map[string]struct{}, len(results))
	for _, result := range results {
		assertNoError(t, result.err)
		cidrs = append(cidrs, result.allocation.CIDR)
		ids[result.allocation.ID] = struct{}{}
	}

	slices.SortFunc(cidrs, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })

	for index, cidr := range cidrs {
		want := netip.MustParsePrefix(fmt.Sprintf("10.0.%d.0/24", index))
		if cidr != want {
			t.Fatalf("CIDR %d = %s, want %s", index, cidr, want)
		}
	}

	if len(ids) != 8 {
		t.Fatalf("unique generated IDs = %d, want 8", len(ids))
	}
}

func TestAllocateSameIDConvergesAndReuseConflicts(t *testing.T) {
	t.Parallel()

	store := memory.New()
	clients := []*Client{
		newTestClient(store),
		newTestClient(store),
	}
	mustCreatePool(t, clients[0], "prod", "10.0.0.0/16")

	results := runConcurrentAllocationsIndex(t, 8, func(index int) (Allocation, error) {
		return clients[index%len(clients)].Allocate(t.Context(), "prod", AllocationSpec{
			ID:           testAllocationID,
			PrefixLength: 24,
		})
	})
	for _, result := range results {
		assertNoError(t, result.err)

		if result.allocation.CIDR != netip.MustParsePrefix("10.0.0.0/24") {
			t.Fatalf("Allocate() CIDR = %s", result.allocation.CIDR)
		}
	}

	allocations, err := clients[0].ListAllocations(t.Context(), "prod")
	assertNoError(t, err)

	if len(allocations) != 1 {
		t.Fatalf("ListAllocations() count = %d, want 1", len(allocations))
	}

	_, err = clients[0].Allocate(t.Context(), "prod", AllocationSpec{
		ID:           testAllocationID,
		PrefixLength: 25,
	})
	assertConflictReason(t, err, "id_reused")
}

func TestAllocateExplicitCanonicalizesBeforeIdempotency(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	first, err := client.Allocate(t.Context(), "prod", AllocationSpec{
		ID:   testAllocationID,
		CIDR: netip.MustParsePrefix("10.0.1.7/24"),
	})
	assertNoError(t, err)
	second, err := client.Allocate(t.Context(), "prod", AllocationSpec{
		ID:   testAllocationID,
		CIDR: netip.MustParsePrefix("10.0.1.0/24"),
	})
	assertNoError(t, err)

	if first.CIDR != netip.MustParsePrefix("10.0.1.0/24") || second.CIDR != first.CIDR {
		t.Fatalf("canonical explicit allocations = %s and %s", first.CIDR, second.CIDR)
	}
}

func TestCreatePoolConcurrentSameAndDifferentSpecs(t *testing.T) {
	t.Parallel()

	newRacingClients := func(store storage.Storage) ([]*Client, []*committedWriteStore) {
		clients := make([]*Client, 8)
		recorders := make([]*committedWriteStore, 8)

		for index := range clients {
			recorders[index] = &committedWriteStore{Storage: store}
			clients[index] = New(
				recorders[index],
				WithRand(mathrand.New(mathrand.NewSource(int64(index)+1))),
				WithSleeper(yieldSleeper),
				WithMaxAttempts(0),
			)
		}

		return clients, recorders
	}

	t.Run("same spec converges", func(t *testing.T) {
		store := memory.New()
		clients, _ := newRacingClients(store)

		results := runConcurrentPools(t, clients, func(index int) PoolSpec {
			return PoolSpec{ID: "prod", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")}}
		})

		var first Pool

		for index, result := range results {
			assertNoError(t, result.err)

			if index == 0 {
				first = result.pool
			}

			if !poolsEqual(result.pool, first) || result.pool.CIDRs[0] != netip.MustParsePrefix("10.0.0.0/16") {
				t.Fatalf("CreatePool() = %#v, want %#v", result.pool, first)
			}
		}

		if _, err := newTestClient(store).Allocate(t.Context(), "prod", AllocationSpec{PrefixLength: 24}); err != nil {
			t.Fatalf("Allocate() after same-spec race = %v", err)
		}
	})

	t.Run("different specs have one winner", func(t *testing.T) {
		store := memory.New()
		clients, recorders := newRacingClients(store)

		results := runConcurrentPools(t, clients, func(index int) PoolSpec {
			return PoolSpec{
				ID:    "prod",
				CIDRs: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("10.%d.0.0/16", index))},
			}
		})
		successes := 0

		for index, result := range results {
			switch {
			case result.err == nil:
				successes++
			case conflictReason(result.err, "pool_exists"):
				if writes := recorders[index].committedKeys(); len(writes) != 0 {
					t.Fatalf("losing creator %d committed writes %v", index, writes)
				}
			default:
				t.Fatalf("CreatePool() unexpected error = %v", result.err)
			}
		}

		if successes != 1 {
			t.Fatalf("successes = %d, want 1", successes)
		}

		if _, err := newTestClient(store).Allocate(t.Context(), "prod", AllocationSpec{PrefixLength: 24}); err != nil {
			t.Fatalf("winner pool not allocatable: %v", err)
		}
	})
}

func TestAllocatePermanentPreconditionUsesExactlyMaxAttempts(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := New(base, WithSleeper(noSleep))
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	store := &permanentPreconditionStore{Storage: base}

	_, err := New(store, WithMaxAttempts(8), WithSleeper(noSleep)).Allocate(
		t.Context(),
		"prod",
		AllocationSpec{ID: testAllocationID, PrefixLength: 24},
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Allocate() error = %v, want ErrConflict", err)
	}

	assertConflictReason(t, err, "contention")

	if got := store.puts.Load(); got != 8 {
		t.Fatalf("PutIfMatch calls = %d, want 8", got)
	}
}

func TestAllocateUnknownOutcomeObservesCommittedWrite(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	store := &unknownAfterCommitStore{Storage: base}
	allocation, err := newTestClient(store).Allocate(t.Context(), "prod", AllocationSpec{
		ID:           testAllocationID,
		PrefixLength: 24,
	})
	assertNoError(t, err)

	if allocation.CIDR != netip.MustParsePrefix("10.0.0.0/24") {
		t.Fatalf("Allocate() CIDR = %s", allocation.CIDR)
	}

	if store.puts.Load() != 1 {
		t.Fatalf("PutIfMatch calls = %d, want 1", store.puts.Load())
	}

	allocations, err := client.ListAllocations(t.Context(), "prod")
	assertNoError(t, err)

	if len(allocations) != 1 {
		t.Fatalf("allocation count = %d, want 1", len(allocations))
	}
}

func TestAllocateNeverWritesPoolDocumentAndMissingReservationsDoesNotResurrect(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)
	mustCreatePool(t, client, "prod", "10.0.0.0/16")
	_, reservationsVersion, err := base.Get(t.Context(), reservationsKey("prod"))
	assertNoError(t, err)
	assertNoError(t, base.DeleteIfMatch(t.Context(), reservationsKey("prod"), reservationsVersion))

	recorder := &recordingStore{Storage: base}

	_, err = newTestClient(recorder).Allocate(t.Context(), "prod", AllocationSpec{
		ID:           testAllocationID,
		PrefixLength: 24,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Allocate() error = %v, want ErrNotFound", err)
	}

	if writes := recorder.writeKeys(); len(writes) != 0 {
		t.Fatalf("Allocate() writes = %v, want none", writes)
	}

	// Restore the reservations document and prove normal Allocate writes only it.

	poolData, _, err := base.Get(t.Context(), poolKey("prod"))
	assertNoError(t, err)
	pool, err := decodePoolDocument(poolData, "prod")
	assertNoError(t, err)
	data, err := encodeReservationsDocument(reservationsDocument{
		PoolID: "prod", Incarnation: pool.Incarnation, Allocations: map[string]Allocation{},
	})
	assertNoError(t, err)
	_, err = base.CreateIfAbsent(t.Context(), reservationsKey("prod"), data)
	assertNoError(t, err)

	_, err = newTestClient(recorder).Allocate(t.Context(), "prod", AllocationSpec{
		ID:           testAllocationID,
		PrefixLength: 24,
	})
	assertNoError(t, err)

	for _, key := range recorder.writeKeys() {
		if key != reservationsKey("prod") {
			t.Fatalf("Allocate() wrote %q", key)
		}
	}
}

func TestAllocateRejectsUnsupportedSchemaWithoutWrites(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)
	mustCreatePool(t, client, "prod", "10.0.0.0/16")
	data, version, err := base.Get(t.Context(), reservationsKey("prod"))
	assertNoError(t, err)

	data = bytes.Replace(data, []byte(`"schema_version": 1`), []byte(`"schema_version": 2`), 1)
	_, err = base.PutIfMatch(t.Context(), reservationsKey("prod"), data, version)
	assertNoError(t, err)

	recorder := &recordingStore{Storage: base}

	_, err = newTestClient(recorder).Allocate(t.Context(), "prod", AllocationSpec{
		ID:           testAllocationID,
		PrefixLength: 24,
	})
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Allocate() error = %v, want ErrUnsupportedSchema", err)
	}

	if writes := recorder.writeKeys(); len(writes) != 0 {
		t.Fatalf("writes = %v, want none", writes)
	}
}

func TestAllocateCancellationStopsRetryWithoutPartialState(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	ctx, cancel := context.WithCancel(t.Context())
	store := &permanentPreconditionStore{Storage: base}
	retrying := New(store, WithMaxAttempts(0), WithSleeper(func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	}))

	_, err := retrying.Allocate(ctx, "prod", AllocationSpec{ID: testAllocationID, PrefixLength: 24})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Allocate() error = %v, want context.Canceled", err)
	}

	allocations, listErr := client.ListAllocations(t.Context(), "prod")
	assertNoError(t, listErr)

	if len(allocations) != 0 {
		t.Fatalf("allocations after cancellation = %#v", allocations)
	}
}

func TestClientMalformedInputs(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	tests := []struct {
		name string
		spec AllocationSpec
	}{
		{"invalid id", AllocationSpec{ID: "not-a-uuid", PrefixLength: 24}},
		{"reserved id", AllocationSpec{ID: "pool:user", PrefixLength: 24}},
		{"prefix too short", AllocationSpec{ID: testAllocationID, PrefixLength: 8}},
		{"outside pool", AllocationSpec{ID: testAllocationID, CIDR: netip.MustParsePrefix("192.0.2.0/24")}},
		{"mapped prefix", AllocationSpec{ID: testAllocationID, CIDR: netip.MustParsePrefix("::ffff:10.0.0.0/104")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.Allocate(t.Context(), "prod", test.spec)
			if err == nil {
				t.Fatal("Allocate() error = nil")
			}
		})
	}
}

func newTestClient(store storage.Storage) *Client {
	seed := byte(testIncarnationSeed.Add(1))

	return New(
		store,
		WithClock(func() time.Time { return time.Unix(1, 0).UTC() }),
		WithRand(mathrand.New(mathrand.NewSource(int64(seed)))),
		WithSleeper(noSleep),
	)
}

func noSleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func mustCreatePool(t *testing.T, client *Client, id, cidr string) Pool {
	t.Helper()
	pool, err := client.CreatePool(t.Context(), PoolSpec{
		ID:    id,
		CIDRs: []netip.Prefix{netip.MustParsePrefix(cidr)},
	})
	assertNoError(t, err)

	return pool
}

type allocationResult struct {
	allocation Allocation
	err        error
}

func runConcurrentAllocations(t *testing.T, count int, fn func() (Allocation, error)) []allocationResult {
	t.Helper()
	return runConcurrentAllocationsIndex(t, count, func(int) (Allocation, error) { return fn() })
}

func runConcurrentAllocationsIndex(
	t *testing.T,
	count int,
	fn func(index int) (Allocation, error),
) []allocationResult {
	t.Helper()

	results := make([]allocationResult, count)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for index := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start

			results[index].allocation, results[index].err = fn(index)
		}()
	}

	close(start)
	wg.Wait()

	return results
}

type poolResult struct {
	pool Pool
	err  error
}

func runConcurrentPools(t *testing.T, clients []*Client, spec func(int) PoolSpec) []poolResult {
	t.Helper()

	results := make([]poolResult, len(clients))
	start := make(chan struct{})

	var wg sync.WaitGroup
	for index, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start

			results[index].pool, results[index].err = client.CreatePool(t.Context(), spec(index))
		}()
	}

	close(start)
	wg.Wait()

	return results
}

func assertNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertConflictReason(t *testing.T, err error, reason string) {
	t.Helper()

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}

	var opErr *OpError
	if !errors.As(err, &opErr) || opErr.Reason != reason {
		t.Fatalf("error = %#v, want reason %q", err, reason)
	}
}

func allocationsEqual(left, right Allocation) bool {
	return left.ID == right.ID &&
		left.PoolID == right.PoolID &&
		left.Request == right.Request &&
		left.CIDR == right.CIDR &&
		left.Description == right.Description &&
		maps.Equal(left.Labels, right.Labels) &&
		left.CreatedAt.Equal(right.CreatedAt)
}

type permanentPreconditionStore struct {
	storage.Storage
	puts atomic.Int64
}

func (s *permanentPreconditionStore) PutIfMatch(
	context.Context,
	string,
	[]byte,
	storage.Version,
) (storage.Version, error) {
	s.puts.Add(1)
	return "", storage.ErrPreconditionFailed
}

type unknownAfterCommitStore struct {
	storage.Storage
	once sync.Once
	puts atomic.Int64
}

func (s *unknownAfterCommitStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	s.puts.Add(1)

	next, err := s.Storage.PutIfMatch(ctx, key, data, version)
	if err != nil {
		return next, err
	}

	unknown := false

	s.once.Do(func() { unknown = true })

	if unknown {
		return "", fmt.Errorf("lost response: %w", storage.ErrUnknownOutcome)
	}

	return next, nil
}

type recordingStore struct {
	storage.Storage
	mu     sync.Mutex
	writes []string
}

func (s *recordingStore) CreateIfAbsent(
	ctx context.Context,
	key string,
	data []byte,
) (storage.Version, error) {
	s.record(key)
	return s.Storage.CreateIfAbsent(ctx, key, data)
}

func (s *recordingStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	s.record(key)
	return s.Storage.PutIfMatch(ctx, key, data, version)
}

func (s *recordingStore) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	s.record(key)
	return s.Storage.DeleteIfMatch(ctx, key, version)
}

func (s *recordingStore) record(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.writes = append(s.writes, key)
}

func (s *recordingStore) writeKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.writes...)
}

type fixedReader byte

func (r fixedReader) Read(p []byte) (int, error) {
	for index := range p {
		p[index] = byte(r)
	}

	return len(p), nil
}

var _ io.Reader = fixedReader(0)
