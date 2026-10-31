// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package alloc

import (
	"errors"
	"net/netip"
	"slices"
	"testing"
)

func TestSelect(t *testing.T) {
	fragmented := prefixes("10.0.0.128/26", "10.0.0.224/27")
	equalHoles := prefixes("10.0.0.64/26", "10.0.0.192/26")
	mapped := prefixes("::ffff:192.0.2.0/120")
	invalid := []netip.Prefix{{}}

	cases := []struct {
		name    string
		in      Input
		want    string
		wantErr error
	}{
		{
			name: "lowest chooses the lowest address",
			in:   Input{Parents: prefixes("10.0.0.0/24"), Used: fragmented, Length: 28},
			want: "10.0.0.0/28",
		},
		{
			name: "best fit chooses the tightest hole",
			in:   Input{Parents: prefixes("10.0.0.0/24"), Used: fragmented, Length: 28, Strategy: StrategyBestFit},
			want: "10.0.0.192/28",
		},
		{
			name: "best fit ranks blocks across parents",
			in:   Input{Parents: prefixes("10.0.0.0/24", "10.0.2.0/26"), Length: 28, Strategy: StrategyBestFit},
			want: "10.0.2.0/28",
		},
		{
			name: "best fit breaks ties by address",
			in:   Input{Parents: prefixes("10.0.2.0/26", "10.0.0.0/26"), Length: 28, Strategy: StrategyBestFit},
			want: "10.0.0.0/28",
		},
		{
			name: "adjacent parents remain distinct blocks",
			in:   Input{Parents: prefixes("10.0.0.0/25", "10.0.0.128/25", "10.0.2.0/24"), Length: 26, Strategy: StrategyBestFit},
			want: "10.0.0.0/26",
		},
		{
			name: "none ignores valid cooling prefixes",
			in:   Input{Parents: prefixes("10.0.0.0/24"), Cooling: prefixes("10.0.0.0/24"), Length: 28},
			want: "10.0.0.0/28",
		},
		{
			name: "strict chooses non cooling space",
			in:   Input{Parents: prefixes("10.0.0.0/23"), Cooling: prefixes("10.0.0.0/24"), Length: 25, CooldownMode: CooldownStrict},
			want: "10.0.1.0/25",
		},
		{
			name: "strict best fit ranks newly split blocks",
			in:   Input{Parents: prefixes("10.0.0.0/23"), Cooling: prefixes("10.0.0.0/25"), Length: 26, Strategy: StrategyBestFit, CooldownMode: CooldownStrict},
			want: "10.0.0.128/26",
		},
		{
			name:    "strict reports only cooling space fits",
			in:      Input{Parents: prefixes("10.0.0.0/24"), Cooling: prefixes("10.0.0.0/25", "10.0.0.128/25"), Length: 26, CooldownMode: CooldownStrict},
			wantErr: ErrCooling,
		},
		{
			name: "fit aware keeps cooling choice over looser clean space",
			in:   Input{Parents: prefixes("10.0.0.0/24"), Used: fragmented, Cooling: prefixes("10.0.0.192/27"), Length: 28, Strategy: StrategyBestFit, CooldownMode: CooldownFitAware},
			want: "10.0.0.192/28",
		},
		{
			name: "fit aware chooses equally tight clean space",
			in:   Input{Parents: prefixes("10.0.0.0/24"), Used: equalHoles, Cooling: prefixes("10.0.0.0/26"), Length: 28, Strategy: StrategyBestFit, CooldownMode: CooldownFitAware},
			want: "10.0.0.128/28",
		},
		{
			name: "fit aware ranks clean pieces by their new size",
			in:   Input{Parents: prefixes("10.0.0.0/24"), Used: equalHoles, Cooling: prefixes("10.0.0.0/28"), Length: 28, Strategy: StrategyBestFit, CooldownMode: CooldownFitAware},
			want: "10.0.0.16/28",
		},
		{
			name: "fit aware lowest requires equal original block size",
			in:   Input{Parents: prefixes("10.0.0.0/24"), Used: fragmented, Cooling: prefixes("10.0.0.0/25"), Length: 28, CooldownMode: CooldownFitAware},
			want: "10.0.0.0/28",
		},
		{
			name: "fit aware falls back when all space is cooling",
			in:   Input{Parents: prefixes("10.0.0.0/24"), Cooling: prefixes("10.0.0.0/24"), Length: 28, CooldownMode: CooldownFitAware},
			want: "10.0.0.0/28",
		},
		{
			name: "IPv6 48 to 64",
			in:   Input{Parents: prefixes("2001:db8::/48"), Length: 64, Strategy: StrategyBestFit},
			want: "2001:db8::/64",
		},
		{
			name: "IPv6 strict 128",
			in:   Input{Parents: prefixes("2001:db8::/126"), Cooling: prefixes("2001:db8::/127"), Length: 128, CooldownMode: CooldownStrict},
			want: "2001:db8::2/128",
		},
		{
			name: "IPv6 host parent",
			in:   Input{Parents: prefixes("2001:db8::1/128"), Length: 128, Strategy: StrategyBestFit},
			want: "2001:db8::1/128",
		},
		{
			name: "IPv4 zero length",
			in:   Input{Parents: prefixes("0.0.0.0/0"), Length: 0, Strategy: StrategyBestFit},
			want: "0.0.0.0/0",
		},
		{
			name: "mixed families ignore unrelated used and cooling",
			in:   Input{Parents: prefixes("2001:db8::/48", "10.0.0.0/24"), Used: prefixes("2001:db8::/64"), Cooling: prefixes("2001:db8::/48"), Length: 28, CooldownMode: CooldownStrict},
			want: "10.0.0.0/28",
		},
		{name: "unknown strategy", in: Input{Parents: prefixes("10.0.0.0/24"), Length: 28, Strategy: Strategy(255)}, wantErr: ErrInvalidArgument},
		{name: "unknown cooldown mode", in: Input{Parents: prefixes("10.0.0.0/24"), Length: 28, CooldownMode: CooldownMode(255)}, wantErr: ErrInvalidArgument},
		{name: "invalid parent", in: Input{Parents: invalid, Length: 28}, wantErr: ErrInvalidCIDR},
		{name: "invalid used", in: Input{Parents: prefixes("10.0.0.0/24"), Used: invalid, Length: 28}, wantErr: ErrInvalidCIDR},
		{name: "invalid cooling even with none", in: Input{Parents: prefixes("10.0.0.0/24"), Cooling: invalid, Length: 28}, wantErr: ErrInvalidCIDR},
		{name: "mapped parent", in: Input{Parents: mapped, Length: 128}, wantErr: ErrInvalidCIDR},
		{name: "mapped used", in: Input{Parents: prefixes("10.0.0.0/24"), Used: mapped, Length: 28}, wantErr: ErrInvalidCIDR},
		{name: "mapped cooling", in: Input{Parents: prefixes("10.0.0.0/24"), Cooling: mapped, Length: 28}, wantErr: ErrInvalidCIDR},
		{name: "empty parents", in: Input{Length: 28}, wantErr: ErrInvalidCIDR},
		{name: "negative length", in: Input{Parents: prefixes("10.0.0.0/24"), Length: -1}, wantErr: ErrInvalidCIDR},
		{name: "IPv4 length too long", in: Input{Parents: prefixes("10.0.0.0/24"), Length: 33}, wantErr: ErrInvalidCIDR},
		{name: "IPv6 length too long", in: Input{Parents: prefixes("2001:db8::/48"), Length: 129}, wantErr: ErrInvalidCIDR},
		{name: "cannot span adjacent parents", in: Input{Parents: prefixes("10.0.0.0/25", "10.0.0.128/25"), Length: 24}, wantErr: ErrInvalidCIDR},
	}

	for _, mode := range []CooldownMode{CooldownNone, CooldownStrict, CooldownFitAware} {
		cases = append(cases, struct {
			name    string
			in      Input
			want    string
			wantErr error
		}{
			name:    "exhausted before cooldown",
			in:      Input{Parents: prefixes("10.0.0.0/24"), Used: prefixes("10.0.0.0/25", "10.0.0.128/26"), Cooling: prefixes("10.0.0.0/24"), Length: 25, CooldownMode: mode},
			wantErr: ErrExhausted,
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Select(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Select() error = %v, want %v", err, tc.wantErr)
			}

			if tc.wantErr != nil {
				if got.IsValid() {
					t.Fatalf("Select() = %v with error %v, want invalid prefix", got, err)
				}

				return
			}

			if want := netip.MustParsePrefix(tc.want); got != want {
				t.Fatalf("Select() = %v, want %v", got, want)
			}
		})
	}
}

func TestSelectPermutations(t *testing.T) {
	parents := prefixes("10.0.2.7/24", "10.0.0.9/24")
	used := prefixes("10.0.0.131/26", "10.0.0.225/27", "10.0.0.131/26")
	cooling := prefixes("10.0.0.195/28", "10.0.2.7/25", "10.0.0.199/29")
	parentsBefore := slices.Clone(parents)
	usedBefore := slices.Clone(used)
	coolingBefore := slices.Clone(cooling)

	for _, strategy := range []Strategy{StrategyLowest, StrategyBestFit} {
		for _, mode := range []CooldownMode{CooldownNone, CooldownStrict, CooldownFitAware} {
			in := Input{Parents: parents, Used: used, Cooling: cooling, Length: 28, Strategy: strategy, CooldownMode: mode}
			want, wantErr := Select(in)

			for _, parentOrder := range prefixPermutations(parents) {
				for _, usedOrder := range prefixPermutations(used) {
					for _, coolingOrder := range prefixPermutations(cooling) {
						input := Input{Parents: parentOrder, Used: usedOrder, Cooling: coolingOrder, Length: 28, Strategy: strategy, CooldownMode: mode}
						before := Input{Parents: slices.Clone(parentOrder), Used: slices.Clone(usedOrder), Cooling: slices.Clone(coolingOrder)}

						got, err := Select(input)
						if !errors.Is(err, wantErr) || got != want {
							t.Fatalf("strategy=%d mode=%d Select(%+v)=(%v,%v), want (%v,%v)", strategy, mode, input, got, err, want, wantErr)
						}

						if !slices.Equal(input.Parents, before.Parents) || !slices.Equal(input.Used, before.Used) || !slices.Equal(input.Cooling, before.Cooling) {
							t.Fatalf("Select() mutated inputs: before=%+v after=%+v", before, input)
						}
					}
				}
			}
		}
	}

	if !slices.Equal(parents, parentsBefore) || !slices.Equal(used, usedBefore) || !slices.Equal(cooling, coolingBefore) {
		t.Fatalf("Select() mutated original inputs: parents=%v used=%v cooling=%v", parents, used, cooling)
	}
}

func prefixPermutations(values []netip.Prefix) [][]netip.Prefix {
	if len(values) == 0 {
		return [][]netip.Prefix{nil}
	}

	var result [][]netip.Prefix

	for index, value := range values {
		rest := append(slices.Clone(values[:index]), values[index+1:]...)
		for _, suffix := range prefixPermutations(rest) {
			result = append(result, append([]netip.Prefix{value}, suffix...))
		}
	}

	return result
}
