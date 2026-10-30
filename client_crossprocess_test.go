// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam_test

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/appvia/ipam"
	"github.com/appvia/ipam/storage/local"
)

func TestCrossProcessLocalAllocationProof(t *testing.T) {
	if worker := os.Getenv("IPAM_CROSS_PROCESS_WORKER"); worker != "" {
		runAllocationWorker(t, worker)
		return
	}

	directory := t.TempDir()

	store, err := local.New(directory)
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	client := ipam.New(store, ipam.WithMaxAttempts(0))

	_, err = client.CreatePool(t.Context(), ipam.PoolSpec{
		ID:    "prod",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
	})
	if err != nil {
		t.Fatalf("CreatePool() error = %v", err)
	}

	const processes = 4

	outputs := make([][]byte, processes)
	errorsByProcess := make([]error, processes)

	var wait sync.WaitGroup
	for process := range processes {
		wait.Add(1)
		go func() {
			defer wait.Done()

			command := exec.CommandContext(
				t.Context(),
				os.Args[0],
				"-test.run=^TestCrossProcessLocalAllocationProof$",
				"-test.count=1",
			)

			command.Env = append(
				os.Environ(),
				"IPAM_CROSS_PROCESS_WORKER="+strconv.Itoa(process),
				"IPAM_CROSS_PROCESS_DIRECTORY="+directory,
			)
			outputs[process], errorsByProcess[process] = command.CombinedOutput()
		}()
	}

	wait.Wait()

	for process, commandErr := range errorsByProcess {
		if commandErr != nil {
			t.Fatalf("worker %d error = %v\n%s", process, commandErr, outputs[process])
		}
	}

	allocations, err := client.ListAllocations(t.Context(), "prod")
	if err != nil {
		t.Fatalf("ListAllocations() error = %v", err)
	}

	if len(allocations) != 100 {
		t.Fatalf("allocation count = %d, want 100", len(allocations))
	}

	slices.SortFunc(allocations, func(left, right ipam.Allocation) int {
		return left.CIDR.Addr().Compare(right.CIDR.Addr())
	})

	for index, allocation := range allocations {
		want := netip.MustParsePrefix(fmt.Sprintf("10.0.%d.0/24", index))
		if allocation.CIDR != want {
			t.Fatalf("allocation %d = %s, want %s", index, allocation.CIDR, want)
		}

		if index > 0 && allocations[index-1].CIDR.Contains(allocation.CIDR.Addr()) {
			t.Fatalf("allocations overlap: %s and %s", allocations[index-1].CIDR, allocation.CIDR)
		}
	}
}

func runAllocationWorker(t *testing.T, worker string) {
	t.Helper()

	process, err := strconv.Atoi(worker)
	if err != nil {
		t.Fatalf("worker index %q: %v", worker, err)
	}

	store, err := local.New(os.Getenv("IPAM_CROSS_PROCESS_DIRECTORY"))
	if err != nil {
		t.Fatalf("local.New() error = %v", err)
	}

	client := ipam.New(store, ipam.WithMaxAttempts(0))

	for index := range 25 {
		id := fmt.Sprintf("01900000-0000-7000-8000-%012d", process*25+index+1)

		_, err := client.Allocate(context.Background(), "prod", ipam.AllocationSpec{
			ID:           id,
			PrefixLength: 24,
		})
		if err != nil {
			t.Fatalf("Allocate(%s) error = %v", id, err)
		}
	}
}
