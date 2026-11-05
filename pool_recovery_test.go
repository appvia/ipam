// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/local"
	"github.com/appvia/ipam/storage/memory"
)

func TestChildCreateCancellationRollback(t *testing.T) {
	t.Run("AC-35", func(t *testing.T) {
		runPoolRecoveryCases(t, func(t *testing.T, openStore func() storage.Storage, options []Option) {
			setup := New(openStore(), options...)
			mustCreatePool(t, setup, "parent", "10.0.0.0/23")

			ctx, cancel := context.WithCancel(t.Context())

			blocked := &cancelPublicationStore{
				Storage: openStore(),
				entered: make(chan struct{}),
			}
			result := make(chan error, 1)
			done := make(chan struct{})

			go func() {
				defer close(done)

				_, err := New(blocked, options...).CreatePool(ctx, recoveryChildSpec())
				result <- err
			}()
			defer func() {
				cancel()
				<-done
			}()

			select {
			case <-blocked.entered:
			case err := <-result:
				t.Fatalf("CreatePool returned before child publication: %v", err)
			case <-t.Context().Done():
				t.Fatal("test cancelled before child publication")
			}

			// These reads must not share the context that will be cancelled.
			fresh := context.Background()
			observer := New(openStore(), options...)
			carve := assertRecoveryCarveOut(t, fresh, observer)
			assertRecoveryChildDocumentsAbsent(t, fresh, openStore())
			t.Logf("setup: parent reservation %s at %s committed before child reservations write", carve.ID, carve.CIDR)

			cancel()

			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("CreatePool error = %v, want context.Canceled", err)
			}

			t.Log("failure identity: errors.Is(CreatePool error, context.Canceled) = true")
			assertRecoveryChildDocumentsAbsent(t, fresh, openStore())
			assertRecoveryParentReleased(t, fresh, New(openStore(), options...))

			mustCreatePool(t, observer, "parent", "10.0.0.0/23")
			recreated, err := observer.CreatePool(fresh, recoveryChildSpec())
			assertNoError(t, err)

			if recreated.CIDRs[0] != recoveryChildSpec().Request.CIDR {
				t.Fatalf("recreated child CIDRs = %v, want explicit request", recreated.CIDRs)
			}
		})
	})
}

func TestChildCreateCompensationGuards(t *testing.T) {
	t.Run("AC-35", func(t *testing.T) {
		for _, scenario := range []string{
			"no-owned-documents", "owned-reservations", "owned-pool", "foreign-winner", "cleanup-error", "parent-absent",
		} {
			t.Run(scenario, func(t *testing.T) {
				runPoolRecoveryCases(t, func(t *testing.T, openStore func() storage.Storage, options []Option) {
					base := openStore()
					setup := New(base, options...)
					mustCreatePool(t, setup, "parent", "10.0.0.0/23")
					child, err := setup.CreatePool(t.Context(), recoveryChildSpec())
					assertNoError(t, err)

					if scenario != "owned-pool" {
						rawDelete(t, base, poolKey("child"))
					}

					if scenario != "owned-reservations" {
						rawDelete(t, base, reservationsKey("child"))
					}

					var winner Pool

					winnerVersions := make(map[string]storage.Version)

					if scenario == "foreign-winner" {
						spec := recoveryChildSpec()
						spec.Request.CIDR = netip.MustParsePrefix("10.0.1.0/24")
						winner, err = New(openStore(), options...).CreatePool(t.Context(), spec)
						assertNoError(t, err)

						for _, key := range []string{poolKey("child"), reservationsKey("child")} {
							_, version, readErr := base.Get(t.Context(), key)
							assertNoError(t, readErr)

							winnerVersions[key] = version
						}
					}

					if scenario == "parent-absent" {
						rawDelete(t, base, reservationsKey("parent"))
						rawDelete(t, base, poolKey("parent"))
					}

					cleanupFailure := errors.New("synthetic compensation failure")

					store := openStore()
					if scenario == "cleanup-error" {
						store = &recoveryReleaseFailureStore{Storage: store, err: cleanupFailure}
					}

					probe := &recoveryCompensationContextStore{Storage: store}

					type contextKey struct{}

					ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "synthetic-value"))
					cancel()

					cause := errors.New("synthetic publication failure")
					before := time.Now()
					err = New(probe, options...).releaseOwnCarveOut(ctx, child.Parent, false, cause)
					after := time.Now()

					if probe.ctx == nil {
						t.Fatal("compensation did not reach storage")
					}

					deadline, bounded := probe.ctx.Deadline()
					if probe.entryErr != nil || !bounded || deadline.Before(before.Add(5*time.Second)) ||
						deadline.After(after.Add(5*time.Second)) || probe.ctx.Value(contextKey{}) != "synthetic-value" {
						t.Errorf("compensation context: entry error = %v, deadline = %v (bounded %t), value = %v",
							probe.entryErr, deadline, bounded, probe.ctx.Value(contextKey{}))
					}

					if probe.ctx.Err() != context.Canceled {
						t.Errorf("compensation context after return = %v, want canceled", probe.ctx.Err())
					}

					if scenario == "cleanup-error" {
						if !errors.Is(err, cause) || !errors.Is(err, cleanupFailure) {
							t.Errorf("compensation error = %v, want both causes", err)
						}
					} else if err != cause {
						t.Errorf("compensation error = %v, want only original cause %v", err, cause)
					}

					if scenario == "parent-absent" {
						return
					}

					observer := New(openStore(), options...)
					if scenario == "owned-reservations" || scenario == "owned-pool" || scenario == "cleanup-error" {
						carve := assertRecoveryCarveOut(t, t.Context(), observer)
						if carve.ID != child.Parent.AllocationID {
							t.Errorf("retained carve-out = %s, want %s", carve.ID, child.Parent.AllocationID)
						}

						return
					}

					if scenario == "foreign-winner" {
						allocations, listErr := observer.ListAllocations(t.Context(), "parent")
						assertNoError(t, listErr)

						if len(allocations) != 1 || allocations[0].ID != winner.Parent.AllocationID ||
							allocations[0].CIDR != winner.CIDRs[0] {
							t.Errorf("winner reservations = %#v, want only %s", allocations, winner.Parent.AllocationID)
						}

						for key, want := range winnerVersions {
							_, version, readErr := base.Get(t.Context(), key)
							assertNoError(t, readErr)

							if version != want {
								t.Errorf("winner %s version changed from %s to %s", key, want, version)
							}
						}

						return
					}

					assertRecoveryParentReleased(t, t.Context(), observer)
				})
			})
		}
	})
}

type recoveryCompensationContextStore struct {
	storage.Storage
	ctx      context.Context
	entryErr error
}

func (store *recoveryCompensationContextStore) Get(
	ctx context.Context, key string,
) ([]byte, storage.Version, error) {
	if store.ctx == nil {
		store.ctx = ctx
		store.entryErr = ctx.Err()
	}

	return store.Storage.Get(ctx, key)
}

func TestChildDeleteReleaseRetry(t *testing.T) {
	t.Run("AC-11", func(t *testing.T) {
		runPoolRecoveryCases(t, func(t *testing.T, openStore func() storage.Storage, options []Option) {
			setup := New(openStore(), options...)
			mustCreatePool(t, setup, "parent", "10.0.0.0/23")
			child, err := setup.CreatePool(t.Context(), recoveryChildSpec())
			assertNoError(t, err)

			fresh := context.Background()
			carve := assertRecoveryCarveOut(t, fresh, New(openStore(), options...))
			if child.Parent == nil || child.Parent.AllocationID != carve.ID {
				t.Fatalf("child parent = %#v, want carve-out %s", child.Parent, carve.ID)
			}

			t.Logf("setup: child published with parent reservation %s at %s", carve.ID, carve.CIDR)

			injected := errors.New("synthetic parent release failure before commit")
			failing := &recoveryReleaseFailureStore{Storage: openStore(), err: injected}
			err = New(failing, options...).DeletePool(t.Context(), "child")
			if !errors.Is(err, injected) || !failing.failed.Load() {
				t.Fatalf("DeletePool error = %v, injected CAS reached = %t", err, failing.failed.Load())
			}

			t.Log("failure identity: errors.Is(DeletePool error, injected) = true; parent CAS did not commit")

			// Neither client-local state nor the failing decorator is available to recovery.
			retry := New(openStore(), options...)
			remaining := assertRecoveryCarveOut(t, fresh, retry)
			if remaining.ID != carve.ID || remaining.CIDR != carve.CIDR {
				t.Fatalf("reservation after failed release = %#v, want %#v", remaining, carve)
			}

			// AC-25/AC-31: a fresh caller finishes an interrupted deletion.
			err = retry.DeletePool(fresh, "child")
			if err != nil && !errors.Is(err, ErrNotFound) {
				t.Fatalf("DeletePool retry error = %v", err)
			}

			if _, err := retry.GetPool(fresh, "child"); !errors.Is(err, ErrNotFound) {
				t.Errorf("GetPool after delete retry error = %v, want ErrNotFound", err)
			}

			assertRecoveryChildDocumentsAbsent(t, fresh, openStore())
			assertRecoveryParentReleased(t, fresh, New(openStore(), options...))
		})
	})
}

func recoveryChildSpec() PoolSpec {
	return PoolSpec{
		ID:         "child",
		ParentPool: "parent",
		Request:    Request{Kind: RequestExplicit, CIDR: netip.MustParsePrefix("10.0.0.0/24")},
	}
}

func runPoolRecoveryCases(t *testing.T, test func(*testing.T, func() storage.Storage, []Option)) {
	t.Helper()

	for _, backend := range []string{"memory", "local"} {
		t.Run(backend, func(t *testing.T) {
			for _, locks := range []string{"with-pool-locks", "without-pool-locks"} {
				t.Run(locks, func(t *testing.T) {
					base := memory.New()
					openStore := func() storage.Storage { return base }

					if backend == "local" {
						dir := t.TempDir()
						openStore = func() storage.Storage {
							store, err := local.New(dir)
							assertNoError(t, err)
							t.Cleanup(func() { assertNoError(t, store.Close()) })

							return store
						}
					}

					options := []Option{
						WithClock(func() time.Time { return time.Unix(1, 0).UTC() }),
						WithSleeper(noSleep),
					}
					if locks == "without-pool-locks" {
						options = append(options, withoutPoolLocks())
					}

					test(t, openStore, options)
				})
			}
		})
	}
}

func assertRecoveryCarveOut(t *testing.T, ctx context.Context, client *Client) Allocation {
	t.Helper()

	allocations, err := client.ListAllocations(ctx, "parent")
	assertNoError(t, err)

	if len(allocations) != 1 || !strings.HasPrefix(allocations[0].ID, "pool:child:") ||
		allocations[0].CIDR != recoveryChildSpec().Request.CIDR {
		t.Fatalf("parent reservations = %#v, want only the child's carve-out", allocations)
	}

	carve := allocations[0]
	probe, err := client.Allocate(ctx, "parent", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)

	if probe.CIDR.Overlaps(carve.CIDR) {
		t.Fatalf("parent allocation %s overlaps reserved child range %s", probe.CIDR, carve.CIDR)
	}

	assertNoError(t, client.Release(ctx, "parent", probe.ID))

	return carve
}

func assertRecoveryChildDocumentsAbsent(t *testing.T, ctx context.Context, store storage.Storage) {
	t.Helper()

	for _, key := range []string{poolKey("child"), reservationsKey("child")} {
		if _, _, err := store.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("Get(%s) error = %v, want storage.ErrNotFound", key, err)
		}
	}
}

func assertRecoveryParentReleased(t *testing.T, ctx context.Context, client *Client) {
	t.Helper()

	allocations, err := client.ListAllocations(ctx, "parent")
	assertNoError(t, err)

	if len(allocations) != 0 {
		t.Errorf("carve-out remains reserved after recovery: %v", allocations)
	}

	// AC-11: the parent whose only allocation was the carve-out is deletable.
	if err := client.DeletePool(ctx, "parent"); err != nil {
		t.Errorf("DeletePool(parent) after recovery error = %v, want nil", err)
	}
}

// cancelPublicationStore stops before any child document is written. Unlike
// failWriteStore, it waits for cancellation and preserves its context error.
type cancelPublicationStore struct {
	storage.Storage
	entered chan struct{}
}

func (store *cancelPublicationStore) CreateIfAbsent(
	ctx context.Context, key string, data []byte,
) (storage.Version, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if key == reservationsKey("child") {
		close(store.entered)
		<-ctx.Done()

		return "", ctx.Err()
	}

	return store.Storage.CreateIfAbsent(ctx, key, data)
}

// recoveryReleaseFailureStore rejects one parent CAS before it can commit,
// rather than reporting a failure after a successful write like failWriteStore.
type recoveryReleaseFailureStore struct {
	storage.Storage
	err    error
	failed atomic.Bool
}

func (store *recoveryReleaseFailureStore) PutIfMatch(
	ctx context.Context, key string, data []byte, version storage.Version,
) (storage.Version, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if key == reservationsKey("parent") && store.failed.CompareAndSwap(false, true) {
		return "", store.err
	}

	return store.Storage.PutIfMatch(ctx, key, data, version)
}
