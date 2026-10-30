// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package alloc

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

func TestNext(t *testing.T) {
	t.Run("allocates the lowest IPv4 prefixes in order", func(t *testing.T) {
		parents := prefixes("10.0.0.0/16")

		first, err := Next(parents, nil, 24)
		assertPrefix(t, first, err, "10.0.0.0/24")

		second, err := Next(parents, []netip.Prefix{first}, 24)
		assertPrefix(t, second, err, "10.0.1.0/24")
	})

	t.Run("chooses the lowest address across unsorted parents", func(t *testing.T) {
		got, err := Next(prefixes("10.2.0.0/16", "10.0.0.0/16"), nil, 24)
		assertPrefix(t, got, err, "10.0.0.0/24")
	})

	t.Run("allocates IPv6 including a 64 from a 48", func(t *testing.T) {
		got, err := Next(prefixes("2001:db8::/48"), nil, 64)
		assertPrefix(t, got, err, "2001:db8::/64")
	})

	t.Run("does not span adjacent parents", func(t *testing.T) {
		_, err := Next(prefixes("10.0.0.0/25", "10.0.0.128/25"), nil, 24)
		if !errors.Is(err, ErrInvalidCIDR) {
			t.Fatalf("Next() error = %v, want %v", err, ErrInvalidCIDR)
		}
	})

	t.Run("skips a partially held candidate and stays aligned", func(t *testing.T) {
		used := []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("10.0.0.129"), 25)}
		got, err := Next(prefixes("10.0.0.0/16"), used, 24)
		assertPrefix(t, got, err, "10.0.1.0/24")
	})

	t.Run("ignores used prefixes from another family", func(t *testing.T) {
		got, err := Next(
			prefixes("2001:db8::/48", "10.0.0.0/16"),
			prefixes("2001:db8::/64"),
			24,
		)
		assertPrefix(t, got, err, "10.0.0.0/24")
	})

	t.Run("supports zero and host-length boundaries", func(t *testing.T) {
		got, err := Next(prefixes("0.0.0.0/0"), nil, 1)
		assertPrefix(t, got, err, "0.0.0.0/1")

		got, err = Next(prefixes("192.0.2.1/32"), nil, 32)
		assertPrefix(t, got, err, "192.0.2.1/32")

		got, err = Next(prefixes("2001:db8::1/128"), nil, 128)
		assertPrefix(t, got, err, "2001:db8::1/128")
	})

	t.Run("handles duplicate overlapping and unsorted used prefixes", func(t *testing.T) {
		got, err := Next(
			prefixes("10.0.0.0/24"),
			prefixes("10.0.0.64/26", "10.0.0.0/25", "10.0.0.0/25"),
			25,
		)
		assertPrefix(t, got, err, "10.0.0.128/25")
	})

	t.Run("a used prefix larger than the parent exhausts it", func(t *testing.T) {
		_, err := Next(prefixes("10.0.1.0/24"), prefixes("10.0.0.0/16"), 25)
		if !errors.Is(err, ErrExhausted) {
			t.Fatalf("Next() error = %v, want %v", err, ErrExhausted)
		}
	})

	t.Run("rejects a requested prefix larger than every parent", func(t *testing.T) {
		_, err := Next(prefixes("10.0.0.0/24"), nil, 16)
		if !errors.Is(err, ErrInvalidCIDR) {
			t.Fatalf("Next() error = %v, want %v", err, ErrInvalidCIDR)
		}
	})

	t.Run("reports exhaustion for a full pool", func(t *testing.T) {
		_, err := Next(prefixes("10.0.0.0/24"), prefixes("10.0.0.0/24"), 24)
		if !errors.Is(err, ErrExhausted) {
			t.Fatalf("Next() error = %v, want %v", err, ErrExhausted)
		}
	})

	t.Run("rejects malformed and mapped inputs", func(t *testing.T) {
		mapped := netip.MustParsePrefix("::ffff:192.0.2.0/120")

		cases := []struct {
			name    string
			parents []netip.Prefix
			used    []netip.Prefix
			length  int
		}{
			{"zero parent", []netip.Prefix{{}}, nil, 24},
			{"zero used", prefixes("10.0.0.0/24"), []netip.Prefix{{}}, 25},
			{"mapped parent", []netip.Prefix{mapped}, nil, 120},
			{"negative length", prefixes("10.0.0.0/24"), nil, -1},
			{"too long", prefixes("2001:db8::/48"), nil, 129},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := Next(tc.parents, tc.used, tc.length)
				if !errors.Is(err, ErrInvalidCIDR) {
					t.Fatalf("Next() error = %v, want %v", err, ErrInvalidCIDR)
				}
			})
		}
	})

	t.Run("uses the unzoned prefix produced by netip", func(t *testing.T) {
		parent := netip.PrefixFrom(netip.MustParseAddr("fe80::1%eth0"), 64)
		if parent.Addr().Zone() != "" {
			t.Fatalf("netip.PrefixFrom() retained zone %q", parent.Addr().Zone())
		}

		got, err := Next([]netip.Prefix{parent}, nil, 128)
		assertPrefix(t, got, err, "fe80::/128")
	})

	t.Run("does not mutate inputs and is deterministic", func(t *testing.T) {
		parents := prefixes("10.2.0.0/16", "10.0.0.0/16")
		used := prefixes("10.0.1.0/24", "2001:db8::/64", "10.0.0.0/25")

		parentsBefore := append([]netip.Prefix(nil), parents...)
		usedBefore := append([]netip.Prefix(nil), used...)

		for range 100 {
			got, err := Next(parents, used, 24)
			assertPrefix(t, got, err, "10.0.2.0/24")
		}

		if !reflect.DeepEqual(parents, parentsBefore) {
			t.Fatalf("Next() mutated parents: got %v, want %v", parents, parentsBefore)
		}

		if !reflect.DeepEqual(used, usedBefore) {
			t.Fatalf("Next() mutated used: got %v, want %v", used, usedBefore)
		}
	})
}
