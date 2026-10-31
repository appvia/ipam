// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"testing"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

var schemaVersionPattern = regexp.MustCompile(`"schema_version": [0-9]+`)

func rewriteSchemaVersion(t *testing.T, store storage.Storage, key, version string) {
	t.Helper()

	data, current, err := store.Get(t.Context(), key)
	assertNoError(t, err)

	data = schemaVersionPattern.ReplaceAll(data, []byte(`"schema_version": `+version))
	_, err = store.PutIfMatch(t.Context(), key, data, current)
	assertNoError(t, err)
}

func TestListPoolsSkipsUnsupportedSchemaPools(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)

	_, err := client.CreatePool(t.Context(), PoolSpec{
		ID:     "p1",
		CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")},
		Policy: PoolPolicy{Strategy: StrategyLowest},
	})
	assertNoError(t, err)
	mustCreatePool(t, client, "p2", "10.2.0.0/16")
	mustCreatePool(t, client, "p3", "10.3.0.0/16")
	rewriteSchemaVersion(t, base, poolKey("p3"), "3")
	rewriteSchemaVersion(t, base, reservationsKey("p3"), "3")

	pools, err := client.ListPools(t.Context())

	var unsupported *UnsupportedPoolsError
	if !errors.As(err, &unsupported) {
		t.Fatalf("ListPools() error = %v, want *UnsupportedPoolsError", err)
	}

	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("ListPools() error = %v, want %v", err, ErrUnsupportedSchema)
	}

	if !slices.Equal(unsupported.PoolIDs, []string{"p3"}) {
		t.Fatalf("PoolIDs = %v, want [p3]", unsupported.PoolIDs)
	}

	if len(pools) != 2 || pools[0].ID != "p1" || pools[1].ID != "p2" {
		t.Fatalf("ListPools() pools = %v, want [p1 p2]", pools)
	}
}

func TestListPoolsAbortsOnInvalidDocument(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)
	mustCreatePool(t, client, "p1", "10.1.0.0/16")
	mustCreatePool(t, client, "p2", "10.2.0.0/16")

	_, version, err := base.Get(t.Context(), poolKey("p2"))
	assertNoError(t, err)

	_, err = base.PutIfMatch(t.Context(), poolKey("p2"), []byte(`{"schema_version":1`), version)
	assertNoError(t, err)

	pools, err := client.ListPools(t.Context())
	if err == nil {
		t.Fatal("ListPools() error = nil, want invalid document error")
	}

	var unsupported *UnsupportedPoolsError
	if errors.As(err, &unsupported) || errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("ListPools() error = %v, want a non-schema error", err)
	}

	if pools != nil {
		t.Fatalf("ListPools() pools = %v, want nil", pools)
	}
}
