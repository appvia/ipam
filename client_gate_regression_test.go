// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

func waitGateResult(t *testing.T, done <-chan struct{}, operation string) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not return", operation)
	}
}

func TestCreatePoolRejectsSelfParentWithoutDeadlock(t *testing.T) {
	client := New(memory.New(), WithSleeper(noSleep))

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	done := make(chan struct{})

	var err error

	go func() {
		_, err = client.CreatePool(ctx, PoolSpec{
			ID:         "child",
			ParentPool: "child",
			Request:    Request{Kind: RequestNext, PrefixLength: 24},
		})

		close(done)
	}()

	waitGateResult(t, done, "CreatePool(child,parent=child)")

	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreatePool() error = %v, want ErrInvalidArgument", err)
	}
}

func TestCreatePoolMutualParentsNeverDeadlock(t *testing.T) {
	client := New(memory.New(), WithSleeper(noSleep))
	first := make(chan error, 1)
	second := make(chan error, 1)

	go func() {
		_, err := client.CreatePool(t.Context(), PoolSpec{
			ID:         "a",
			ParentPool: "b",
			Request:    Request{Kind: RequestNext, PrefixLength: 24},
		})
		first <- err
	}()

	go func() {
		_, err := client.CreatePool(t.Context(), PoolSpec{
			ID:         "b",
			ParentPool: "a",
			Request:    Request{Kind: RequestNext, PrefixLength: 24},
		})
		second <- err
	}()

	for name, result := range map[string]<-chan error{"first": first, "second": second} {
		select {
		case err := <-result:
			if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrConflict) {
				t.Fatalf("%s CreatePool() error = %v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s mutual-parent CreatePool() did not return", name)
		}
	}
}

func TestCreatePoolAfterCrashedDeleteRequiresDeleteNotAdoption(t *testing.T) {
	base := memory.New()
	client := New(base, WithSleeper(noSleep))
	original := mustCreatePool(t, client, "prod", "10.0.0.0/16")
	poolData, _, err := base.Get(t.Context(), poolKey("prod"))
	assertNoError(t, err)
	storedPool, err := decodePoolDocument(poolData, "prod")
	assertNoError(t, err)

	failing := &failWriteStore{
		Storage: base,
		op:      "delete-after",
		key:     reservationsKey("prod"),
		err:     errors.New("crash after reservations delete"),
	}
	if err := New(failing, WithSleeper(noSleep)).DeletePool(t.Context(), "prod"); err == nil {
		t.Fatal("DeletePool() error = nil, want injected crash")
	}

	_, err = client.CreatePool(t.Context(), PoolSpec{
		ID:    "prod",
		CIDRs: original.CIDRs,
	})
	assertConflictReason(t, err, "pool_incomplete")

	_, err = client.CreatePool(t.Context(), PoolSpec{
		ID:    "prod",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/16")},
	})
	assertConflictReason(t, err, "pool_exists")
	assertNoError(t, client.DeletePool(t.Context(), "prod"))

	recreated, err := client.CreatePool(t.Context(), PoolSpec{
		ID:    "prod",
		CIDRs: original.CIDRs,
	})
	assertNoError(t, err)

	if recreated.CIDRs[0] != original.CIDRs[0] {
		t.Fatalf("CreatePool() CIDR = %s, want %s", recreated.CIDRs[0], original.CIDRs[0])
	}

	poolData, _, err = base.Get(t.Context(), poolKey("prod"))
	assertNoError(t, err)
	recreatedPool, err := decodePoolDocument(poolData, "prod")
	assertNoError(t, err)

	if recreatedPool.Incarnation == storedPool.Incarnation {
		t.Fatalf("recreated pool adopted the crashed pool's incarnation %s", storedPool.Incarnation)
	}

	assertNoError(t, client.DeletePool(t.Context(), "prod"))
}

func TestCreatePoolConflictRollsBackNewParentCarveOut(t *testing.T) {
	client := New(memory.New(), WithSleeper(noSleep))
	mustCreatePool(t, client, "parent-a", "10.0.0.0/16")
	mustCreatePool(t, client, "parent-b", "10.1.0.0/16")

	_, err := client.CreatePool(t.Context(), PoolSpec{
		ID:         "child",
		ParentPool: "parent-a",
		Request:    Request{Kind: RequestNext, PrefixLength: 24},
	})
	assertNoError(t, err)

	_, err = client.CreatePool(t.Context(), PoolSpec{
		ID:         "child",
		ParentPool: "parent-b",
		Request:    Request{Kind: RequestNext, PrefixLength: 24},
	})
	assertConflictReason(t, err, "pool_exists")

	allocations, err := client.ListAllocations(t.Context(), "parent-b")
	assertNoError(t, err)

	for _, allocation := range allocations {
		if strings.HasPrefix(allocation.ID, "pool:child:") {
			t.Fatalf("conflicting child create left carve-out %s in parent-b", allocation.CIDR)
		}
	}

	assertNoError(t, client.DeletePool(t.Context(), "parent-b"))
}

type childCreateBarrierStore struct {
	storage.Storage
	key     string
	reached chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (store *childCreateBarrierStore) CreateIfAbsent(
	ctx context.Context,
	key string,
	data []byte,
) (storage.Version, error) {
	if key == store.key {
		store.once.Do(func() {
			close(store.reached)

			select {
			case <-store.resume:
			case <-ctx.Done():
			}
		})
	}

	return store.Storage.CreateIfAbsent(ctx, key, data)
}

func TestCreateChildRacingDeleteNeverLosesCarveOut(t *testing.T) {
	base := memory.New()
	admin := New(base, WithSleeper(noSleep))
	mustCreatePool(t, admin, "root", "10.0.0.0/16")

	barrier := &childCreateBarrierStore{
		Storage: base,
		key:     reservationsKey("child"),
		reached: make(chan struct{}),
		resume:  make(chan struct{}),
	}
	creator := New(barrier, WithSleeper(noSleep))
	result := make(chan error, 1)

	go func() {
		_, err := creator.CreatePool(t.Context(), PoolSpec{
			ID:         "child",
			ParentPool: "root",
			Request:    Request{Kind: RequestNext, PrefixLength: 24},
		})
		result <- err
	}()

	<-barrier.reached

	deleteErr := New(base, WithSleeper(noSleep)).DeletePool(t.Context(), "child")
	if !errors.Is(deleteErr, ErrNotFound) {
		t.Fatalf("concurrent DeletePool() error = %v, want ErrNotFound", deleteErr)
	}

	close(barrier.resume)
	assertNoError(t, <-result)

	child, err := admin.GetPool(t.Context(), "child")
	assertNoError(t, err)
	rootAllocation, err := admin.GetAllocation(t.Context(), "root", child.Parent.AllocationID)
	assertNoError(t, err)

	if child.CIDRs[0] != rootAllocation.CIDR {
		t.Fatalf("child CIDR %s does not match carve-out %s", child.CIDRs[0], rootAllocation.CIDR)
	}

	other, err := admin.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)

	if child.CIDRs[0].Overlaps(other.CIDR) {
		t.Fatalf("child CIDR %s overlaps root allocation %s", child.CIDRs[0], other.CIDR)
	}
}

func TestRetryBackoffUsesFullJitterAndCap(t *testing.T) {
	var random bytes.Buffer

	for _, value := range []uint64{3, 7, 11} {
		var encoded [8]byte
		binary.LittleEndian.PutUint64(encoded[:], value)
		_, _ = random.Write(encoded[:])
	}

	var delays []time.Duration

	client := New(
		memory.New(),
		WithBackoff(10*time.Millisecond, 25*time.Millisecond),
		WithRand(&random),
		WithSleeper(func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)

			return nil
		}),
	)

	for attempt := range 3 {
		assertNoError(t, client.waitRetry(t.Context(), attempt))
	}

	want := []time.Duration{3, 7, 11}
	if !slices.Equal(delays, want) {
		t.Fatalf("retry delays = %v, want %v", delays, want)
	}

	for index, limit := range []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 25 * time.Millisecond} {
		if delays[index] < 0 || delays[index] > limit {
			t.Fatalf("retry delay %d = %s, want within [0,%s]", index, delays[index], limit)
		}
	}

	if delays[0] == delays[1] || delays[1] == delays[2] {
		t.Fatalf("different random values produced equal delays: %v", delays)
	}
}

func TestDefaultSleeperHonoursCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepContext() error = %v, want context.Canceled", err)
	}
}

func TestPoolLockWaitHonoursContextCancellation(t *testing.T) {
	client := New(memory.New())
	lock := client.poolLock("prod")
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan error, 1)

	go func() {
		_, err := client.Allocate(ctx, "prod", AllocationSpec{PrefixLength: 24})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Allocate() error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Allocate() did not honor cancellation while waiting for pool lock")
	}
}

type differentRequestUnknownStore struct {
	storage.Storage
	once sync.Once
}

func (store *differentRequestUnknownStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	triggered := false

	store.once.Do(func() { triggered = true })

	if !triggered {
		return store.Storage.PutIfMatch(ctx, key, data, version)
	}

	reservations, err := decodeReservationsDocument(data, "prod")
	if err != nil {
		return "", err
	}

	for id, allocation := range reservations.Allocations {
		allocation.Request = Request{
			Kind: RequestExplicit,
			CIDR: netip.MustParsePrefix("10.0.200.0/24"),
		}
		allocation.CIDR = allocation.Request.CIDR
		reservations.Allocations[id] = allocation
	}

	different, err := encodeReservationsDocument(reservations)
	if err != nil {
		return "", err
	}

	if _, err := store.Storage.PutIfMatch(ctx, key, different, version); err != nil {
		return "", err
	}

	return "", errors.Join(storage.ErrUnknownOutcome, storage.ErrPreconditionFailed)
}

func TestUnknownOutcomeDifferentRequestIsNotSuccess(t *testing.T) {
	base := memory.New()
	client := New(base, WithSleeper(noSleep))
	mustCreatePool(t, client, "prod", "10.0.0.0/16")

	store := &differentRequestUnknownStore{Storage: base}
	_, err := New(store, WithSleeper(noSleep)).Allocate(t.Context(), "prod", AllocationSpec{
		ID:           testAllocationID,
		PrefixLength: 24,
	})
	assertConflictReason(t, err, "id_reused")
}
