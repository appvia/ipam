// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package alloc

import (
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"net/netip"
	"testing"
)

func TestProperty(t *testing.T) {
	const seed = uint64(0x4950414d)

	for sequence := range 500 {
		rng := rand.New(rand.NewPCG(seed, uint64(sequence)))
		parents := prefixes("10.42.0.0/16")
		used := make([]netip.Prefix, 0, 12)

		for step := range 12 {
			switch rng.IntN(3) {
			case 0:
				length := 24 + rng.IntN(5)
				want, wantErr := bruteNextIPv4(parents[0], used, length)

				got, err := Next(parents, used, length)
				if !errors.Is(err, wantErr) || (err == nil && got != want) {
					t.Fatalf("seed=%d sequence=%d step=%d Next(/%d)=(%v,%v), want (%v,%v)",
						seed, sequence, step, length, got, err, want, wantErr)
				}

				if err == nil {
					used = append(used, got)
				}
			case 1:
				length := 24 + rng.IntN(5)
				cidr := randomPrefix(rng, parents[0], length)

				err := CheckExplicit(parents, used, cidr)
				if err == nil {
					used = append(used, cidr.Masked())
				} else if !errors.Is(err, ErrOverlap) {
					t.Fatalf("seed=%d sequence=%d step=%d CheckExplicit(%v) error=%v",
						seed, sequence, step, cidr, err)
				}
			case 2:
				if len(used) > 0 {
					index := rng.IntN(len(used))
					used = append(used[:index], used[index+1:]...)
				}
			}

			assertNoOverlaps(t, seed, sequence, step, used)
		}
	}
}

func bruteNextIPv4(parent netip.Prefix, used []netip.Prefix, length int) (netip.Prefix, error) {
	parent = parent.Masked()
	start := binary.BigEndian.Uint32(parent.Addr().AsSlice())
	size := uint32(1) << (32 - length)
	count := uint32(1) << (length - parent.Bits())

	for index := range count {
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], start+index*size)

		candidate := netip.PrefixFrom(netip.AddrFrom4(raw), length)
		if !overlapsAny(candidate, used) {
			return candidate, nil
		}
	}

	return netip.Prefix{}, ErrExhausted
}

func randomPrefix(rng *rand.Rand, parent netip.Prefix, length int) netip.Prefix {
	start := binary.BigEndian.Uint32(parent.Masked().Addr().AsSlice())
	offset := uint32(rng.IntN(1 << (length - parent.Bits())))
	size := uint32(1) << (32 - length)

	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], start+offset*size)

	return netip.PrefixFrom(netip.AddrFrom4(raw), length)
}

func assertNoOverlaps(t *testing.T, seed uint64, sequence, step int, used []netip.Prefix) {
	t.Helper()

	for i, left := range used {
		for j := i + 1; j < len(used); j++ {
			if prefixesOverlap(left, used[j]) {
				t.Fatalf("seed=%d sequence=%d step=%d allocations overlap: %v and %v",
					seed, sequence, step, left, used[j])
			}
		}
	}
}

func overlapsAny(candidate netip.Prefix, used []netip.Prefix) bool {
	for _, existing := range used {
		if prefixesOverlap(candidate, existing) {
			return true
		}
	}

	return false
}

func prefixesOverlap(left, right netip.Prefix) bool {
	left = left.Masked()
	right = right.Masked()

	return left.Addr().BitLen() == right.Addr().BitLen() &&
		(left.Contains(right.Addr()) || right.Contains(left.Addr()))
}

func TestPropertySelect(t *testing.T) {
	const seed = uint64(0x4950414d)

	for _, strategy := range []Strategy{StrategyLowest, StrategyBestFit} {
		for _, mode := range []CooldownMode{CooldownNone, CooldownStrict, CooldownFitAware} {
			for sequence := range 500 {
				rng := rand.New(rand.NewPCG(seed, uint64(sequence)))
				parents := prefixes("10.42.0.0/22")
				used := []netip.Prefix{randomPrefix(rng, parents[0], 24+rng.IntN(5))}
				cooling := []netip.Prefix{randomPrefix(rng, parents[0], 24+rng.IntN(5))}

				for step := range 12 {
					length := 24 + rng.IntN(5)
					in := Input{Parents: parents, Used: used, Cooling: cooling, Length: length, Strategy: strategy, CooldownMode: mode}
					want, wantErr := bruteSelectIPv4(in)
					got, err := Select(in)

					if !errors.Is(err, wantErr) || got != want {
						t.Fatalf("seed=%d strategy=%d mode=%d sequence=%d step=%d Select(/%d)=(%v,%v), want (%v,%v); used=%v cooling=%v",
							seed, strategy, mode, sequence, step, length, got, err, want, wantErr, used, cooling)
					}

					if err == nil && (got != got.Masked() || got.Bits() != length || !parents[0].Contains(got.Addr()) || overlapsAny(got, used)) {
						t.Fatalf("seed=%d strategy=%d mode=%d sequence=%d step=%d invalid allocation %v",
							seed, strategy, mode, sequence, step, got)
					}

					switch rng.IntN(3) {
					case 0:
						if err == nil {
							used = append(used, got)
						}
					case 1:
						if len(used) > 0 {
							index := rng.IntN(len(used))
							cooling = append(cooling, used[index])
							used = append(used[:index], used[index+1:]...)
						}
					case 2:
						cidr := randomPrefix(rng, parents[0], 24+rng.IntN(5))
						if !overlapsAny(cidr, used) {
							used = append(used, cidr)
						}
					}

					if len(cooling) > 0 && rng.IntN(3) == 0 {
						index := rng.IntN(len(cooling))
						cooling = append(cooling[:index], cooling[index+1:]...)
					}

					assertNoOverlaps(t, seed, sequence, step, used)
				}
			}
		}
	}
}

// bruteSelectIPv4 enumerates every aligned request, growing each one to its
// largest unused superprefix instead of decomposing space with netipx.
func bruteSelectIPv4(in Input) (netip.Prefix, error) {
	parent := in.Parents[0].Masked()
	start := binary.BigEndian.Uint32(parent.Addr().AsSlice())
	size := uint32(1) << (32 - in.Length)
	count := uint32(1) << (in.Length - parent.Bits())

	chooseCandidate := func(blocked []netip.Prefix, originalBits int) (netip.Prefix, int) {
		var best netip.Prefix

		bestBits := -1

		for index := range count {
			var raw [4]byte
			binary.BigEndian.PutUint32(raw[:], start+index*size)
			candidate := netip.PrefixFrom(netip.AddrFrom4(raw), in.Length)

			if overlapsAny(candidate, blocked) {
				continue
			}

			if originalBits >= 0 && bruteBlockBitsIPv4(parent, candidate, in.Used) != originalBits {
				continue
			}

			bits := bruteBlockBitsIPv4(parent, candidate, blocked)
			if !best.IsValid() || (in.Strategy == StrategyBestFit && bits > bestBits) {
				best = candidate
				bestBits = bits
			}
		}

		return best, bestBits
	}

	best, bestBits := chooseCandidate(in.Used, -1)
	if !best.IsValid() {
		return netip.Prefix{}, ErrExhausted
	}

	if in.CooldownMode == CooldownNone {
		return best, nil
	}

	blocked := append(append([]netip.Prefix(nil), in.Used...), in.Cooling...)
	originalBits := -1

	if in.CooldownMode == CooldownFitAware {
		originalBits = bestBits
	}

	clean, _ := chooseCandidate(blocked, originalBits)
	if clean.IsValid() {
		return clean, nil
	}

	if in.CooldownMode == CooldownStrict {
		return netip.Prefix{}, ErrCooling
	}

	return best, nil
}

func bruteBlockBitsIPv4(parent, candidate netip.Prefix, blocked []netip.Prefix) int {
	bits := candidate.Bits()

	for bits > parent.Bits() {
		larger := netip.PrefixFrom(candidate.Addr(), bits-1).Masked()
		if overlapsAny(larger, blocked) {
			break
		}

		bits--
	}

	return bits
}
