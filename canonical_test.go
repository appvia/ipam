// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCanonicalPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "masks IPv4 host bits", input: "10.0.0.5/16", want: "10.0.0.0/16"},
		{name: "converts mapped IPv4", input: "::ffff:10.0.0.0/104", want: "10.0.0.0/8"},
		{name: "accepts IPv4 zero prefix", input: "203.0.113.9/0", want: "0.0.0.0/0"},
		{name: "accepts IPv6 host prefix", input: "2001:db8::1/128", want: "2001:db8::1/128"},
		{name: "rejects mapped prefix shorter than 96", input: "::ffff:10.0.0.0/95", wantErr: ErrInvalidCIDR},
		{name: "rejects zoned address", input: "fe80::1%en0/64", wantErr: ErrInvalidCIDR},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := canonicalPrefix(test.input)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("canonicalPrefix() error = %v, want %v", err, test.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("canonicalPrefix() unexpected error: %v", err)
			}

			if got.String() != test.want {
				t.Fatalf("canonicalPrefix() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCanonicalPool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pool    Pool
		want    []string
		wantErr error
	}{
		{
			name: "sorts disjoint prefixes",
			pool: Pool{
				ID:    "prod-main",
				CIDRs: []netip.Prefix{netip.MustParsePrefix("10.2.0.0/16"), netip.MustParsePrefix("10.0.0.5/16")},
			},
			want: []string{"10.0.0.0/16", "10.2.0.0/16"},
		},
		{
			name: "allows adjacent prefixes",
			pool: Pool{
				ID:    "prod-main",
				CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/25"), netip.MustParsePrefix("10.0.0.128/25")},
			},
			want: []string{"10.0.0.0/25", "10.0.0.128/25"},
		},
		{
			name:    "rejects empty pool",
			pool:    Pool{ID: "prod-main"},
			wantErr: ErrInvalidCIDR,
		},
		{
			name: "rejects mixed families",
			pool: Pool{
				ID:    "prod-main",
				CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")},
			},
			wantErr: ErrInvalidCIDR,
		},
		{
			name: "rejects overlap",
			pool: Pool{
				ID:    "prod-main",
				CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("10.1.0.0/16")},
			},
			wantErr: ErrInvalidCIDR,
		},
		{
			name: "rejects duplicate",
			pool: Pool{
				ID:    "prod-main",
				CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8")},
			},
			wantErr: ErrInvalidCIDR,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := canonicalPool(test.pool)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("canonicalPool() error = %v, want %v", err, test.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("canonicalPool() unexpected error: %v", err)
			}

			if got.Family != 4 {
				t.Fatalf("canonicalPool() family = %d, want 4", got.Family)
			}

			gotCIDRs := make([]string, len(got.CIDRs))
			for index, prefix := range got.CIDRs {
				gotCIDRs[index] = prefix.String()
			}

			if strings.Join(gotCIDRs, ",") != strings.Join(test.want, ",") {
				t.Fatalf("canonicalPool() CIDRs = %v, want %v", gotCIDRs, test.want)
			}
		})
	}
}

func TestCanonicalIDs(t *testing.T) {
	t.Parallel()

	generated := NewAllocationID()

	parsed, err := uuid.Parse(generated)
	if err != nil {
		t.Fatalf("NewAllocationID() returned invalid UUID %q: %v", generated, err)
	}

	if parsed.Version() != 7 {
		t.Fatalf("NewAllocationID() version = %d, want 7", parsed.Version())
	}

	if generated != strings.ToLower(generated) {
		t.Fatalf("NewAllocationID() = %q, want lowercase", generated)
	}

	tests := []struct {
		name    string
		id      string
		wantErr error
	}{
		{name: "accepts generated UUID", id: generated},
		{
			name: "accepts incarnation keyed child pool reservation",
			id:   "pool:child-1:0123456789abcdef0123456789abcdef",
		},
		{name: "rejects caller chosen name", id: "vnet-main", wantErr: ErrInvalidArgument},
		{name: "rejects uppercase UUID", id: strings.ToUpper(generated), wantErr: ErrInvalidArgument},
		{name: "rejects bare child pool reservation", id: "pool:child-1", wantErr: ErrInvalidArgument},
		{
			name:    "rejects malformed child pool",
			id:      "pool:Child:0123456789abcdef0123456789abcdef",
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "rejects short incarnation",
			id:      "pool:child-1:0123456789abcdef",
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "rejects long incarnation",
			id:      "pool:child-1:0123456789abcdef0123456789abcdef00",
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "rejects uppercase incarnation",
			id:      "pool:child-1:0123456789ABCDEF0123456789ABCDEF",
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "rejects non-hex incarnation",
			id:      "pool:child-1:0123456789abcdef0123456789abcdeg",
			wantErr: ErrInvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := validateAllocationID(test.id)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("validateAllocationID() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
