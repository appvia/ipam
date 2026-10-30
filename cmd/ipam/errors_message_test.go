// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

func TestStderrPrefixIsNotDoubled(t *testing.T) {
	h := newFileHarness(t)

	res := h.runEnv(map[string]string{"IPAM_BACKEND": "ftp://example/x"}, "pool", "list")
	if res.code != exitUsage {
		t.Fatalf("exit %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
	}

	if strings.Contains(res.stderr, "ipam: ipam:") || !strings.HasPrefix(res.stderr, "ipam: invalid argument: ") {
		t.Fatalf("stderr %q does not read \"ipam: invalid argument: ...\" once", res.stderr)
	}
}

func TestInvalidCIDRReasonAndExitCode(t *testing.T) {
	h := newMemHarness(t)
	h.ok("pool", "create", "prod", "--cidr", "10.0.0.0/16")

	cases := map[string]struct {
		args []string
		want string
	}{
		"outside": {[]string{"alloc", "create", "prod", "--cidr", "11.0.0.0/24"}, "cidr 11.0.0.0/24 is outside pool prod (10.0.0.0/16)"},
		"shorter": {[]string{"alloc", "create", "prod", "--prefix-length", "8"}, "prefix length 8 is shorter than pool prod (/16)"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := h.run(tc.args...)
			if res.code != exitUsage {
				t.Fatalf("exit %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}

			if !strings.Contains(res.stderr, tc.want) {
				t.Fatalf("stderr %q lacks %q", res.stderr, tc.want)
			}

			if strings.Contains(res.stderr, "pool pool") {
				t.Fatalf("stderr %q repeats \"pool\"", res.stderr)
			}
		})
	}
}
