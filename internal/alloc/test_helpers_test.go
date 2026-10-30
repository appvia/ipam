// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package alloc

import (
	"net/netip"
	"testing"
)

func prefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}

	return result
}

func assertPrefix(t *testing.T, got netip.Prefix, err error, want string) {
	t.Helper()

	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}

	if got != netip.MustParsePrefix(want) {
		t.Fatalf("Next() = %v, want %v", got, want)
	}
}
