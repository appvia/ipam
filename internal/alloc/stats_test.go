// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package alloc

import (
	"errors"
	"math/big"
	"net/netip"
	"reflect"
	"slices"
	"testing"
)

func TestStats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		parents     []string
		used        []string
		reference   int
		total       int64
		free        int64
		largest     string
		histogram   []BlockCount
		allocatable *int64
		capacity    *int64
		index       *int64
	}{
		{
			name: "empty /24 ref 26", parents: []string{"10.0.0.0/24"}, reference: 26,
			total: 256, free: 256, largest: "10.0.0.0/24",
			histogram:   []BlockCount{{Bits: 24, Count: 1}},
			allocatable: i64(4), capacity: i64(4), index: i64(0),
		},
		{
			name: "/24 used .64/26 ref 25", parents: []string{"10.0.0.0/24"},
			used: []string{"10.0.0.64/26"}, reference: 25,
			total: 256, free: 192, largest: "10.0.0.128/25",
			histogram:   []BlockCount{{Bits: 25, Count: 1}, {Bits: 26, Count: 1}},
			allocatable: i64(1), capacity: i64(1), index: i64(0),
		},
		{
			name: "/24 used .64/26 and .192/26 ref 25 index 1", parents: []string{"10.0.0.0/24"},
			used: []string{"10.0.0.64/26", "10.0.0.192/26"}, reference: 25,
			total: 256, free: 128, largest: "10.0.0.0/26",
			histogram:   []BlockCount{{Bits: 26, Count: 2}},
			allocatable: i64(0), capacity: i64(1), index: i64(1),
		},
		{
			name: "full pool", parents: []string{"10.0.0.0/24"}, used: []string{"10.0.0.0/24"},
			reference: 26, total: 256, free: 0, largest: "",
			allocatable: i64(0), capacity: i64(0),
		},
		{
			name: "no reference", parents: []string{"10.0.0.0/24"}, used: []string{"10.0.0.0/25"},
			total: 256, free: 128, largest: "10.0.0.128/25",
			histogram: []BlockCount{{Bits: 25, Count: 1}},
		},
		{
			name: "adjacent /25 parents never merge", parents: []string{"10.0.0.0/25", "10.0.0.128/25"},
			reference: 25, total: 256, free: 256, largest: "10.0.0.0/25",
			histogram:   []BlockCount{{Bits: 25, Count: 2}},
			allocatable: i64(2), capacity: i64(2), index: i64(0),
		},
		{
			name: "overlapping used is harmless", parents: []string{"10.0.0.0/24"},
			used: []string{"10.0.0.0/25", "10.0.0.64/26", "192.168.0.0/16"}, reference: 25,
			total: 256, free: 128, largest: "10.0.0.128/25",
			histogram:   []BlockCount{{Bits: 25, Count: 1}},
			allocatable: i64(1), capacity: i64(1), index: i64(0),
		},
		{
			name: "unaligned parent is masked", parents: []string{"10.0.0.77/24"},
			total: 256, free: 256, largest: "10.0.0.0/24",
			histogram: []BlockCount{{Bits: 24, Count: 1}},
		},
		{
			name: "largest tie lowest address", parents: []string{"10.0.1.0/24", "10.0.0.0/24"},
			total: 512, free: 512, largest: "10.0.0.0/24",
			histogram: []BlockCount{{Bits: 24, Count: 2}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			parents, used := prefixes(test.parents...), prefixes(test.used...)
			parentsBefore, usedBefore := slices.Clone(parents), slices.Clone(used)

			got, err := ComputeStats(parents, used, test.reference)
			if err != nil {
				t.Fatalf("ComputeStats() error = %v", err)
			}

			if !slices.Equal(parents, parentsBefore) || !slices.Equal(used, usedBefore) {
				t.Fatal("ComputeStats() mutated its inputs")
			}

			assertBig(t, "Total", got.Total, big.NewInt(test.total))
			assertBig(t, "Free", got.Free, big.NewInt(test.free))

			var wantLargest netip.Prefix
			if test.largest != "" {
				wantLargest = netip.MustParsePrefix(test.largest)
			}

			if got.Largest != wantLargest {
				t.Errorf("Largest = %v, want %v", got.Largest, wantLargest)
			}

			if len(test.histogram) != len(got.Histogram) || (len(got.Histogram) > 0 && !reflect.DeepEqual(got.Histogram, test.histogram)) {
				t.Errorf("Histogram = %v, want %v", got.Histogram, test.histogram)
			}

			if got.ReferenceLength != test.reference {
				t.Errorf("ReferenceLength = %d, want %d", got.ReferenceLength, test.reference)
			}

			assertOptionalBig(t, "Allocatable", got.Allocatable, test.allocatable)
			assertOptionalBig(t, "Capacity", got.Capacity, test.capacity)

			switch {
			case test.index == nil && got.Index != nil:
				t.Errorf("Index = %v, want nil", got.Index)
			case test.index != nil && (got.Index == nil || got.Index.Cmp(new(big.Rat).SetInt64(*test.index)) != 0):
				t.Errorf("Index = %v, want %d", got.Index, *test.index)
			}
		})
	}
}

func TestStatsFractionalIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		parents   []string
		used      []string
		reference int
		capacity  int64
		index     *big.Rat
	}{
		{
			name: "one of two whole blocks free", parents: []string{"10.0.0.0/22"},
			used:      []string{"10.0.0.0/25", "10.0.1.0/25", "10.0.2.0/24"},
			reference: 24, capacity: 2, index: big.NewRat(1, 2),
		},
		{
			name: "two of three whole blocks free", parents: []string{"10.0.0.0/22"},
			used:      []string{"10.0.0.0/25", "10.0.1.0/25"},
			reference: 24, capacity: 3, index: big.NewRat(1, 3),
		},
		{
			name: "only halves free", parents: []string{"10.0.0.0/22"},
			used:      []string{"10.0.0.0/25", "10.0.1.0/25", "10.0.2.0/25", "10.0.3.0/25"},
			reference: 24, capacity: 2, index: big.NewRat(1, 1),
		},
		{
			name: "free space already unfragmented", parents: []string{"10.0.0.0/22"},
			used:      []string{"10.0.0.128/25"},
			reference: 24, capacity: 3, index: big.NewRat(0, 1),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ComputeStats(prefixes(test.parents...), prefixes(test.used...), test.reference)
			if err != nil {
				t.Fatalf("ComputeStats() error = %v", err)
			}

			assertBig(t, "Capacity", got.Capacity, big.NewInt(test.capacity))

			if got.Index == nil || got.Index.Cmp(test.index) != 0 {
				t.Errorf("Index = %v, want %v", got.Index, test.index)
			}
		})
	}
}

func TestStatsIPv6Whole(t *testing.T) {
	t.Parallel()

	got, err := ComputeStats(prefixes("::/0"), nil, 64)
	if err != nil {
		t.Fatalf("ComputeStats() error = %v", err)
	}

	want := new(big.Int).Lsh(big.NewInt(1), 128)

	assertBig(t, "Total", got.Total, want)
	assertBig(t, "Free", got.Free, want)
	assertBig(t, "Allocatable", got.Allocatable, new(big.Int).Lsh(big.NewInt(1), 64))
	assertBig(t, "Capacity", got.Capacity, new(big.Int).Lsh(big.NewInt(1), 64))

	if got.Largest != netip.MustParsePrefix("::/0") {
		t.Errorf("Largest = %v, want ::/0", got.Largest)
	}
}

func TestStatsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		parents   []netip.Prefix
		used      []netip.Prefix
		reference int
		want      error
	}{
		{name: "reference 8 on /16 is invalid argument", parents: prefixes("10.0.0.0/16"), reference: 8, want: ErrInvalidArgument},
		{name: "reference 33 on IPv4 is invalid argument", parents: prefixes("10.0.0.0/16"), reference: 33, want: ErrInvalidArgument},
		{name: "negative reference is invalid argument", parents: prefixes("10.0.0.0/16"), reference: -1, want: ErrInvalidArgument},
		{name: "reference without parents is invalid argument", reference: 24, want: ErrInvalidArgument},
		{name: "mixed families with reference is invalid argument", parents: prefixes("10.0.0.0/16", "2001:db8::/32"), reference: 24, want: ErrInvalidArgument},
		{name: "zero prefix parent is invalid CIDR", parents: []netip.Prefix{{}}, want: ErrInvalidCIDR},
		{name: "zero prefix used is invalid CIDR", parents: prefixes("10.0.0.0/16"), used: []netip.Prefix{{}}, want: ErrInvalidCIDR},
		{name: "4in6 parent is invalid CIDR", parents: prefixes("::ffff:10.0.0.0/104"), want: ErrInvalidCIDR},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := ComputeStats(test.parents, test.used, test.reference)
			if !errors.Is(err, test.want) {
				t.Fatalf("ComputeStats() error = %v, want %v", err, test.want)
			}
		})
	}
}

func i64(value int64) *int64 { return &value }

func assertBig(t *testing.T, name string, got, want *big.Int) {
	t.Helper()

	if got == nil || got.Cmp(want) != 0 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func assertOptionalBig(t *testing.T, name string, got *big.Int, want *int64) {
	t.Helper()

	if want == nil {
		if got != nil {
			t.Errorf("%s = %v, want nil", name, got)
		}

		return
	}

	assertBig(t, name, got, big.NewInt(*want))
}
