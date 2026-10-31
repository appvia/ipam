// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"strings"
	"testing"

	"github.com/appvia/ipam"
	"github.com/appvia/ipam/storage/memory"
)

type fragmentationOutput struct {
	Result string `json:"result"`
	Checks []struct {
		Check  string `json:"check"`
		Pool   string `json:"pool"`
		Status string `json:"status"`
		Detail string `json:"detail"`
		Stats  *struct {
			Utilization        string `json:"utilization"`
			Total              string `json:"total"`
			Free               string `json:"free"`
			Allocatable        string `json:"allocatable"`
			Capacity           string `json:"capacity"`
			FragmentationIndex string `json:"fragmentation_index"`
			LargestFree        string `json:"largest_free"`
			FreeBlocks         []struct {
				PrefixLength int `json:"prefix_length"`
				Count        int `json:"count"`
			} `json:"free_blocks"`
			ReferenceLength int    `json:"reference_length"`
			Strategy        string `json:"strategy"`
			Cooldown        string `json:"cooldown"`
		} `json:"stats"`
	} `json:"checks"`
}

func fragmentationCheck(t *testing.T, out, pool string) (fragmentationOutput, int) {
	t.Helper()

	var report fragmentationOutput
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor stdout is not JSON: %v\n%s", err, out)
	}

	for i, check := range report.Checks {
		if check.Check == "fragmentation" && check.Pool == pool {
			return report, i
		}
	}

	t.Fatalf("no fragmentation check for pool %q in %s", pool, out)

	return report, -1
}

func TestDoctorFragmentationHealthyPoolPasses(t *testing.T) {
	mem := newPoolStore(t)
	store := &inspectingStore{recordingStore: &recordingStore{Storage: mem}, info: protected()}

	res := runDoctor(t, store, "-o", "json")
	if res.code != exitOK {
		t.Fatalf("exit %d; stdout %s stderr %s", res.code, res.stdout, res.stderr)
	}

	report, i := fragmentationCheck(t, res.stdout, "p")
	check := report.Checks[i]

	if report.Result != "pass" || check.Status != "PASS" {
		t.Fatalf("result %q, check %+v, want pass", report.Result, check)
	}

	if !strings.HasPrefix(check.Detail, "util 0.4%, free 65280 of 65536, largest /") ||
		!strings.Contains(check.Detail, "/24 allocatable 255 of 255, index 0.00") || strings.Contains(check.Detail, "\n") {
		t.Fatalf("detail = %q", check.Detail)
	}

	if check.Stats == nil || check.Stats.Total != "65536" || check.Stats.Free != "65280" ||
		check.Stats.ReferenceLength != 24 || check.Stats.Capacity != "255" || check.Stats.Strategy == "" ||
		check.Stats.Cooldown != "none" || len(check.Stats.FreeBlocks) == 0 {
		t.Fatalf("stats = %+v", check.Stats)
	}

	// The fragmentation row comes after every write probe.
	for j, other := range report.Checks {
		if other.Check == "write-probe" && j > i {
			t.Fatalf("write-probe after fragmentation: %+v", report.Checks)
		}
	}

	if writes := store.recordedWrites(); len(writes) != 1 || writes[0] != "put reservations/p.json "+string(probeVersion) {
		t.Fatalf("writes = %q, want only the probe", writes)
	}
}

func TestDoctorFragmentationNoReferenceOmitsParts(t *testing.T) {
	mem := memory.New()
	if _, err := ipam.New(mem).CreatePool(t.Context(), ipam.PoolSpec{
		ID: "e", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/24")},
	}); err != nil {
		t.Fatal(err)
	}

	res := runDoctor(t, &inspectingStore{recordingStore: &recordingStore{Storage: mem}, info: protected()}, "-o", "json")

	report, i := fragmentationCheck(t, res.stdout, "e")
	check := report.Checks[i]

	if check.Status != "PASS" || strings.Contains(check.Detail, "allocatable") || strings.Contains(check.Detail, "index") {
		t.Fatalf("check = %+v", check)
	}

	if check.Stats == nil || check.Stats.ReferenceLength != 0 || check.Stats.FragmentationIndex != "" {
		t.Fatalf("stats = %+v", check.Stats)
	}
}

func TestDoctorFragmentedPoolWarnsWithExitZero(t *testing.T) {
	mem := memory.New()
	client := ipam.New(mem)

	if _, err := client.CreatePool(t.Context(), ipam.PoolSpec{
		ID:     "f",
		CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.1.0.0/22")},
		Policy: ipam.PoolPolicy{Strategy: ipam.StrategyLowest},
	}); err != nil {
		t.Fatal(err)
	}

	first, err := client.Allocate(t.Context(), "f", ipam.AllocationSpec{PrefixLength: 26})
	if err != nil || first.CIDR != netip.MustParsePrefix("10.1.0.0/26") {
		t.Fatalf("first /26 = %v, %v", first.CIDR, err)
	}

	// Reserve every other remaining /27 (indexes 3, 5, ... 31), leaving 15 isolated /27s.
	base := netip.MustParseAddr("10.1.0.0")
	for index := 3; index < 32; index += 2 {
		addr := base
		for range index * 32 {
			addr = addr.Next()
		}

		if _, err := client.Allocate(t.Context(), "f", ipam.AllocationSpec{CIDR: netip.PrefixFrom(addr, 27)}); err != nil {
			t.Fatalf("explicit /27 #%d: %v", index, err)
		}
	}

	store := &inspectingStore{recordingStore: &recordingStore{Storage: mem}, info: protected()}

	res := runDoctor(t, store, "-o", "json")
	if res.code != exitOK {
		t.Fatalf("exit %d, want 0; stdout %s stderr %s", res.code, res.stdout, res.stderr)
	}

	report, i := fragmentationCheck(t, res.stdout, "f")
	check := report.Checks[i]

	if report.Result != "warn" || check.Status != "WARN" {
		t.Fatalf("result %q, check %+v, want warn", report.Result, check)
	}

	stats := check.Stats
	if stats == nil || stats.ReferenceLength != 26 || stats.Allocatable != "0" || stats.Capacity != "7" ||
		stats.FragmentationIndex != "1.00" || stats.Strategy != "lowest" {
		t.Fatalf("stats = %+v", stats)
	}

	if !strings.Contains(check.Detail, "blocks /27:15") || !strings.HasSuffix(check.Detail, "/26 allocatable 0 of 7, index 1.00") {
		t.Fatalf("detail = %q", check.Detail)
	}

	if writes := store.recordedWrites(); len(writes) != 1 || writes[0] != "put reservations/f.json "+string(probeVersion) {
		t.Fatalf("writes = %q, want only the probe", writes)
	}

	table := runDoctor(t, &inspectingStore{recordingStore: &recordingStore{Storage: mem}, info: protected()})
	if table.code != exitOK || !strings.Contains(table.stdout, "fragmentation") || !strings.Contains(table.stdout, "RESULT: warn") {
		t.Fatalf("table output: exit %d\n%s", table.code, table.stdout)
	}
}

func TestDoctorFragmentationUnsupportedSchemaWarns(t *testing.T) {
	mem := newPoolStore(t)

	data, version, err := mem.Get(t.Context(), "reservations/p.json")
	if err != nil {
		t.Fatal(err)
	}

	changed := regexp.MustCompile(`"schema_version":\s*\d+`).ReplaceAll(data, []byte(`"schema_version": 3`))
	if string(changed) == string(data) {
		t.Fatalf("no schema_version in %s", data)
	}

	if _, err := mem.PutIfMatch(t.Context(), "reservations/p.json", changed, version); err != nil {
		t.Fatal(err)
	}

	store := &inspectingStore{recordingStore: &recordingStore{Storage: mem}, info: protected()}

	res := runDoctor(t, store, "-o", "json")
	if res.code != exitOK {
		t.Fatalf("exit %d, want 0; stdout %s stderr %s", res.code, res.stdout, res.stderr)
	}

	report, i := fragmentationCheck(t, res.stdout, "p")
	check := report.Checks[i]

	if report.Result != "warn" || check.Status != "WARN" || !strings.Contains(check.Detail, "unsupported schema") || check.Stats != nil {
		t.Fatalf("result %q, check %+v, want WARN unsupported schema without stats", report.Result, check)
	}
}
