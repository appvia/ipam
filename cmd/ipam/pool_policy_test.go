// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/appvia/ipam"
	"github.com/appvia/ipam/storage/memory"
)

func TestPoolCreateDefaultStrategyIsBestFit(t *testing.T) {
	h := newMemHarness(t)

	pool := decodeObject(t, h.ok("pool", "create", "p", "--cidr", "10.0.0.0/24", "-o", "json"))
	if pool["strategy"] != "best-fit" {
		t.Fatalf("strategy = %v, want best-fit", pool["strategy"])
	}

	if _, ok := pool["cooldown"]; ok {
		t.Fatalf("cooldown present on a pool without one: %v", pool)
	}
}

func TestPoolCreateWithPolicy(t *testing.T) {
	h := newMemHarness(t)

	pool := decodeObject(t, h.ok("pool", "create", "p", "--cidr", "10.0.0.0/24",
		"--strategy", "lowest", "--cooldown", "1h", "--cooldown-mode", "strict", "-o", "json"))

	cooldown, _ := pool["cooldown"].(map[string]any)
	if pool["strategy"] != "lowest" || cooldown["mode"] != "strict" || cooldown["duration"] != "1h0m0s" {
		t.Fatalf("pool create = %v", pool)
	}

	got := decodeObject(t, h.ok("pool", "get", "p", "-o", "json"))

	cooldown, _ = got["cooldown"].(map[string]any)
	if cooldown["mode"] != "strict" || cooldown["duration"] != "1h0m0s" {
		t.Fatalf("pool get = %v", got)
	}

	if table := h.ok("pool", "get", "p"); !strings.Contains(table, "STRATEGY") ||
		!strings.Contains(table, "COOLDOWN") || !strings.Contains(table, "strict 1h0m0s") {
		t.Fatalf("table lacks policy columns:\n%s", table)
	}
}

func TestPoolUpdatePolicy(t *testing.T) {
	h := newMemHarness(t)
	h.ok("pool", "create", "p", "--cidr", "10.0.0.0/24", "--cooldown", "1h", "--cooldown-mode", "fit-aware")

	pool := decodeObject(t, h.ok("pool", "update", "p", "--strategy", "lowest", "-o", "json"))
	if cooldown, _ := pool["cooldown"].(map[string]any); pool["strategy"] != "lowest" || cooldown["mode"] != "fit-aware" {
		t.Fatalf("strategy-only update = %v", pool)
	}

	pool = decodeObject(t, h.ok("pool", "update", "p", "--cooldown", "30m", "--cooldown-mode", "strict",
		"--description", "d", "-o", "json"))
	if cooldown, _ := pool["cooldown"].(map[string]any); pool["strategy"] != "lowest" ||
		cooldown["mode"] != "strict" || cooldown["duration"] != "30m0s" || pool["description"] != "d" {
		t.Fatalf("cooldown update = %v", pool)
	}

	pool = decodeObject(t, h.ok("pool", "update", "p", "--strategy", "lowest", "--no-cooldown", "-o", "json"))
	if _, ok := pool["cooldown"]; ok || pool["strategy"] != "lowest" {
		t.Fatalf("no-cooldown update = %v", pool)
	}
}

func TestPoolPolicyFlagErrors(t *testing.T) {
	h := newMemHarness(t)
	h.ok("pool", "create", "p", "--cidr", "10.0.0.0/24")

	cases := []struct {
		name string
		args []string
	}{
		{"create cooldown without mode", []string{"pool", "create", "q", "--cidr", "10.1.0.0/24", "--cooldown", "1h"}},
		{"create mode without cooldown", []string{"pool", "create", "q", "--cidr", "10.1.0.0/24", "--cooldown-mode", "strict"}},
		{"create bad strategy", []string{"pool", "create", "q", "--cidr", "10.1.0.0/24", "--strategy", "random"}},
		{"create empty strategy", []string{"pool", "create", "q", "--cidr", "10.1.0.0/24", "--strategy", ""}},
		{"create bad mode", []string{"pool", "create", "q", "--cidr", "10.1.0.0/24", "--cooldown", "1h", "--cooldown-mode", "x"}},
		{"create bad duration", []string{"pool", "create", "q", "--cidr", "10.1.0.0/24", "--cooldown", "soon", "--cooldown-mode", "strict"}},
		{"create zero duration", []string{"pool", "create", "q", "--cidr", "10.1.0.0/24", "--cooldown", "0s", "--cooldown-mode", "strict"}},
		{"create huge duration", []string{"pool", "create", "q", "--cidr", "10.1.0.0/24", "--cooldown", "9000h", "--cooldown-mode", "strict"}},
		{"update nothing", []string{"pool", "update", "p"}},
		{"update cooldown without mode", []string{"pool", "update", "p", "--cooldown", "1h"}},
		{"update bad strategy", []string{"pool", "update", "p", "--strategy", "random"}},
		{"update no-cooldown with cooldown", []string{"pool", "update", "p", "--no-cooldown", "--cooldown", "1h", "--cooldown-mode", "strict"}},
		{"update no-cooldown with mode", []string{"pool", "update", "p", "--no-cooldown", "--cooldown-mode", "strict"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if res := h.run(tc.args...); res.code != exitUsage || res.stderr == "" {
				t.Fatalf("exit %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
		})
	}

	if res := h.run("pool", "get", "q"); res.code != exitNotFound {
		t.Fatalf("pool q exists after rejected creates: exit %d", res.code)
	}

	if pool := decodeObject(t, h.ok("pool", "get", "p", "-o", "json")); pool["strategy"] != "best-fit" {
		t.Fatalf("pool p changed: %v", pool)
	}
}

func TestCooldownRefusalExitsExhausted(t *testing.T) {
	h := newMemHarness(t)
	h.ok("pool", "create", "p", "--cidr", "10.0.0.0/24", "--cooldown", "1h", "--cooldown-mode", "strict")

	first := decodeObject(t, h.ok("alloc", "create", "p", "--prefix-length", "25", "-o", "json"))["id"].(string)
	h.ok("alloc", "create", "p", "--prefix-length", "25")
	h.ok("alloc", "release", "p", first)

	res := h.run("alloc", "create", "p", "--prefix-length", "25")
	if res.code != exitExhausted || !strings.Contains(res.stderr, "(cooldown)") || !strings.Contains(res.stderr, "until") {
		t.Fatalf("exit %d, stderr %q", res.code, res.stderr)
	}
}

func TestPoolListWarnsAboutUnsupportedPools(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	h := &harness{
		t: t, backend: "mem://", env: map[string]string{},
		open: func(context.Context, string) (*ipam.Client, error) { return ipam.New(store), nil },
	}

	h.ok("pool", "create", "good", "--cidr", "10.0.0.0/24")
	h.ok("pool", "create", "other", "--cidr", "10.1.0.0/24")

	if _, err := store.CreateIfAbsent(ctx, "pools/future.json", []byte(`{"schema_version":3,"kind":"pool","pool_id":"future"}`)); err != nil {
		t.Fatal(err)
	}

	res := h.run("pool", "list", "-o", "json")
	if res.code != exitOK {
		t.Fatalf("exit %d, stderr %q", res.code, res.stderr)
	}

	if ids := poolIDs(decodeArray(t, res.stdout)); strings.Join(ids, ",") != "good,other" {
		t.Fatalf("listed pools = %v", ids)
	}

	const warning = "ipam: warning: skipped pools with an unsupported schema (upgrade ipam): future\n"
	if res.stderr != warning {
		t.Fatalf("stderr = %q, want %q", res.stderr, warning)
	}

	table := h.run("pool", "list")
	if table.code != exitOK || !strings.Contains(table.stdout, "good") || !strings.Contains(table.stderr, "future") {
		t.Fatalf("table: exit %d stdout %q stderr %q", table.code, table.stdout, table.stderr)
	}
}

func poolIDs(pools []map[string]any) []string {
	ids := make([]string, 0, len(pools))
	for _, pool := range pools {
		ids = append(ids, pool["id"].(string))
	}

	return ids
}
