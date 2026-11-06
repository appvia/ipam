// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestValidateMetadata(t *testing.T) {
	t.Parallel()

	labelsWithCount := func(count int) map[string]string {
		labels := make(map[string]string, count)
		for index := range count {
			labels[fmt.Sprintf("label-%02d", index)] = ""
		}

		return labels
	}

	tooManyInvalidLabels := labelsWithCount(64)
	tooManyInvalidLabels[""] = ""

	tests := []struct {
		name        string
		description string
		labels      map[string]string
		wantReason  string
	}{
		{name: "AC-1 empty description and nil labels"},
		{name: "AC-1 description at byte limit", description: strings.Repeat("d", 1024)},
		{name: "AC-1 description over byte limit", description: strings.Repeat("d", 1025), wantReason: "description exceeds 1024 bytes"},
		{name: "AC-1 multibyte description at byte limit", description: strings.Repeat("ä", 512)},
		{name: "AC-1 multibyte description over byte limit", description: strings.Repeat("ä", 512) + "d", wantReason: "description exceeds 1024 bytes"},
		{name: "AC-1 tab and newline accepted", description: "synthetic\tmetadata\ntext"},
		{name: "AC-1 carriage return rejected", description: "synthetic\rmetadata", wantReason: "description contains a control character"},
		{name: "AC-1 NUL rejected", description: "synthetic\x00metadata", wantReason: "description contains a control character"},
		{name: "AC-1 DEL rejected", description: "synthetic\x7fmetadata", wantReason: "description contains a control character"},
		{name: "AC-1 Unicode control rejected", description: "synthetic\u0085metadata", wantReason: "description contains a control character"},
		{name: "AC-1 invalid UTF-8 rejected", description: "synthetic\xffmetadata", wantReason: "description is not valid UTF-8"},
		{name: "AC-1 byte limit precedes UTF-8 check", description: strings.Repeat("d", 1024) + "\xff", wantReason: "description exceeds 1024 bytes"},
		{name: "AC-1 UTF-8 check precedes control check", description: "\x00\xff", wantReason: "description is not valid UTF-8"},
		{name: "AC-1 description precedes labels", description: "\r", labels: labelsWithCount(65), wantReason: "description contains a control character"},
		{name: "AC-2 empty labels accepted", labels: map[string]string{}},
		{name: "AC-2 labels at count limit", labels: labelsWithCount(64)},
		{name: "AC-2 labels over count limit", labels: labelsWithCount(65), wantReason: "more than 64 labels"},
		{name: "AC-2 label count precedes key check", labels: tooManyInvalidLabels, wantReason: "more than 64 labels"},
		{name: "AC-3 empty key rejected", labels: map[string]string{"": "synthetic-value"}, wantReason: "label key is empty"},
		{name: "AC-3 key at byte limit", labels: map[string]string{strings.Repeat("k", 63): ""}},
		{name: "AC-3 key over byte limit", labels: map[string]string{strings.Repeat("k", 64): ""}, wantReason: "label key exceeds 63 characters"},
		{name: "AC-3 single character key accepted", labels: map[string]string{"a": ""}},
		{name: "AC-3 punctuated key accepted", labels: map[string]string{"a.b-c_d": ""}},
		{name: "AC-3 uppercase and numeric key accepted", labels: map[string]string{"A0": ""}},
		{name: "AC-3 leading hyphen rejected", labels: map[string]string{"-a": ""}, wantReason: "label key has invalid characters"},
		{name: "AC-3 trailing hyphen rejected", labels: map[string]string{"a-": ""}, wantReason: "label key has invalid characters"},
		{name: "AC-3 space in key rejected", labels: map[string]string{"a b": ""}, wantReason: "label key has invalid characters"},
		{name: "AC-3 non-ASCII key rejected", labels: map[string]string{"ä": ""}, wantReason: "label key has invalid characters"},
		{name: "AC-3 key byte limit precedes grammar", labels: map[string]string{strings.Repeat("ä", 32): ""}, wantReason: "label key exceeds 63 characters"},
		{name: "AC-3 value at rune limit", labels: map[string]string{"a": strings.Repeat("v", 63)}},
		{name: "AC-3 value over rune limit", labels: map[string]string{"a": strings.Repeat("v", 64)}, wantReason: "label value exceeds 63 characters"},
		{name: "AC-3 multibyte value at rune limit", labels: map[string]string{"a": strings.Repeat("界", 63)}},
		{name: "AC-3 multibyte value over rune limit", labels: map[string]string{"a": strings.Repeat("界", 64)}, wantReason: "label value exceeds 63 characters"},
		{name: "AC-3 key grammar precedes value limit", labels: map[string]string{"-a": strings.Repeat("v", 64)}, wantReason: "label key has invalid characters"},
		{name: "AC-3 keys checked in sorted order", labels: map[string]string{"z-": "", "a": strings.Repeat("v", 64)}, wantReason: "label value exceeds 63 characters"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := validateMetadata(test.description, test.labels)
			if test.wantReason == "" {
				if err != nil {
					t.Fatalf("validateMetadata() unexpected error: %v", err)
				}

				return
			}

			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("validateMetadata() error = %v, want ErrInvalidArgument", err)
			}

			// Exact fixed text also proves that caller metadata is not echoed.
			if want := test.wantReason + ": " + ErrInvalidArgument.Error(); err.Error() != want {
				t.Fatalf("validateMetadata() error = %q, want %q", err.Error(), want)
			}
		})
	}
}

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
