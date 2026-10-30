// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
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
