// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/appvia/ipam/storage/memory"
)

// TestClientChurnPolicies replays one seeded script of 300 mixed /24-/28
// allocate/release operations against a /16 for each policy, on a fake clock
// that advances one minute per operation. After every operation it asserts
// that no two stored allocations overlap; strict cooldown never re-issues a
// CIDR overlapping one released less than the cooldown earlier. After every
// write (successful allocate or release, which prune expired records) it also
// asserts every stored released record is unexpired. A refused strict
// allocation performs no write, so the unexpired check is skipped for it.
func TestClientChurnPolicies(t *testing.T) {
	const (
		operations = 300
		cooldown   = 30 * time.Minute
	)

	policies := []struct {
		name   string
		policy PoolPolicy
	}{
		{"lowest", PoolPolicy{Strategy: StrategyLowest}},
		{"best-fit", PoolPolicy{Strategy: StrategyBestFit}},
		{"best-fit+fit-aware", PoolPolicy{
			Strategy: StrategyBestFit, Cooldown: Cooldown{Mode: CooldownFitAware, Duration: cooldown},
		}},
		{"best-fit+strict", PoolPolicy{
			Strategy: StrategyBestFit, Cooldown: Cooldown{Mode: CooldownStrict, Duration: cooldown},
		}},
	}
	lengths := []int{24, 25, 26, 27, 28}

	for _, test := range policies {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			store := memory.New()
			clock := newCooldownTestClock()
			client := New(store, WithClock(clock.now), WithSleeper(noSleep))

			_, err := client.CreatePool(ctx, PoolSpec{
				ID: "churn", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
				Policy: test.policy,
			})
			assertNoError(t, err)

			type releasedCIDR struct {
				cidr netip.Prefix
				at   time.Time
			}

			var (
				rng                            = rand.New(rand.NewPCG(0x4950414d, 11))
				live                           []Allocation
				released                       []releasedCIDR
				allocs, releases, refusals     int
				failures, reissues, writeCheck int
			)

			for step := range operations {
				clock.advance(time.Minute)

				now := clock.now()
				wrote := false

				if len(live) == 0 || rng.IntN(100) < 55 {
					length := lengths[rng.IntN(len(lengths))]

					allocation, allocErr := client.Allocate(ctx, "churn", AllocationSpec{PrefixLength: length})

					var opErr *OpError

					switch {
					case allocErr == nil:
						allocs++
						wrote = true

						live = append(live, allocation)

						for _, old := range released {
							if !old.cidr.Overlaps(allocation.CIDR) || !old.at.Add(cooldown).After(now) {
								continue
							}

							reissues++

							if test.policy.Cooldown.Mode == CooldownStrict {
								t.Fatalf("step %d: strict re-issued %s released %s ago (< %s)",
									step, allocation.CIDR, now.Sub(old.at), cooldown)
							}

							break
						}
					case errors.As(allocErr, &opErr) && opErr.Reason == "cooldown":
						if test.policy.Cooldown.Mode != CooldownStrict {
							t.Fatalf("step %d: cooldown refusal under %s: %v", step, test.name, allocErr)
						}

						refusals++
					case errors.Is(allocErr, ErrPoolExhausted):
						failures++
					default:
						t.Fatalf("step %d: allocate /%d: %v", step, length, allocErr)
					}
				} else {
					index := rng.IntN(len(live))
					victim := live[index]

					assertNoError(t, client.Release(ctx, "churn", victim.ID))

					live = slices.Delete(live, index, index+1)

					released = append(released, releasedCIDR{victim.CIDR, now})
					releases++
					wrote = true
				}

				data, _, getErr := store.Get(ctx, reservationsKey("churn"))
				assertNoError(t, getErr)

				reservations, decodeErr := decodeReservationsDocument(data, "churn")
				assertNoError(t, decodeErr)

				if len(reservations.Allocations) != len(live) {
					t.Fatalf("step %d: stored %d allocations, tracked %d",
						step, len(reservations.Allocations), len(live))
				}

				stored := make([]netip.Prefix, 0, len(reservations.Allocations))
				for _, allocation := range reservations.Allocations {
					stored = append(stored, allocation.CIDR)
				}

				for i := range stored {
					for j := i + 1; j < len(stored); j++ {
						if stored[i].Overlaps(stored[j]) {
							t.Fatalf("step %d: overlapping allocations %s and %s", step, stored[i], stored[j])
						}
					}
				}

				if wrote {
					writeCheck++

					for cidr, expiry := range reservations.Released {
						if !expiry.After(now) {
							t.Fatalf("step %d: stored released %s expired at %s (now %s)", step, cidr, expiry, now)
						}
					}
				}
			}

			if allocs == 0 || releases == 0 {
				t.Fatalf("degenerate script: %d allocations, %d releases", allocs, releases)
			}

			t.Logf("%s: allocations=%d releases=%d exhausted=%d cooldown_refusals=%d "+
				"reissues_within_cooldown=%d unexpired_checks=%d live_end=%d",
				test.name, allocs, releases, failures, refusals, reissues, writeCheck, len(live))
		})
	}
}
