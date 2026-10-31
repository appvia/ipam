// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/appvia/ipam/internal/alloc"
	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

type cooldownTestClock struct {
	nanos atomic.Int64
}

func newCooldownTestClock() *cooldownTestClock {
	clock := &cooldownTestClock{}
	clock.nanos.Store(time.Date(2026, 10, 30, 12, 0, 0, 250000000, time.UTC).UnixNano())

	return clock
}

func (clock *cooldownTestClock) now() time.Time {
	return time.Unix(0, clock.nanos.Load()).UTC()
}

func (clock *cooldownTestClock) advance(duration time.Duration) {
	clock.nanos.Add(int64(duration))
}

func newCooldownClient(t *testing.T, mode CooldownMode) (*Client, *memory.Store, *cooldownTestClock) {
	t.Helper()

	base := memory.New()
	clock := newCooldownTestClock()
	client := New(base, WithClock(clock.now), WithSleeper(noSleep))
	_, err := client.CreatePool(t.Context(), PoolSpec{
		ID: "root", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
		Policy: PoolPolicy{Cooldown: Cooldown{Mode: mode, Duration: time.Hour}},
	})
	assertNoError(t, err)

	return client, base, clock
}

func readCooldownReservations(t *testing.T, base storage.Storage) reservationsDocument {
	t.Helper()

	data, _, err := base.Get(t.Context(), reservationsKey("root"))
	assertNoError(t, err)
	reservations, err := decodeReservationsDocument(data, "root")
	assertNoError(t, err)

	return reservations
}

func writeCooldownReservations(t *testing.T, base storage.Storage, reservations reservationsDocument) {
	t.Helper()

	_, version, err := base.Get(t.Context(), reservationsKey("root"))
	assertNoError(t, err)
	data, err := encodeReservationsDocument(reservations)
	assertNoError(t, err)
	_, err = base.PutIfMatch(t.Context(), reservationsKey("root"), data, version)
	assertNoError(t, err)
}

func assertCooldownError(t *testing.T, err error, op, poolID, id string) {
	t.Helper()

	var opErr *OpError
	if !errors.Is(err, ErrPoolExhausted) || !errors.As(err, &opErr) ||
		opErr.Op != op || opErr.Pool != poolID || opErr.ID != id || opErr.Reason != "cooldown" {
		t.Fatalf("error = %v, want %s/%s/%s cooldown wrapping ErrPoolExhausted", err, op, poolID, id)
	}

	t.Log(err)
}

func TestAllocationStrategyChoosesTightHole(t *testing.T) {
	for _, strategy := range []AllocationStrategy{StrategyDefault, StrategyBestFit, StrategyLowest} {
		t.Run(string(strategy), func(t *testing.T) {
			client := newTestClient(memory.New())
			_, err := client.CreatePool(t.Context(), PoolSpec{
				ID: "root", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
				Policy: PoolPolicy{Strategy: strategy},
			})
			assertNoError(t, err)

			// Leave a low /25 and a higher, tight /28.
			for _, cidr := range []string{"10.0.0.128/26", "10.0.0.192/27", "10.0.0.240/28"} {
				_, err := client.Allocate(t.Context(), "root", AllocationSpec{CIDR: netip.MustParsePrefix(cidr)})
				assertNoError(t, err)
			}

			allocation, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 28})
			assertNoError(t, err)

			want := netip.MustParsePrefix("10.0.0.224/28")
			if strategy == StrategyLowest {
				want = netip.MustParsePrefix("10.0.0.0/28")
			}

			if allocation.CIDR != want {
				t.Fatalf("Allocate() = %s, want %s", allocation.CIDR, want)
			}
		})
	}
}

func TestCooldownStrictRefusesUntilExpiryAndPrunes(t *testing.T) {
	client, base, clock := newCooldownClient(t, CooldownStrict)
	first, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)
	assertNoError(t, client.Release(t.Context(), "root", first.ID))

	expiry := clock.now().Add(time.Hour)
	if got := readCooldownReservations(t, base).Released[first.CIDR]; !got.Equal(expiry) {
		t.Fatalf("release expiry = %s, want %s", got, expiry)
	}

	_, err = client.Allocate(t.Context(), "root", AllocationSpec{ID: testAllocationID, PrefixLength: 24})
	assertCooldownError(t, err, "allocate", "root", testAllocationID)

	clock.advance(time.Hour + time.Nanosecond)

	reused, err := client.Allocate(t.Context(), "root", AllocationSpec{ID: testAllocationID, PrefixLength: 24})
	assertNoError(t, err)

	if reused.CIDR != first.CIDR || len(readCooldownReservations(t, base).Released) != 0 {
		t.Fatalf("Allocate() = %s, want %s with no stored release record", reused.CIDR, first.CIDR)
	}
}

func TestCooldownUntilUsesFirstFittingExpiryRoundedUp(t *testing.T) {
	for _, fractional := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole second", true: "fractional second"}[fractional], func(t *testing.T) {
			client, base, clock := newCooldownClient(t, CooldownStrict)
			reservations := readCooldownReservations(t, base)

			early := clock.now().Truncate(time.Second).Add(time.Minute)
			if fractional {
				early = early.Add(time.Nanosecond)
			}

			reservations.Released = map[netip.Prefix]time.Time{
				netip.MustParsePrefix("10.0.0.0/25"):   early,
				netip.MustParsePrefix("10.0.0.128/26"): clock.now().Add(24 * time.Hour),
			}
			writeCooldownReservations(t, base, reservations)

			_, err := client.Allocate(t.Context(), "root", AllocationSpec{ID: testAllocationID, PrefixLength: 25})
			assertCooldownError(t, err, "allocate", "root", testAllocationID)

			want := early
			if fractional {
				want = early.Truncate(time.Second).Add(time.Second)
			}

			if !strings.Contains(err.Error(), want.Format(time.RFC3339)) {
				t.Fatalf("error = %v, want fitting expiry %s", err, want)
			}
		})
	}
}

func TestCooldownUntilSkipsExpiryThatCannotFreeFittingBlock(t *testing.T) {
	client, base, clock := newCooldownClient(t, CooldownStrict)
	_, err := client.Allocate(t.Context(), "root", AllocationSpec{CIDR: netip.MustParsePrefix("10.0.0.192/26")})
	assertNoError(t, err)
	reservations := readCooldownReservations(t, base)
	reservations.Released = map[netip.Prefix]time.Time{
		netip.MustParsePrefix("10.0.0.0/25"):   clock.now().Add(24 * time.Hour),
		netip.MustParsePrefix("10.0.0.128/26"): clock.now().Add(time.Minute),
	}
	writeCooldownReservations(t, base, reservations)

	_, err = client.Allocate(t.Context(), "root", AllocationSpec{ID: testAllocationID, PrefixLength: 25})
	assertCooldownError(t, err, "allocate", "root", testAllocationID)

	want := clock.now().Add(24 * time.Hour).Truncate(time.Second).Add(time.Second)
	if !strings.Contains(err.Error(), want.Format(time.RFC3339)) {
		t.Fatalf("error = %v, want fitting expiry %s rather than the first record's expiry", err, want)
	}
}

func TestCooldownFitAwareReusesOnlyFittingSpace(t *testing.T) {
	client, _, _ := newCooldownClient(t, CooldownFitAware)
	first, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)
	assertNoError(t, client.Release(t.Context(), "root", first.ID))

	reused, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)

	if reused.CIDR != first.CIDR {
		t.Fatalf("Allocate() = %s, want %s", reused.CIDR, first.CIDR)
	}
}

func TestCooldownExplicitIgnoresActiveReleaseAndReplayWins(t *testing.T) {
	client, base, _ := newCooldownClient(t, CooldownStrict)
	first, err := client.Allocate(t.Context(), "root", AllocationSpec{ID: testAllocationID, PrefixLength: 25})
	assertNoError(t, err)
	second, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 25})
	assertNoError(t, err)
	assertNoError(t, client.Release(t.Context(), "root", second.ID))

	// No next /25 fits, but the existing ID is checked before selection.
	replayed, err := client.Allocate(t.Context(), "root", AllocationSpec{ID: first.ID, PrefixLength: 25})
	assertNoError(t, err)

	if !allocationsEqual(replayed, first) {
		t.Fatalf("replayed = %+v, want %+v", replayed, first)
	}

	explicit, err := client.Allocate(t.Context(), "root", AllocationSpec{CIDR: second.CIDR})
	assertNoError(t, err)

	if explicit.CIDR != second.CIDR || len(readCooldownReservations(t, base).Released) != 1 {
		t.Fatalf("explicit = %+v, want cooling %s with record preserved", explicit, second.CIDR)
	}
}

func TestCooldownChildCreateUsesParentPolicy(t *testing.T) {
	client, _, _ := newCooldownClient(t, CooldownStrict)
	child, err := client.CreatePool(t.Context(), childPoolSpec("child", 24))
	assertNoError(t, err)
	assertNoError(t, client.DeletePool(t.Context(), child.ID))

	_, err = client.CreatePool(t.Context(), childPoolSpec("next", 24))
	assertCooldownError(t, err, "create pool", "next", "")
}

func TestCooldownChildRollbackDoesNotRecordRelease(t *testing.T) {
	for _, key := range []string{reservationsKey("child"), poolKey("child")} {
		t.Run(key, func(t *testing.T) {
			_, base, clock := newCooldownClient(t, CooldownStrict)
			failure := &failWriteStore{Storage: base, op: "create", key: key, err: errInjectedCrash}
			client := New(failure, WithClock(clock.now), WithSleeper(noSleep))

			_, err := client.CreatePool(t.Context(), childPoolSpec("child", 24))
			if !errors.Is(err, errInjectedCrash) {
				t.Fatalf("CreatePool() = %v, want injected failure", err)
			}

			stored := readCooldownReservations(t, base)
			if len(stored.Released) != 0 || len(stored.Allocations) != 0 {
				t.Fatalf("rollback left parent reservations %+v", stored)
			}

			_, err = client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
			assertNoError(t, err)
		})
	}
}

func TestCooldownReleaseUnknownOutcomeKeepsLandedRecord(t *testing.T) {
	client, base, clock := newCooldownClient(t, CooldownStrict)
	allocation, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
	assertNoError(t, err)

	store := &unknownAfterCommitStore{Storage: base}
	releasing := New(store, WithClock(clock.now), WithSleeper(noSleep))

	assertNoError(t, releasing.Release(t.Context(), "root", allocation.ID))
	clock.advance(time.Minute)
	assertNoError(t, releasing.Release(t.Context(), "root", allocation.ID))

	stored := readCooldownReservations(t, base)

	want := clock.now().Add(59 * time.Minute)
	if len(stored.Allocations) != 0 || !stored.Released[allocation.CIDR].Equal(want) || store.puts.Load() != 1 {
		t.Fatalf("landed release = %+v, puts = %d, want expiry %s and one write", stored, store.puts.Load(), want)
	}
}

func TestCooldownReleaseKeepsLaterExistingExpiry(t *testing.T) {
	for _, existingLater := range []bool{false, true} {
		t.Run(map[bool]string{false: "extend", true: "preserve"}[existingLater], func(t *testing.T) {
			client, base, clock := newCooldownClient(t, CooldownStrict)
			allocation, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
			assertNoError(t, err)
			stored := readCooldownReservations(t, base)
			expiry := clock.now().Add(time.Minute)

			want := clock.now().Add(time.Hour)
			if existingLater {
				expiry = clock.now().Add(24 * time.Hour)
				want = expiry
			}

			stored.Released = map[netip.Prefix]time.Time{allocation.CIDR: expiry}
			writeCooldownReservations(t, base, stored)

			assertNoError(t, client.Release(t.Context(), "root", allocation.ID))

			if got := readCooldownReservations(t, base).Released[allocation.CIDR]; !got.Equal(want) {
				t.Fatalf("expiry = %s, want %s", got, want)
			}
		})
	}
}

func TestCooldownEveryReservationsWritePrunesExpiredRecords(t *testing.T) {
	for _, operation := range []string{"allocate", "release", "metadata", "child create", "child delete"} {
		for _, mode := range []CooldownMode{CooldownNone, CooldownStrict} {
			t.Run(operation+"/"+string(mode), func(t *testing.T) {
				client, base, clock := newCooldownClient(t, CooldownStrict)
				allocation, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 28})
				assertNoError(t, err)
				child, err := client.CreatePool(t.Context(), childPoolSpec("child", 26))
				assertNoError(t, err)
				stored := readCooldownReservations(t, base)

				stored.Released = map[netip.Prefix]time.Time{
					netip.MustParsePrefix("10.0.0.128/28"): clock.now(),
					netip.MustParsePrefix("10.0.0.144/28"): clock.now().Add(-time.Nanosecond),
				}
				if mode == CooldownNone {
					stored.Cooldown = Cooldown{}
				}

				writeCooldownReservations(t, base, stored)

				switch operation {
				case "allocate":
					_, err = client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 28})
				case "release":
					err = client.Release(t.Context(), "root", allocation.ID)
				case "metadata":
					err = client.UpdateAllocationMetadata(t.Context(), "root", allocation.ID, "updated", nil)
				case "child create":
					_, err = client.CreatePool(t.Context(), childPoolSpec("next", 28))
				case "child delete":
					err = client.DeletePool(t.Context(), child.ID)
				}

				assertNoError(t, err)
				stored = readCooldownReservations(t, base)

				for cidr, expiry := range stored.Released {
					if !expiry.After(clock.now()) {
						t.Fatalf("%s left expired record %s at %s", operation, cidr, expiry)
					}
				}
			})
		}
	}
}

func TestAllocationStrategyMapsInvalidAllocatorArgument(t *testing.T) {
	err := mapAllocatorError("allocate", "root", testAllocationID, alloc.ErrInvalidArgument)

	var opErr *OpError
	if !errors.Is(err, ErrInvalidArgument) || !errors.As(err, &opErr) ||
		opErr.Op != "allocate" || opErr.Pool != "root" || opErr.ID != testAllocationID {
		t.Fatalf("mapAllocatorError() = %v, want allocation ErrInvalidArgument", err)
	}
}

func TestLifecycleCooldownAuditDetectsAndExemptsTransitions(t *testing.T) {
	for _, scenario := range []string{"strict overlap", "explicit", "expired", "none"} {
		t.Run(scenario, func(t *testing.T) {
			client, base, clock := newCooldownClient(t, CooldownStrict)
			allocation, err := client.Allocate(t.Context(), "root", AllocationSpec{PrefixLength: 24})
			assertNoError(t, err)
			assertNoError(t, client.Release(t.Context(), "root", allocation.ID))

			stored := readCooldownReservations(t, base)
			if scenario == "expired" {
				stored.Released[allocation.CIDR] = clock.now()
			}

			if scenario == "none" {
				stored.Cooldown = Cooldown{}
			}

			writeCooldownReservations(t, base, stored)
			audit := &lifecycleCooldownStore{
				Storage: base, snapshots: make(map[lifecycleVersion]reservationsDocument),
			}
			_, version, err := audit.Get(t.Context(), reservationsKey("root"))
			assertNoError(t, err)

			allocation.ID = testAllocationID

			allocation.CreatedAt = clock.now()
			if scenario == "explicit" {
				allocation.Request = Request{Kind: RequestExplicit, CIDR: allocation.CIDR}
			}

			// Bypass Allocate to prove the audit catches a forbidden committed
			// transition, even if a future write prunes its release record.
			stored.Allocations[allocation.ID] = allocation
			stored.Released = nil
			data, err := encodeReservationsDocument(stored)
			assertNoError(t, err)
			_, err = audit.PutIfMatch(t.Context(), reservationsKey("root"), data, version)
			assertNoError(t, err)

			want := 0
			if scenario == "strict overlap" {
				want = 1
			}

			if len(audit.failures) != want {
				t.Fatalf("audit failures = %v, want %d", audit.failures, want)
			}
		})
	}
}
