// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package alloc

import (
	"errors"
	"math/rand/v2"
	"net/netip"
	"testing"

	"go4.org/netipx"
)

const (
	simSeed        = uint64(0x4950414d)
	simScenarios   = 16
	simOperations  = 512
	simSampleEvery = 32
	simBand        = 0.025
	simCooldown    = 64
)

var (
	simParent  = netip.MustParsePrefix("10.0.0.0/16")
	simTargets = [...]float64{0.50, 0.65, 0.75, 0.85}
	simLengths = [...]int{20, 21, 22, 23, 24, 24, 25, 25, 26, 26, 27, 27, 28, 28, 28, 28}
)

// simOp allocates a prefix of length for logical allocation logical, or
// releases logical allocation logical.
type simOp struct {
	allocate bool
	length   int
	logical  int
}

type simScript struct {
	ops         []simOp
	allocations int
}

type simConfig struct {
	strategy Strategy
	mode     CooldownMode
	window   int
}

type simTotals struct {
	failures      int
	fragmentation int
	cooling       int
	allocatable   int64
	moved         int
	reused        int
}

type simRelease struct {
	prefix netip.Prefix
	step   int
}

func TestSimulationBestFitNotWorseThanLowest(t *testing.T) {
	var lowest, bestFit simTotals

	for scenario := range simScenarios {
		script := newSimScript(t, scenario)
		lowest.add(replaySimulation(t, scenario, script, simConfig{strategy: StrategyLowest}))
		bestFit.add(replaySimulation(t, scenario, script, simConfig{strategy: StrategyBestFit}))
	}

	t.Logf("totals lowest: failures=%d fragmentation_failures=%d allocatable24=%d",
		lowest.failures, lowest.fragmentation, lowest.allocatable)
	t.Logf("totals best-fit: failures=%d fragmentation_failures=%d allocatable24=%d",
		bestFit.failures, bestFit.fragmentation, bestFit.allocatable)

	if bestFit.fragmentation > lowest.fragmentation {
		t.Fatalf("best-fit fragmentation failures %d > lowest %d", bestFit.fragmentation, lowest.fragmentation)
	}

	if bestFit.allocatable < lowest.allocatable {
		t.Fatalf("best-fit allocatable /24 %d < lowest %d", bestFit.allocatable, lowest.allocatable)
	}
}

func TestSimulationCooldownModes(t *testing.T) {
	names := map[CooldownMode]string{CooldownNone: "none", CooldownStrict: "strict", CooldownFitAware: "fit-aware"}

	for _, strategy := range []Strategy{StrategyLowest, StrategyBestFit} {
		for _, mode := range []CooldownMode{CooldownNone, CooldownStrict, CooldownFitAware} {
			var totals simTotals

			for scenario := range simScenarios {
				script := newSimScript(t, scenario)
				cfg := simConfig{strategy: strategy, mode: mode, window: simCooldown}
				totals.add(replaySimulation(t, scenario, script, cfg))
			}

			t.Logf("totals strategy=%d mode=%s: failures=%d fragmentation_failures=%d cooling_refusals=%d "+
				"moved_off_cooling=%d reused_cooling=%d allocatable24=%d",
				strategy, names[mode], totals.failures, totals.fragmentation, totals.cooling,
				totals.moved, totals.reused, totals.allocatable)
		}
	}
}

// newSimScript generates the one operation script shared by every strategy in
// a scenario. Utilisation is driven by a strategy-neutral reference replay that
// treats the parent as perfectly packed: an allocation is only emitted while
// the reference has room for it, and a release always names a logical
// allocation live in the reference. A strategy's live allocations are
// therefore always a subset of the reference's, so it always has at least as
// many free addresses and every refusal is fragmentation (or cooldown). A
// release of a logical allocation that a strategy failed to allocate is a
// no-op for that strategy.
func newSimScript(t *testing.T, scenario int) simScript {
	t.Helper()

	rng := rand.New(rand.NewPCG(simSeed, uint64(scenario)))
	capacity := 1 << (32 - simParent.Bits())
	phase := simOperations / len(simTargets)
	sizes := map[int]int{}
	live := []int{}
	used := 0
	script := simScript{ops: make([]simOp, 0, simOperations)}

	for step := range simOperations {
		target := simTargets[(step/phase)%len(simTargets)]
		utilisation := float64(used) / float64(capacity)
		allocate := utilisation < target-simBand ||
			(utilisation <= target+simBand && rng.IntN(2) == 0)

		if len(live) == 0 {
			allocate = true
		}

		if !allocate {
			index := rng.IntN(len(live))
			logical := live[index]
			live = append(live[:index], live[index+1:]...)
			used -= sizes[logical]
			script.ops = append(script.ops, simOp{logical: logical})

			continue
		}

		length := simLengths[rng.IntN(len(simLengths))]
		size := 1 << (32 - length)

		if used+size > capacity {
			t.Fatalf("seed=%d scenario=%d step=%d reference cannot fit /%d at %d/%d used",
				simSeed, scenario, step, length, used, capacity)
		}

		logical := script.allocations
		script.allocations++
		sizes[logical] = size
		live = append(live, logical)
		used += size

		script.ops = append(script.ops, simOp{allocate: true, length: length, logical: logical})
	}

	return script
}

func replaySimulation(t *testing.T, scenario int, script simScript, cfg simConfig) simTotals {
	t.Helper()

	parents := []netip.Prefix{simParent}
	held := make([]netip.Prefix, script.allocations)
	released := []simRelease{}

	var totals simTotals

	for step, op := range script.ops {
		used := heldPrefixes(held)

		switch {
		case op.allocate:
			in := Input{
				Parents:      parents,
				Used:         used,
				Cooling:      coolingPrefixes(released, step, cfg.window),
				Length:       op.length,
				Strategy:     cfg.strategy,
				CooldownMode: cfg.mode,
			}

			got, err := Select(in)

			switch {
			case err == nil:
				checkSimChoice(t, scenario, step, in, got, &totals)
				held[op.logical] = got
			case errors.Is(err, ErrCooling) && cfg.mode == CooldownStrict:
				totals.cooling++
			case errors.Is(err, ErrExhausted):
				totals.failures++

				if freeAddresses(used) >= 1<<(32-op.length) {
					totals.fragmentation++
				}
			default:
				t.Fatalf("seed=%d scenario=%d step=%d Select(/%d) error = %v", simSeed, scenario, step, op.length, err)
			}
		case held[op.logical].IsValid():
			released = append(released, simRelease{prefix: held[op.logical], step: step})
			held[op.logical] = netip.Prefix{}
		}

		current := heldPrefixes(held)
		assertSimInvariants(t, scenario, step, current)

		if (step+1)%simSampleEvery == 0 {
			stats, err := ComputeStats(parents, current, 24)
			if err != nil {
				t.Fatalf("seed=%d scenario=%d step=%d ComputeStats error = %v", simSeed, scenario, step, err)
			}

			totals.allocatable += stats.Allocatable.Int64()
		}
	}

	return totals
}

func checkSimChoice(t *testing.T, scenario, step int, in Input, got netip.Prefix, totals *simTotals) {
	t.Helper()

	if got.Bits() != in.Length || got != got.Masked() || !simParent.Contains(got.Addr()) {
		t.Fatalf("seed=%d scenario=%d step=%d Select(/%d) = %v is not an aligned /%d in %v",
			simSeed, scenario, step, in.Length, got, in.Length, simParent)
	}

	if overlapsAny(got, in.Used) {
		t.Fatalf("seed=%d scenario=%d step=%d Select(/%d) = %v overlaps an allocation",
			simSeed, scenario, step, in.Length, got)
	}

	switch in.CooldownMode {
	case CooldownNone:
	case CooldownStrict:
		if overlapsAny(got, in.Cooling) {
			t.Fatalf("seed=%d scenario=%d step=%d strict Select(/%d) = %v overlaps a cooling prefix",
				simSeed, scenario, step, in.Length, got)
		}
	case CooldownFitAware:
		base := in
		base.CooldownMode = CooldownNone

		want, err := Select(base)
		if err != nil {
			t.Fatalf("seed=%d scenario=%d step=%d no-cooldown Select(/%d) error = %v",
				simSeed, scenario, step, in.Length, err)
		}

		gotBlock := containingFreeBlock(t, scenario, step, in.Used, got)
		wantBlock := containingFreeBlock(t, scenario, step, in.Used, want)

		if gotBlock.Bits() != wantBlock.Bits() {
			t.Fatalf("seed=%d scenario=%d step=%d fit-aware chose %v in block %v, no-cooldown chose %v in block %v",
				simSeed, scenario, step, got, gotBlock, want, wantBlock)
		}

		if got != want {
			totals.moved++
		}

		if overlapsAny(got, in.Cooling) {
			totals.reused++
		}
	}
}

func containingFreeBlock(t *testing.T, scenario, step int, used []netip.Prefix, prefix netip.Prefix) netip.Prefix {
	t.Helper()

	var builder netipx.IPSetBuilder
	builder.AddPrefix(simParent)

	for _, existing := range used {
		builder.RemovePrefix(existing)
	}

	free, err := builder.IPSet()
	if err != nil {
		t.Fatalf("seed=%d scenario=%d step=%d free space error = %v", simSeed, scenario, step, err)
	}

	for _, block := range free.Prefixes() {
		if block.Bits() <= prefix.Bits() && block.Contains(prefix.Addr()) {
			return block
		}
	}

	t.Fatalf("seed=%d scenario=%d step=%d %v is not inside one free block", simSeed, scenario, step, prefix)

	return netip.Prefix{}
}

func assertSimInvariants(t *testing.T, scenario, step int, used []netip.Prefix) {
	t.Helper()

	for _, prefix := range used {
		if prefix != prefix.Masked() || prefix.Bits() < simParent.Bits() || !simParent.Contains(prefix.Addr()) {
			t.Fatalf("seed=%d scenario=%d step=%d allocation %v is not an aligned prefix in %v",
				simSeed, scenario, step, prefix, simParent)
		}
	}

	for i, left := range used {
		for _, right := range used[i+1:] {
			if prefixesOverlap(left, right) {
				t.Fatalf("seed=%d scenario=%d step=%d allocations overlap: %v and %v",
					simSeed, scenario, step, left, right)
			}
		}
	}
}

func heldPrefixes(held []netip.Prefix) []netip.Prefix {
	used := make([]netip.Prefix, 0, len(held))

	for _, prefix := range held {
		if prefix.IsValid() {
			used = append(used, prefix)
		}
	}

	return used
}

// coolingPrefixes returns prefixes released within the last window operations.
func coolingPrefixes(released []simRelease, step, window int) []netip.Prefix {
	var cooling []netip.Prefix

	for _, release := range released {
		if step-release.step <= window {
			cooling = append(cooling, release.prefix)
		}
	}

	return cooling
}

func freeAddresses(used []netip.Prefix) int {
	free := 1 << (32 - simParent.Bits())

	for _, prefix := range used {
		free -= 1 << (32 - prefix.Bits())
	}

	return free
}

func (s *simTotals) add(other simTotals) {
	s.failures += other.failures
	s.fragmentation += other.fragmentation
	s.cooling += other.cooling
	s.allocatable += other.allocatable
	s.moved += other.moved
	s.reused += other.reused
}
