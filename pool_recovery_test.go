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
			assertRecoveryPendingChild(t, openStore(), options, child)

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

// AC-11/AC-25/AC-31: each durable deletion boundary remains recoverable by a
// separate client, including a committed release whose response was lost.
func TestChildDeleteRecoveryBoundaries(t *testing.T) {
	for _, scenario := range []string{"unknown-release", "unknown-before-release", "final-delete", "cancel-release", "absent-parent"} {
		t.Run(scenario, func(t *testing.T) {
			runPoolRecoveryCases(t, func(t *testing.T, openStore func() storage.Storage, options []Option) {
				setup := New(openStore(), options...)
				mustCreatePool(t, setup, "parent", "10.0.0.0/23")
				child, err := setup.CreatePool(t.Context(), recoveryChildSpec())
				assertNoError(t, err)

				injected := errors.New("synthetic final child delete failure")
				fault := &recoveryDeleteBoundaryStore{Storage: openStore(), scenario: scenario, err: injected}

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				fault.cancel = cancel

				if scenario == "absent-parent" {
					rawDelete(t, openStore(), poolKey("parent"))
					rawDelete(t, openStore(), reservationsKey("parent"))
				}

				err = New(fault, options...).DeletePool(ctx, "child")

				switch scenario {
				case "cancel-release":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("DeletePool = %v, want context.Canceled", err)
					}
				case "final-delete":
					if !errors.Is(err, injected) {
						t.Fatalf("DeletePool = %v, want original failure", err)
					}
				default:
					assertNoError(t, err)
				}

				if scenario == "cancel-release" || scenario == "final-delete" {
					assertRecoveryPendingChild(t, openStore(), options, child)

					if scenario == "final-delete" {
						allocations, listErr := setup.ListAllocations(t.Context(), "parent")
						assertNoError(t, listErr)

						if len(allocations) != 0 {
							t.Fatalf("final child delete attempted before parent release: %v", allocations)
						}
					}
					// Use a healthy client; the failure decorator and cancelled
					// context are deliberately not available to recovery.
					assertNoError(t, New(openStore(), options...).DeletePool(t.Context(), "child"))
				}

				assertRecoveryChildDocumentsAbsent(t, t.Context(), openStore())

				if scenario != "absent-parent" {
					// Confirm explicit reuse, not merely an empty allocation listing.
					retry := New(openStore(), options...)
					allocation, allocErr := retry.Allocate(t.Context(), "parent", AllocationSpec{CIDR: child.CIDRs[0]})
					assertNoError(t, allocErr)
					assertNoError(t, retry.Release(t.Context(), "parent", allocation.ID))
					assertRecoveryParentReleased(t, t.Context(), retry)
				}
			})
		})
	}
}

func assertRecoveryPendingChild(t *testing.T, store storage.Storage, options []Option, child Pool) {
	t.Helper()

	data, _, err := store.Get(t.Context(), poolKey("child"))
	assertNoError(t, err)
	definition, err := decodePoolDocument(data, "child")
	assertNoError(t, err)

	if !parentEqual(definition.Parent, child.Parent) {
		t.Fatalf("retained parent = %#v, want %#v", definition.Parent, child.Parent)
	}

	if _, _, err := store.Get(t.Context(), reservationsKey("child")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("child reservations = %v, want absent", err)
	}

	client := New(store, options...)
	if err := client.Release(t.Context(), "parent", child.Parent.AllocationID); !conflictReason(err, "pool_in_use") {
		t.Fatalf("public Release = %v, want pool_in_use", err)
	}

	recorder := &recordingStore{Storage: store}

	_, err = New(recorder, options...).CreatePool(t.Context(), recoveryChildSpec())
	if !conflictReason(err, "pool_incomplete") || len(recorder.writeKeys()) != 0 {
		t.Fatalf("CreatePool pending child = %v, writes %v", err, recorder.writeKeys())
	}

	assertLifecycleInvariants(t, store)
}

func TestChildDeleteCooldownOnce(t *testing.T) {
	t.Run("AC-11", func(t *testing.T) {
		runPoolRecoveryCases(t, func(t *testing.T, openStore func() storage.Storage, options []Option) {
			setup := New(openStore(), options...)
			_, err := setup.CreatePool(t.Context(), PoolSpec{
				ID: "parent", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/23")},
				Policy: PoolPolicy{Cooldown: Cooldown{Mode: CooldownStrict, Duration: time.Hour}},
			})
			assertNoError(t, err)
			child, err := setup.CreatePool(t.Context(), recoveryChildSpec())
			assertNoError(t, err)
			fault := &recoveryDeleteBoundaryStore{Storage: openStore(), scenario: "unknown-release-final-delete", err: errInjectedCrash}

			err = New(fault, options...).DeletePool(t.Context(), "child")
			if !errors.Is(err, errInjectedCrash) || fault.puts != 1 {
				t.Fatalf("DeletePool = %v, parent writes = %d", err, fault.puts)
			}

			_, before, version, err := setup.readPoolState(t.Context(), "test", "parent")
			assertNoError(t, err)

			if expiry := before.Released[child.CIDRs[0]]; !expiry.Equal(time.Unix(1, 0).Add(time.Hour)) {
				t.Fatalf("cooldown expiry = %v", expiry)
			}

			later := append([]Option(nil), options...)
			later = append(later, WithClock(func() time.Time { return time.Unix(61, 0) }))
			assertNoError(t, New(openStore(), later...).DeletePool(t.Context(), "child"))
			_, after, afterVersion, err := setup.readPoolState(t.Context(), "test", "parent")
			assertNoError(t, err)

			if afterVersion != version || !after.Released[child.CIDRs[0]].Equal(before.Released[child.CIDRs[0]]) {
				t.Fatal("retry rewrote parent or extended cooldown")
			}

			assertRecoveryChildDocumentsAbsent(t, t.Context(), openStore())
			assertRecoveryParentReleased(t, t.Context(), setup)
		})
	})
}

func TestChildDeleteReleaseRechecksReservations(t *testing.T) {
	runPoolRecoveryCases(t, func(t *testing.T, openStore func() storage.Storage, options []Option) {
		setup := New(openStore(), options...)
		mustCreatePool(t, setup, "parent", "10.0.0.0/23")
		child, err := setup.CreatePool(t.Context(), recoveryChildSpec())
		assertNoError(t, err)
		data, _, err := openStore().Get(t.Context(), poolKey("child"))
		assertNoError(t, err)
		definition, err := decodePoolDocument(data, "child")
		assertNoError(t, err)

		gate := newGateStore(openStore())
		blocked := gate.arm("put", reservationsKey("parent"))
		result := make(chan error, 1)

		done := make(chan struct{})
		go func() {
			defer close(done)

			result <- New(gate, options...).DeletePool(t.Context(), "child")
		}()

		defer func() {
			select {
			case <-blocked.resume:
			default:
				close(blocked.resume)
			}

			<-done
		}()

		<-blocked.reached

		// Model a late write by the original creator. Force a CAS retry so the
		// deleter must re-read the documents, not reuse its earlier permission.
		rawCreateReservations(t, openStore(), "child", definition.Incarnation, nil)
		probe, err := setup.Allocate(t.Context(), "parent", AllocationSpec{CIDR: netip.MustParsePrefix("10.0.1.0/24")})
		assertNoError(t, err)
		close(blocked.resume)

		if err := <-result; !conflictReason(err, "pool_in_use") {
			t.Fatalf("DeletePool after reservations reappeared = %v", err)
		}

		assertConflictReason(t, setup.Release(t.Context(), "parent", child.Parent.AllocationID), "pool_in_use")

		cause := errors.New("synthetic publication error")
		if err := setup.releaseOwnCarveOut(t.Context(), child.Parent, false, cause); err != cause {
			t.Fatalf("create rollback = %v, want original error only", err)
		}

		carve, err := setup.GetAllocation(t.Context(), "parent", child.Parent.AllocationID)
		assertNoError(t, err)

		if carve.CIDR != child.CIDRs[0] || carve.CIDR.Overlaps(probe.CIDR) {
			t.Fatal("guard lost child carve-out or allowed overlap")
		}

		assertStrictLifecycleInvariants(t, openStore())
		assertNoError(t, setup.Release(t.Context(), "parent", probe.ID))
		assertNoError(t, New(openStore(), options...).DeletePool(t.Context(), "child"))
		assertRecoveryParentReleased(t, t.Context(), setup)
	})
}

func TestChildDeleteNonemptyResidue(t *testing.T) {
	for _, shape := range []string{"complete", "reservations-only", "mismatched"} {
		t.Run(shape, func(t *testing.T) {
			runPoolRecoveryCases(t, func(t *testing.T, openStore func() storage.Storage, options []Option) {
				setup := New(openStore(), options...)
				mustCreatePool(t, setup, "parent", "10.0.0.0/23")
				_, err := setup.CreatePool(t.Context(), recoveryChildSpec())
				assertNoError(t, err)
				_, err = setup.Allocate(t.Context(), "child", AllocationSpec{PrefixLength: 28})
				assertNoError(t, err)

				switch shape {
				case "reservations-only":
					rawDelete(t, openStore(), poolKey("child"))
				case "mismatched":
					_, reservations, _, readErr := setup.readPoolState(t.Context(), "test", "child")
					assertNoError(t, readErr)
					rawDelete(t, openStore(), reservationsKey("child"))
					rawCreateReservations(t, openStore(), "child", strings.Repeat("e", 32), reservations.Allocations)
				}

				recorder := &recordingStore{Storage: openStore()}
				assertConflictReason(t, New(recorder, options...).DeletePool(t.Context(), "child"), "pool_not_empty")

				if writes := recorder.writeKeys(); len(writes) != 0 {
					t.Fatalf("nonempty residue deletion wrote %v", writes)
				}

				assertRecoveryCarveOut(t, t.Context(), setup)
			})
		})
	}
}

func TestChildDeleteConcurrentAndStale(t *testing.T) {
	for _, phase := range []string{"concurrent-release", "stale-reservations", "stale-definition"} {
		t.Run(phase, func(t *testing.T) {
			runPoolRecoveryCases(t, func(t *testing.T, openStore func() storage.Storage, options []Option) {
				setup := New(openStore(), options...)
				mustCreatePool(t, setup, "parent", "10.0.0.0/23")
				_, err := setup.CreatePool(t.Context(), recoveryChildSpec())
				assertNoError(t, err)

				gate := newGateStore(openStore())
				op, key := "put", reservationsKey("parent")

				switch phase {
				case "stale-reservations":
					op, key = "delete", reservationsKey("child")
				case "stale-definition":
					op, key = "delete", poolKey("child")
				}

				blocked := gate.arm(op, key)
				result := make(chan error, 1)

				done := make(chan struct{})
				go func() {
					defer close(done)

					result <- New(gate, options...).DeletePool(t.Context(), "child")
				}()

				defer func() {
					select {
					case <-blocked.resume:
					default:
						close(blocked.resume)
					}

					<-done
				}()

				<-blocked.reached

				assertNoError(t, New(openStore(), options...).DeletePool(t.Context(), "child"))

				versions := make(map[string]storage.Version)

				var replacement Pool

				if phase != "concurrent-release" {
					spec := recoveryChildSpec()
					spec.Request.CIDR = netip.MustParsePrefix("10.0.1.0/24")
					replacement, err = New(openStore(), options...).CreatePool(t.Context(), spec)
					assertNoError(t, err)

					for _, key := range []string{poolKey("child"), reservationsKey("child"), reservationsKey("parent")} {
						_, versions[key], err = openStore().Get(t.Context(), key)
						assertNoError(t, err)
					}
				}

				close(blocked.resume)
				assertNoError(t, <-result)

				for key, want := range versions {
					_, got, readErr := openStore().Get(t.Context(), key)
					assertNoError(t, readErr)

					if got != want {
						t.Fatalf("stale delete changed replacement %s", key)
					}
				}

				if phase != "concurrent-release" {
					carve, readErr := setup.GetAllocation(t.Context(), "parent", replacement.Parent.AllocationID)
					assertNoError(t, readErr)

					if carve.CIDR != replacement.CIDRs[0] {
						t.Fatal("replacement lost its carve-out")
					}

					assertStrictLifecycleInvariants(t, openStore())
					assertNoError(t, setup.DeletePool(t.Context(), "child"))
				}

				assertRecoveryChildDocumentsAbsent(t, t.Context(), openStore())
				assertRecoveryParentReleased(t, t.Context(), setup)
			})
		})
	}
}

type recoveryDeleteBoundaryStore struct {
	storage.Storage
	scenario string
	err      error
	cancel   context.CancelFunc
	puts     int
}

func (store *recoveryDeleteBoundaryStore) PutIfMatch(
	ctx context.Context, key string, data []byte, version storage.Version,
) (storage.Version, error) {
	if key == reservationsKey("parent") {
		store.puts++
		if store.scenario == "cancel-release" {
			store.cancel()
			return "", ctx.Err()
		}

		if store.scenario == "unknown-before-release" && store.puts == 1 {
			return "", storage.ErrUnknownOutcome
		}

		if strings.HasPrefix(store.scenario, "unknown-release") {
			next, err := store.Storage.PutIfMatch(ctx, key, data, version)
			if err != nil {
				return next, err
			}

			return "", storage.ErrUnknownOutcome
		}
	}

	return store.Storage.PutIfMatch(ctx, key, data, version)
}

func (store *recoveryDeleteBoundaryStore) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	if key == poolKey("child") && strings.Contains(store.scenario, "final-delete") {
		return store.err
	}

	return store.Storage.DeleteIfMatch(ctx, key, version)
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
