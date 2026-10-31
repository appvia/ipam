// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

type deletedPublishedChildStore struct {
	storage.Storage
	onPublished func()
	outcome     error
}

func (store *deletedPublishedChildStore) CreateIfAbsent(
	ctx context.Context, key string, data []byte,
) (storage.Version, error) {
	version, err := store.Storage.CreateIfAbsent(ctx, key, data)
	if err != nil || key != poolKey("child") {
		return version, err
	}

	store.onPublished()

	return version, store.outcome
}

func TestCooldownChildRollbackRecordsPossiblyPublishedRelease(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledged", true: "unknown outcome"}[unknown], func(t *testing.T) {
			// Given a strict parent and a deleter paused before releasing its carve-out.
			admin, base, clock := newCooldownClient(t, CooldownStrict)

			var carveID string

			store := &deletedPublishedChildStore{Storage: base, onPublished: func() {
				child, err := admin.GetPool(t.Context(), "child")
				assertNoError(t, err)

				carveID = child.Parent.AllocationID

				rawDelete(t, base, reservationsKey("child"))
				rawDelete(t, base, poolKey("child"))
			}}
			if unknown {
				store.outcome = storage.ErrUnknownOutcome
			}

			creator := New(store, WithClock(clock.now), WithSleeper(noSleep), WithMaxAttempts(1))

			// When rollback wins the parent release after publication and deletion.
			_, err := creator.CreatePool(t.Context(), childPoolSpec("child", 24))

			// Then rollback and the deleter's late release preserve strict cooldown.
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("CreatePool() = %v, want ErrConflict", err)
			}

			assertNoError(t, admin.Release(t.Context(), "root", carveID))
			stored := readCooldownReservations(t, base)
			cidr := netip.MustParsePrefix("10.0.0.0/24")

			if len(stored.Allocations) != 0 || !stored.Released[cidr].Equal(clock.now().Add(time.Hour)) {
				t.Fatalf("rollback reservations = %+v, want cooldown for %s", stored, cidr)
			}

			_, err = admin.Allocate(t.Context(), "root", AllocationSpec{ID: testAllocationID, PrefixLength: 24})
			assertCooldownError(t, err, "allocate", "root", testAllocationID)
		})
	}
}

type replacedPolicyPoolStore struct {
	storage.Storage
	key     string
	replace func()
	landed  bool
	unknown bool
}

func (store *replacedPolicyPoolStore) PutIfMatch(
	ctx context.Context, key string, data []byte, version storage.Version,
) (storage.Version, error) {
	if key != store.key || store.replace == nil {
		return store.Storage.PutIfMatch(ctx, key, data, version)
	}

	replace := store.replace
	store.replace = nil

	if store.landed {
		written, err := store.Storage.PutIfMatch(ctx, key, data, version)
		if err != nil {
			return written, err
		}

		replace()

		if store.unknown {
			return "", storage.ErrUnknownOutcome
		}

		return written, nil
	}

	replace()

	return store.Storage.PutIfMatch(ctx, key, data, version)
}

func TestUpdatePoolPolicyDoesNotModifyReplacement(t *testing.T) {
	for _, test := range []struct {
		name    string
		key     string
		landed  bool
		unknown bool
	}{
		{name: "between phases", key: poolKey("prod"), landed: true},
		{name: "strategy retry", key: poolKey("prod")},
		{name: "strategy unknown outcome", key: poolKey("prod"), landed: true, unknown: true},
		{name: "cooldown retry", key: reservationsKey("prod")},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given a replacement created at the selected policy write boundary.
			base := memory.New()
			admin := newPolicyPool(t, base, lowestNoCooldown)

			var poolBefore, reservationsBefore []byte

			store := &replacedPolicyPoolStore{
				Storage: base, key: test.key, landed: test.landed, unknown: test.unknown,
				replace: func() {
					assertNoError(t, admin.DeletePool(t.Context(), "prod"))
					_, err := admin.CreatePool(t.Context(), policySpec("prod", PoolPolicy{
						Strategy: StrategyLowest,
						Cooldown: Cooldown{Mode: CooldownFitAware, Duration: 2 * time.Hour},
					}))
					assertNoError(t, err)

					poolBefore = storedBytes(t, base, poolKey("prod"))
					reservationsBefore = storedBytes(t, base, reservationsKey("prod"))
				},
			}
			updater := newTestClient(store)

			// When an update started on the original incarnation continues.
			err := updater.UpdatePoolPolicy(t.Context(), "prod", strictThirtyMinutes)

			// Then neither replacement document is rewritten.
			assertPoolIncomplete(t, err)

			if !bytes.Equal(poolBefore, storedBytes(t, base, poolKey("prod"))) ||
				!bytes.Equal(reservationsBefore, storedBytes(t, base, reservationsKey("prod"))) {
				t.Fatal("UpdatePoolPolicy modified the replacement pool")
			}
		})
	}
}
