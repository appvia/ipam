// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/local"
	"github.com/appvia/ipam/storage/memory"
)

func TestChildPoolLifecycleAndParentDeleteGuard(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())
	mustCreatePool(t, client, "parent", "10.0.0.0/16")

	child, err := client.CreatePool(t.Context(), PoolSpec{
		ID:          "child",
		ParentPool:  "parent",
		Request:     Request{Kind: RequestNext, PrefixLength: 20},
		Description: "child pool",
	})
	assertNoError(t, err)

	if child.Parent == nil ||
		!strings.HasPrefix(child.Parent.AllocationID, "pool:child:") ||
		child.CIDRs[0] != netip.MustParsePrefix("10.0.0.0/20") {
		t.Fatalf("child = %#v", child)
	}

	err = client.DeletePool(t.Context(), "parent")
	assertConflictReason(t, err, "pool_not_empty")

	assertNoError(t, client.DeletePool(t.Context(), "child"))
	parentAllocations, err := client.ListAllocations(t.Context(), "parent")
	assertNoError(t, err)

	if len(parentAllocations) != 0 {
		t.Fatalf("parent allocations after child delete = %#v", parentAllocations)
	}

	assertNoError(t, client.DeletePool(t.Context(), "parent"))
}

func TestChildCreateCrashThenRetryConverges(t *testing.T) {
	t.Parallel()

	base := memory.New()
	parentClient := newTestClient(base)
	mustCreatePool(t, parentClient, "parent", "10.0.0.0/16")

	failing := &failWriteStore{
		Storage: base,
		op:      "create",
		key:     reservationsKey("child"),
		err:     errors.New("simulated crash"),
	}

	_, err := newTestClient(failing).CreatePool(t.Context(), PoolSpec{
		ID:         "child",
		ParentPool: "parent",
		Request:    Request{Kind: RequestNext, PrefixLength: 20},
	})
	if err == nil {
		t.Fatal("CreatePool() error = nil")
	}

	child, err := parentClient.CreatePool(t.Context(), PoolSpec{
		ID:         "child",
		ParentPool: "parent",
		Request:    Request{Kind: RequestNext, PrefixLength: 20},
	})
	assertNoError(t, err)

	allocations, err := parentClient.ListAllocations(t.Context(), "parent")
	assertNoError(t, err)

	foundLiveCarve := false

	for _, allocation := range allocations {
		if allocation.ID == child.Parent.AllocationID {
			foundLiveCarve = true

			if allocation.CIDR != child.CIDRs[0] {
				t.Fatalf("live carve-out CIDR = %s, want %s", allocation.CIDR, child.CIDRs[0])
			}
		}
	}

	if !foundLiveCarve {
		t.Fatalf("parent allocations = %#v, missing live carve-out %q", allocations, child.Parent.AllocationID)
	}
}

func TestDeletePoolCrashPointsConverge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		op               string
		key              string
		allowSafeAbandon bool
	}{
		{"after step 3 reservations delete", "delete-after", reservationsKey("child"), false},
		{"after step 4 pool delete", "delete-after", poolKey("child"), true},
		{"after step 5 parent release", "put-after", reservationsKey("parent"), false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := memory.New()
			client := newTestClient(base)
			mustCreatePool(t, client, "parent", "10.0.0.0/16")
			_, err := client.CreatePool(t.Context(), PoolSpec{
				ID:         "child",
				ParentPool: "parent",
				Request:    Request{Kind: RequestNext, PrefixLength: 20},
			})
			assertNoError(t, err)

			failing := &failWriteStore{
				Storage: base,
				op:      test.op,
				key:     test.key,
				err:     errors.New("simulated crash after commit"),
			}
			if err := newTestClient(failing).DeletePool(t.Context(), "child"); err == nil {
				t.Fatal("DeletePool() error = nil")
			}

			err = client.DeletePool(t.Context(), "child")
			if err != nil && !errors.Is(err, ErrNotFound) {
				t.Fatalf("DeletePool() retry error = %v", err)
			}

			if _, err := client.GetPool(t.Context(), "child"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("child remains: %v", err)
			}

			allocations, err := client.ListAllocations(t.Context(), "parent")
			assertNoError(t, err)

			for _, allocation := range allocations {
				if strings.HasPrefix(allocation.ID, "pool:child:") && !test.allowSafeAbandon {
					t.Fatalf("orphaned child carve-out = %#v", allocation)
				}
			}
		})
	}
}

func TestAllocateRacingDeleteNeverOrphans(t *testing.T) {
	t.Parallel()

	for iteration := range 30 {
		base := memory.New()
		createClient := newTestClient(base)
		mustCreatePool(t, createClient, "prod", "10.0.0.0/16")

		entered := make(chan struct{})
		release := make(chan struct{})

		var once sync.Once

		base.BeforeWrite = func(op, key string) {
			if op == "put" && key == reservationsKey("prod") {
				once.Do(func() {
					close(entered)
					<-release
				})
			}
		}

		allocateClient := newTestClient(base)
		deleteClient := newTestClient(base)
		allocationDone := make(chan error, 1)

		go func() {
			_, err := allocateClient.Allocate(t.Context(), "prod", AllocationSpec{
				ID:           testAllocationID,
				PrefixLength: 24,
			})
			allocationDone <- err
		}()

		<-entered

		deleteErr := deleteClient.DeletePool(t.Context(), "prod")

		close(release)

		allocateErr := <-allocationDone

		switch {
		case deleteErr == nil:
			if !errors.Is(allocateErr, ErrNotFound) {
				t.Fatalf("iteration %d: delete succeeded, allocate error = %v", iteration, allocateErr)
			}
		case errors.Is(deleteErr, ErrConflict):
			if allocateErr != nil {
				t.Fatalf("iteration %d: delete conflict, allocate error = %v", iteration, allocateErr)
			}
		default:
			t.Fatalf("iteration %d: delete error = %v", iteration, deleteErr)
		}

		keys, err := base.List(t.Context(), "")
		assertNoError(t, err)

		if slicesContain(keys, reservationsKey("prod")) != slicesContain(keys, poolKey("prod")) {
			t.Fatalf("iteration %d: orphaned documents: %v", iteration, keys)
		}
	}
}

func TestReleaseUnknownOutcomeObservesCommittedDelete(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)
	mustCreatePool(t, client, "prod", "10.0.0.0/16")
	_, err := client.Allocate(t.Context(), "prod", AllocationSpec{
		ID: testAllocationID, PrefixLength: 24,
	})
	assertNoError(t, err)

	store := &unknownAfterCommitStore{Storage: base}
	assertNoError(t, newTestClient(store).Release(t.Context(), "prod", testAllocationID))
	allocations, err := client.ListAllocations(t.Context(), "prod")
	assertNoError(t, err)

	if len(allocations) != 0 {
		t.Fatalf("allocations = %#v", allocations)
	}
}

func TestPoolIncarnationPreventsABAOnMemoryAndLocal(t *testing.T) {
	t.Parallel()

	backends := map[string]func(t *testing.T) storage.Storage{
		"memory": func(*testing.T) storage.Storage { return memory.New() },
		"local": func(t *testing.T) storage.Storage {
			store, err := local.New(filepath.Join(t.TempDir(), "store"))
			assertNoError(t, err)

			return store
		},
	}

	for name, newStore := range backends {
		t.Run(name, func(t *testing.T) {
			store := newStore(t)

			allocation := runABARace(t, store, fixedReader(0x11), fixedReader(0x22))
			if !insidePool([]netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}, allocation.CIDR) {
				t.Fatalf("Allocate() committed stale CIDR %s", allocation.CIDR)
			}
		})
	}
}

func TestPoolIncarnationABANegativeControl(t *testing.T) {
	t.Parallel()

	store, err := local.New(filepath.Join(t.TempDir(), "store"))
	assertNoError(t, err)

	allocation := runABARace(t, store, fixedReader(0x11), fixedReader(0x11))

	if insidePool([]netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}, allocation.CIDR) {
		t.Fatalf("negative control CIDR = %s; expected stale corruption when token is neutralised", allocation.CIDR)
	}

	if allocation.CIDR != netip.MustParsePrefix("10.0.0.0/24") {
		t.Fatalf("negative control CIDR = %s, want stale 10.0.0.0/24", allocation.CIDR)
	}
}

func runABARace(t *testing.T, store storage.Storage, firstRand, secondRand fixedReader) Allocation {
	t.Helper()

	first := New(store, WithRand(firstRand), WithClock(func() time.Time {
		return time.Unix(1, 0).UTC()
	}), WithSleeper(noSleep))
	mustCreatePool(t, first, "prod", "10.0.0.0/16")

	blocked := &blockAfterPoolReadStore{
		Storage: store,
		read:    make(chan struct{}),
		resume:  make(chan struct{}),
	}
	allocateClient := New(blocked, WithRand(firstRand), WithClock(func() time.Time {
		return time.Unix(1, 0).UTC()
	}), WithSleeper(noSleep))
	result := make(chan allocationResult, 1)

	go func() {
		allocation, err := allocateClient.Allocate(t.Context(), "prod", AllocationSpec{
			ID:           testAllocationID,
			PrefixLength: 24,
		})
		result <- allocationResult{allocation: allocation, err: err}
	}()

	<-blocked.read

	assertNoError(t, first.DeletePool(t.Context(), "prod"))

	second := New(store, WithRand(secondRand), WithClock(func() time.Time {
		return time.Unix(2, 0).UTC()
	}), WithSleeper(noSleep))
	mustCreatePool(t, second, "prod", "10.1.0.0/16")
	close(blocked.resume)

	got := <-result
	assertNoError(t, got.err)

	return got.allocation
}

type failWriteStore struct {
	storage.Storage
	op   string
	key  string
	err  error
	once atomic.Bool
}

func (s *failWriteStore) CreateIfAbsent(
	ctx context.Context,
	key string,
	data []byte,
) (storage.Version, error) {
	if s.op == "create" && key == s.key && s.once.CompareAndSwap(false, true) {
		return "", s.err
	}

	return s.Storage.CreateIfAbsent(ctx, key, data)
}

func (s *failWriteStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	next, err := s.Storage.PutIfMatch(ctx, key, data, version)
	if err == nil && s.op == "put-after" && key == s.key && s.once.CompareAndSwap(false, true) {
		return "", s.err
	}

	return next, err
}

func (s *failWriteStore) DeleteIfMatch(
	ctx context.Context,
	key string,
	version storage.Version,
) error {
	err := s.Storage.DeleteIfMatch(ctx, key, version)
	if err == nil && s.op == "delete-after" && key == s.key && s.once.CompareAndSwap(false, true) {
		return s.err
	}

	return err
}

type blockAfterPoolReadStore struct {
	storage.Storage
	read   chan struct{}
	resume chan struct{}
	once   sync.Once
}

func (s *blockAfterPoolReadStore) Get(
	ctx context.Context,
	key string,
) ([]byte, storage.Version, error) {
	data, version, err := s.Storage.Get(ctx, key)
	if err == nil && key == poolKey("prod") {
		s.once.Do(func() {
			close(s.read)

			select {
			case <-s.resume:
			case <-ctx.Done():
			}
		})
	}

	return data, version, err
}

func slicesContain(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}

	return false
}
