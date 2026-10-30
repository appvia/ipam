// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package alloc

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

func TestExplicit(t *testing.T) {
	t.Run("accepts a contained unaligned prefix without mutating inputs", func(t *testing.T) {
		parents := prefixes("10.0.0.0/16")
		used := prefixes("10.0.0.0/25")

		parentsBefore := append([]netip.Prefix(nil), parents...)
		usedBefore := append([]netip.Prefix(nil), used...)
		cidr := netip.PrefixFrom(netip.MustParseAddr("10.0.1.129"), 25)

		err := CheckExplicit(parents, used, cidr)
		if err != nil {
			t.Fatalf("CheckExplicit() error = %v", err)
		}

		if !reflect.DeepEqual(parents, parentsBefore) || !reflect.DeepEqual(used, usedBefore) {
			t.Fatalf("CheckExplicit() mutated inputs: parents=%v used=%v", parents, used)
		}
	})

	t.Run("accepts IPv6 in a matching parent", func(t *testing.T) {
		err := CheckExplicit(prefixes("2001:db8::/48"), nil, netip.MustParsePrefix("2001:db8:0:1::/64"))
		if err != nil {
			t.Fatalf("CheckExplicit() error = %v", err)
		}
	})

	t.Run("accepts a prefix in any one of mixed family parents", func(t *testing.T) {
		err := CheckExplicit(
			prefixes("2001:db8::/48", "10.0.0.0/16"),
			prefixes("2001:db8:1::/64"),
			netip.MustParsePrefix("10.0.2.0/24"),
		)
		if err != nil {
			t.Fatalf("CheckExplicit() error = %v", err)
		}
	})

	t.Run("rejects every kind of overlap", func(t *testing.T) {
		cases := []struct {
			name string
			used []netip.Prefix
			cidr netip.Prefix
		}{
			{"exact", prefixes("10.0.1.0/24"), netip.MustParsePrefix("10.0.1.0/24")},
			{"existing contains requested", prefixes("10.0.0.0/16"), netip.MustParsePrefix("10.0.1.0/24")},
			{"requested contains existing", prefixes("10.0.1.128/25"), netip.MustParsePrefix("10.0.1.0/24")},
			{"duplicate overlapping used", prefixes("10.0.1.128/25", "10.0.1.128/25"), netip.MustParsePrefix("10.0.1.0/24")},
			{"unaligned existing", []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("10.0.1.129"), 25)}, netip.MustParsePrefix("10.0.1.128/25")},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := CheckExplicit(prefixes("10.0.0.0/16"), tc.used, tc.cidr)
				if !errors.Is(err, ErrOverlap) {
					t.Fatalf("CheckExplicit() error = %v, want %v", err, ErrOverlap)
				}
			})
		}
	})

	t.Run("rejects ranges not wholly inside one parent", func(t *testing.T) {
		cases := []struct {
			name    string
			parents []netip.Prefix
			cidr    netip.Prefix
		}{
			{"outside", prefixes("10.0.0.0/24"), netip.MustParsePrefix("10.0.1.0/24")},
			{"larger than parent", prefixes("10.0.0.0/24"), netip.MustParsePrefix("10.0.0.0/16")},
			{"cannot span adjacent parents", prefixes("10.0.0.0/25", "10.0.0.128/25"), netip.MustParsePrefix("10.0.0.0/24")},
			{"wrong family", prefixes("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/64")},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := CheckExplicit(tc.parents, nil, tc.cidr)
				if !errors.Is(err, ErrInvalidCIDR) {
					t.Fatalf("CheckExplicit() error = %v, want %v", err, ErrInvalidCIDR)
				}
			})
		}
	})

	t.Run("rejects malformed and mapped inputs", func(t *testing.T) {
		mapped := netip.MustParsePrefix("::ffff:192.0.2.0/120")

		cases := []struct {
			name    string
			parents []netip.Prefix
			used    []netip.Prefix
			cidr    netip.Prefix
		}{
			{"zero cidr", prefixes("10.0.0.0/8"), nil, netip.Prefix{}},
			{"zero parent", []netip.Prefix{{}}, nil, netip.MustParsePrefix("10.0.0.0/24")},
			{"zero used", prefixes("10.0.0.0/8"), []netip.Prefix{{}}, netip.MustParsePrefix("10.0.0.0/24")},
			{"mapped cidr", []netip.Prefix{mapped}, nil, mapped},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := CheckExplicit(tc.parents, tc.used, tc.cidr)
				if !errors.Is(err, ErrInvalidCIDR) {
					t.Fatalf("CheckExplicit() error = %v, want %v", err, ErrInvalidCIDR)
				}
			})
		}
	})

	t.Run("uses the unzoned prefix produced by netip", func(t *testing.T) {
		cidr := netip.PrefixFrom(netip.MustParseAddr("fe80::1%eth0"), 128)
		if cidr.Addr().Zone() != "" {
			t.Fatalf("netip.PrefixFrom() retained zone %q", cidr.Addr().Zone())
		}

		err := CheckExplicit(prefixes("fe80::/64"), nil, cidr)
		if err != nil {
			t.Fatalf("CheckExplicit() error = %v", err)
		}
	})
}
