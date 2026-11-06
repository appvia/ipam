// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/appvia/ipam/storage/memory"
)

func TestMetadataLimitsRejectWrites(t *testing.T) {
	t.Parallel()

	// Exercise all boundaries together, including byte-sized descriptions and
	// ASCII keys versus rune-sized Unicode values.
	description := strings.Repeat("é", 511) + "\t\n"
	labels := make(map[string]string, 64)
	for index := range 64 {
		labels[fmt.Sprintf("k%02d%s", index, strings.Repeat("a", 60))] = strings.Repeat("界", 63)
	}

	tooManyLabels := maps.Clone(labels)
	tooManyLabels["extra"] = "synthetic-extra-value"

	cases := []struct {
		name        string
		description string
		labels      map[string]string
		rejected    string
	}{
		{
			name: "AC-1 over-limit description", description: strings.Repeat("x", 1025),
			rejected: strings.Repeat("x", 1025),
		},
		{
			name: "AC-2 too many labels", labels: tooManyLabels,
			rejected: "synthetic-extra-value",
		},
		{
			name: "AC-3 bad label key", labels: map[string]string{"-bad-key": "synthetic-value"},
			rejected: "-bad-key",
		},
		{
			name: "AC-3 over-limit label value", labels: map[string]string{"key": strings.Repeat("界", 64)},
			rejected: strings.Repeat("界", 64),
		},
		{
			name: "AC-1 AC-2 AC-3 at-limit metadata", description: description, labels: labels,
		},
	}

	for _, operation := range []string{"create pool", "update pool", "allocate", "update allocation", "allocate replay"} {
		t.Run(operation, func(t *testing.T) {
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					client := newTestClient(memory.New())
					ctx := t.Context()
					const poolID = "metadata-pool"

					if operation != "create pool" {
						mustCreatePool(t, client, poolID, "10.0.0.0/16")
						assertNoError(t, client.UpdatePoolMetadata(ctx, poolID, "previous pool", map[string]string{"state": "previous"}))
					}

					if operation == "update allocation" || operation == "allocate replay" {
						initialDescription := "previous allocation"
						initialLabels := map[string]string{"state": "previous"}
						if operation == "allocate replay" {
							initialDescription, initialLabels = description, labels
						}

						_, err := client.Allocate(ctx, poolID, AllocationSpec{
							ID: testAllocationID, PrefixLength: 24,
							Description: initialDescription, Labels: initialLabels,
						})
						assertNoError(t, err)
					}

					var beforePool Pool
					var beforeAllocations []Allocation

					if operation != "create pool" {
						var err error

						beforePool, err = client.GetPool(ctx, poolID)
						assertNoError(t, err)
						beforeAllocations, err = client.ListAllocations(ctx, poolID)
						assertNoError(t, err)
					}

					var err error

					switch operation {
					case "create pool":
						_, err = client.CreatePool(ctx, PoolSpec{
							ID: poolID, CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
							Description: test.description, Labels: test.labels,
						})
					case "update pool":
						err = client.UpdatePoolMetadata(ctx, poolID, test.description, test.labels)
					case "allocate", "allocate replay":
						_, err = client.Allocate(ctx, poolID, AllocationSpec{
							ID: testAllocationID, PrefixLength: 24,
							Description: test.description, Labels: test.labels,
						})
					case "update allocation":
						err = client.UpdateAllocationMetadata(ctx, poolID, testAllocationID, test.description, test.labels)
					}

					if test.rejected != "" {
						if !errors.Is(err, ErrInvalidArgument) {
							t.Errorf("write error = %v, want ErrInvalidArgument", err)
						}

						wantOp, wantID := operation, ""
						if operation == "allocate replay" {
							wantOp = "allocate"
						}

						if wantOp == "allocate" || wantOp == "update allocation" {
							wantID = testAllocationID
						}

						var opErr *OpError
						if !errors.As(err, &opErr) || opErr.Reason != "invalid_metadata" ||
							opErr.Op != wantOp || opErr.Pool != poolID || opErr.ID != wantID {
							t.Errorf("write error = %#v, want contextual OpError with reason invalid_metadata", err)
						}

						if err != nil {
							for _, rejected := range []string{test.rejected, test.description} {
								if rejected != "" && strings.Contains(err.Error(), rejected) {
									t.Error("write error echoes rejected metadata")
								}
							}

							for key, value := range test.labels {
								if strings.Contains(err.Error(), key) || (value != "" && strings.Contains(err.Error(), value)) {
									t.Error("write error echoes a label key or value")
								}
							}
						}
					} else {
						assertNoError(t, err)
					}

					if operation == "create pool" && test.rejected != "" {
						_, err := client.GetPool(ctx, poolID)
						if !errors.Is(err, ErrNotFound) {
							t.Errorf("GetPool after rejection error = %v, want ErrNotFound", err)
						}

						return
					}

					pool, err := client.GetPool(ctx, poolID)
					assertNoError(t, err)
					allocations, err := client.ListAllocations(ctx, poolID)
					assertNoError(t, err)

					if test.rejected != "" {
						if !reflect.DeepEqual(pool, beforePool) || !reflect.DeepEqual(allocations, beforeAllocations) {
							t.Error("rejected write changed stored pool or allocations")
						}

						return
					}

					gotDescription, gotLabels := pool.Description, pool.Labels
					if operation == "allocate" || operation == "allocate replay" || operation == "update allocation" {
						allocation, err := client.GetAllocation(ctx, poolID, testAllocationID)
						assertNoError(t, err)
						gotDescription, gotLabels = allocation.Description, allocation.Labels
					}

					if gotDescription != test.description || !maps.Equal(gotLabels, test.labels) {
						t.Error("at-limit metadata did not read back unchanged")
					}
				})
			}
		})
	}
}
