// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestValidateAcceptsConsistentDocuments(t *testing.T) {
	t.Parallel()

	pool := testPoolDocument()
	reservations := testReservationsDocument()

	if err := validate(pool, reservations); err != nil {
		t.Fatalf("validate() unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidReservations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*poolDocument, *reservationsDocument)
		wantErr    error
		wantReason string
	}{
		{
			name: "incarnation mismatch",
			mutate: func(_ *poolDocument, reservations *reservationsDocument) {
				reservations.Incarnation = "fedcba9876543210fedcba9876543210"
			},
			wantErr:    ErrConflict,
			wantReason: "incarnation_mismatch",
		},
		{
			name: "reservations pool ID mismatch",
			mutate: func(_ *poolDocument, reservations *reservationsDocument) {
				reservations.PoolID = "other"
			},
			wantErr: ErrInvalidArgument,
		},
		{
			name: "allocation pool ID mismatch",
			mutate: func(_ *poolDocument, reservations *reservationsDocument) {
				allocation := reservations.Allocations["018f22e2-1865-7be8-9f45-35d90607d0f7"]
				allocation.PoolID = "other"
				reservations.Allocations[allocation.ID] = allocation
			},
			wantErr: ErrInvalidArgument,
		},
		{
			name: "allocation map key mismatch",
			mutate: func(_ *poolDocument, reservations *reservationsDocument) {
				allocation := reservations.Allocations["018f22e2-1865-7be8-9f45-35d90607d0f7"]
				delete(reservations.Allocations, allocation.ID)
				reservations.Allocations["01900000-0000-7000-8000-000000000000"] = allocation
			},
			wantErr: ErrInvalidArgument,
		},
		{
			name: "allocation outside pool",
			mutate: func(_ *poolDocument, reservations *reservationsDocument) {
				allocation := reservations.Allocations["018f22e2-1865-7be8-9f45-35d90607d0f7"]
				allocation.CIDR = netip.MustParsePrefix("192.0.2.0/24")
				reservations.Allocations[allocation.ID] = allocation
			},
			wantErr: ErrInvalidCIDR,
		},
		{
			name: "allocation family mismatch",
			mutate: func(_ *poolDocument, reservations *reservationsDocument) {
				allocation := reservations.Allocations["018f22e2-1865-7be8-9f45-35d90607d0f7"]
				allocation.CIDR = netip.MustParsePrefix("2001:db8::/64")
				reservations.Allocations[allocation.ID] = allocation
			},
			wantErr: ErrInvalidCIDR,
		},
		{
			name: "allocations overlap",
			mutate: func(_ *poolDocument, reservations *reservationsDocument) {
				reservations.Allocations["01900000-0000-7000-8000-000000000000"] = Allocation{
					ID:     "01900000-0000-7000-8000-000000000000",
					PoolID: "prod-main",
					Request: Request{
						Kind:         RequestNext,
						PrefixLength: 25,
					},
					CIDR:      netip.MustParsePrefix("10.0.0.128/25"),
					CreatedAt: time.Date(2026, time.October, 29, 12, 31, 0, 0, time.UTC),
				}
			},
			wantErr: ErrInvalidCIDR,
		},
		{
			name: "unknown request kind",
			mutate: func(_ *poolDocument, reservations *reservationsDocument) {
				allocation := reservations.Allocations["018f22e2-1865-7be8-9f45-35d90607d0f7"]
				allocation.Request.Kind = "unknown"
				reservations.Allocations[allocation.ID] = allocation
			},
			wantErr: ErrInvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pool := testPoolDocument()
			reservations := testReservationsDocument()
			test.mutate(&pool, &reservations)

			err := validate(pool, reservations)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("validate() error = %v, want %v", err, test.wantErr)
			}

			if test.wantReason == "" {
				return
			}

			var opErr *OpError
			if !errors.As(err, &opErr) {
				t.Fatalf("validate() error type = %T, want *OpError", err)
			}

			if opErr.Reason != test.wantReason {
				t.Fatalf("validate() reason = %q, want %q", opErr.Reason, test.wantReason)
			}
		})
	}
}

func testPoolDocument() poolDocument {
	return poolDocument{
		Pool: Pool{
			ID:     "prod-main",
			Family: 4,
			CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
		},
		Incarnation: "0123456789abcdef0123456789abcdef",
	}
}

func testReservationsDocument() reservationsDocument {
	return reservationsDocument{
		PoolID:      "prod-main",
		Incarnation: "0123456789abcdef0123456789abcdef",
		Allocations: map[string]Allocation{
			"018f22e2-1865-7be8-9f45-35d90607d0f7": {
				ID:     "018f22e2-1865-7be8-9f45-35d90607d0f7",
				PoolID: "prod-main",
				Request: Request{
					Kind:         RequestNext,
					PrefixLength: 24,
				},
				CIDR:      netip.MustParsePrefix("10.0.0.0/24"),
				CreatedAt: time.Date(2026, time.October, 29, 12, 30, 0, 0, time.UTC),
			},
		},
	}
}
