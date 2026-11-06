// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/appvia/ipam/storage/memory"
)

func TestMetadataLimitsDoNotAffectReads(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := memory.New()
	client := newTestClient(store)
	const poolID = "legacy"

	description := strings.Repeat("x", 2000)
	labels := make(map[string]string, 65)
	for index := range 64 {
		labels[fmt.Sprintf("key%02d", index)] = "synthetic-value"
	}

	labels["-bad-"] = strings.Repeat("v", 100)
	descriptionJSON, err := json.Marshal(description)
	assertNoError(t, err)
	labelsJSON, err := json.Marshal(labels)
	assertNoError(t, err)

	// Hand-written v1 envelopes deliberately bypass the production document
	// codec and metadata write guards to represent pre-limit stored documents.
	poolJSON := fmt.Sprintf(`{
		"schema_version": 1,
		"kind": "pool",
		"pool_id": "legacy",
		"incarnation": "0123456789abcdef0123456789abcdef",
		"family": 4,
		"cidrs": ["10.0.0.0/16"],
		"description": %s,
		"labels": %s
	}`, descriptionJSON, labelsJSON)
	reservationsJSON := fmt.Sprintf(`{
		"schema_version": 1,
		"kind": "reservations",
		"pool_id": "legacy",
		"incarnation": "0123456789abcdef0123456789abcdef",
		"allocations": {
			%q: {
				"request": {"kind": "next", "prefix_length": 24},
				"cidr": "10.0.0.0/24",
				"description": %s,
				"labels": %s,
				"created_at": "2026-01-01T00:00:00Z"
			}
		}
	}`, testAllocationID, descriptionJSON, labelsJSON)
	_, err = store.CreateIfAbsent(ctx, poolKey(poolID), []byte(poolJSON))
	assertNoError(t, err)
	_, err = store.CreateIfAbsent(ctx, reservationsKey(poolID), []byte(reservationsJSON))
	assertNoError(t, err)

	assertMetadata := func(t *testing.T, gotDescription string, gotLabels map[string]string) {
		t.Helper()

		if gotDescription != description || !maps.Equal(gotLabels, labels) {
			t.Error("legacy metadata did not read back unchanged")
		}
	}

	t.Run("AC-4 GetPool preserves legacy metadata", func(t *testing.T) {
		pool, err := client.GetPool(ctx, poolID)
		assertNoError(t, err)
		assertMetadata(t, pool.Description, pool.Labels)
	})
	t.Run("AC-4 ListPools preserves legacy metadata", func(t *testing.T) {
		pools, err := client.ListPools(ctx)
		assertNoError(t, err)

		if len(pools) != 1 || pools[0].ID != poolID {
			t.Fatalf("ListPools = %v, want only legacy pool", pools)
		}

		assertMetadata(t, pools[0].Description, pools[0].Labels)
	})
	t.Run("AC-4 GetAllocation preserves legacy metadata", func(t *testing.T) {
		allocation, err := client.GetAllocation(ctx, poolID, testAllocationID)
		assertNoError(t, err)
		assertMetadata(t, allocation.Description, allocation.Labels)
	})
	t.Run("AC-4 ListAllocations preserves legacy metadata", func(t *testing.T) {
		allocations, err := client.ListAllocations(ctx, poolID)
		assertNoError(t, err)

		if len(allocations) != 1 || allocations[0].ID != testAllocationID {
			t.Fatalf("ListAllocations = %v, want only legacy allocation", allocations)
		}

		assertMetadata(t, allocations[0].Description, allocations[0].Labels)
	})
	t.Run("AC-4 UpdatePoolMetadata rejects invalid_metadata", func(t *testing.T) {
		err := client.UpdatePoolMetadata(ctx, poolID, description, labels)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("UpdatePoolMetadata error = %v, want ErrInvalidArgument", err)
		}

		var opErr *OpError
		if !errors.As(err, &opErr) || opErr.Reason != "invalid_metadata" {
			t.Errorf("UpdatePoolMetadata error = %#v, want reason invalid_metadata", err)
		}

		pool, err := client.GetPool(ctx, poolID)
		assertNoError(t, err)
		assertMetadata(t, pool.Description, pool.Labels)
	})
	t.Run("AC-4 UpdatePoolPolicy preserves legacy metadata", func(t *testing.T) {
		assertNoError(t, client.UpdatePoolPolicy(ctx, poolID, PoolPolicy{Strategy: StrategyBestFit}))
		pool, err := client.GetPool(ctx, poolID)
		assertNoError(t, err)

		if pool.Policy.Strategy != StrategyBestFit {
			t.Errorf("strategy = %v, want best-fit", pool.Policy.Strategy)
		}

		assertMetadata(t, pool.Description, pool.Labels)
		allocation, err := client.GetAllocation(ctx, poolID, testAllocationID)
		assertNoError(t, err)
		assertMetadata(t, allocation.Description, allocation.Labels)
	})
	t.Run("AC-4 Release succeeds with legacy metadata", func(t *testing.T) {
		assertNoError(t, client.Release(ctx, poolID, testAllocationID))
		allocations, err := client.ListAllocations(ctx, poolID)
		assertNoError(t, err)

		if len(allocations) != 0 {
			t.Errorf("ListAllocations after Release = %v, want empty", allocations)
		}

		pool, err := client.GetPool(ctx, poolID)
		assertNoError(t, err)
		assertMetadata(t, pool.Description, pool.Labels)
	})
}

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
