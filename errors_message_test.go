// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/appvia/ipam/storage/azblob"
	"github.com/appvia/ipam/storage/memory"
)

func TestOpErrorDoesNotRepeatPool(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  *OpError
		want string
	}{
		"create pool": {
			err:  &OpError{Op: "create pool", Pool: "X", Err: ErrConflict},
			want: "create pool X: ipam: conflict",
		},
		"delete pool with reason": {
			err:  &OpError{Op: "delete pool", Pool: "prod", Reason: "pool_not_empty", Err: ErrConflict},
			want: "delete pool prod (pool_not_empty): ipam: conflict",
		},
		"get pool": {
			err:  &OpError{Op: "get pool", Pool: "prod", Err: ErrNotFound},
			want: "get pool prod: ipam: not found",
		},
		"allocate keeps the pool label": {
			err:  &OpError{Op: "allocate", Pool: "prod", ID: "a1", Err: ErrPoolExhausted},
			want: "allocate pool prod allocation a1: ipam: pool exhausted",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}

			if !errors.Is(tc.err, tc.err.Err) {
				t.Fatalf("errors.Is lost the sentinel %v", tc.err.Err)
			}
		})
	}
}

func TestOpenStorageAzblobErrorIsNotDoubled(t *testing.T) {
	t.Setenv("AZURE_STORAGE_CONNECTION_STRING", "")

	_, err := OpenStorage(t.Context(), "azblob://mycontainer/prefix")
	if err == nil {
		t.Fatal("OpenStorage succeeded without a credential")
	}

	if !errors.Is(err, ErrInvalidArgument) || !errors.Is(err, azblob.ErrInvalidArgument) {
		t.Fatalf("err %v lost ErrInvalidArgument or azblob.ErrInvalidArgument", err)
	}

	msg := err.Error()
	if strings.Count(msg, "invalid argument") != 1 {
		t.Fatalf("message %q repeats \"invalid argument\"", msg)
	}

	if !strings.HasPrefix(msg, "ipam: invalid argument: azblob: ") {
		t.Fatalf("message %q does not read \"ipam: invalid argument: azblob: ...\"", msg)
	}
}

func TestInvalidCIDRCarriesReason(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())
	ctx := t.Context()

	for _, spec := range []PoolSpec{
		{ID: "prod", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")}},
		{
			ID:    "multi",
			CIDRs: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/20"), netip.MustParsePrefix("10.0.0.0/16")},
		},
		{
			ID:    "v6multi",
			CIDRs: []netip.Prefix{netip.MustParsePrefix("fd00::/48"), netip.MustParsePrefix("fd01::/40")},
		},
	} {
		if _, err := client.CreatePool(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}

	tests := map[string]struct {
		pool string
		spec AllocationSpec
		want string
	}{
		"outside the pool": {
			pool: "prod",
			spec: AllocationSpec{CIDR: netip.MustParsePrefix("10.9.0.0/24")},
			want: "cidr 10.9.0.0/24 is outside pool prod (10.0.0.0/16)",
		},
		"prefix shorter than the pool": {
			pool: "prod",
			spec: AllocationSpec{PrefixLength: 8},
			want: "prefix length 8 is shorter than pool prod (/16)",
		},
		"prefix longer than the address": {
			pool: "prod",
			spec: AllocationSpec{PrefixLength: 33},
			want: "prefix length 33 is longer than the /32 addresses of pool prod",
		},
		"multi-CIDR shorter names the largest block": {
			pool: "multi",
			spec: AllocationSpec{PrefixLength: 12},
			want: "prefix length 12 is shorter than the largest block of pool multi (/16)",
		},
		"multi-CIDR outside lists every CIDR": {
			pool: "multi",
			spec: AllocationSpec{CIDR: netip.MustParsePrefix("10.2.0.0/24")},
			want: "cidr 10.2.0.0/24 is outside pool multi (10.0.0.0/16, 10.1.0.0/20)",
		},
		"multi-CIDR longer": {
			pool: "multi",
			spec: AllocationSpec{PrefixLength: 33},
			want: "prefix length 33 is longer than the /32 addresses of pool multi",
		},
		"IPv6 multi-CIDR shorter names the largest block": {
			pool: "v6multi",
			spec: AllocationSpec{PrefixLength: 32},
			want: "prefix length 32 is shorter than the largest block of pool v6multi (/40)",
		},
		"IPv4 CIDR on an IPv6 pool is outside": {
			pool: "v6multi",
			spec: AllocationSpec{CIDR: netip.MustParsePrefix("10.0.0.0/24")},
			want: "cidr 10.0.0.0/24 is outside pool v6multi (fd00::/48, fd01::/40)",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := client.Allocate(ctx, tc.pool, tc.spec)
			if !errors.Is(err, ErrInvalidCIDR) {
				t.Fatalf("err = %v, want ErrInvalidCIDR", err)
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("message %q lacks %q", err.Error(), tc.want)
			}
		})
	}
}

func TestInvalidCIDRReasonOmitsPoolMetadata(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())
	ctx := t.Context()

	if _, err := client.CreatePool(ctx, PoolSpec{
		ID:          "tagged",
		CIDRs:       []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
		Description: "DESCSECRET",
		Labels:      map[string]string{"owner": "LABELSECRET"},
	}); err != nil {
		t.Fatal(err)
	}

	for _, spec := range []AllocationSpec{
		{CIDR: netip.MustParsePrefix("11.0.0.0/24")},
		{PrefixLength: 8},
		{PrefixLength: 33},
	} {
		_, err := client.Allocate(ctx, "tagged", spec)
		if !errors.Is(err, ErrInvalidCIDR) {
			t.Fatalf("err = %v, want ErrInvalidCIDR", err)
		}

		for _, leak := range []string{"owner", "LABELSECRET", "DESCSECRET"} {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("message %q leaks pool metadata %q", err.Error(), leak)
			}
		}
	}
}
