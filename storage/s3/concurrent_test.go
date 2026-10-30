// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package s3_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	s3store "github.com/appvia/ipam/storage/s3"
)

// TestSharedStoreClassifiesEachWriteOnItsOwnAttempts runs concurrent writes on
// ONE Store and checks every key's classification. Keys u-* answer 500 then
// 412 (possibly applied, so unknown); keys p-* answer a first-attempt 412
// (precondition failed after exactly one request).
//
// The server orders the calls so that outcome state shared between calls
// is caught deterministically: the p-* calls start only after every u-* call
// has recorded its 500 (its retry reached the server), and the u-* retries are
// answered only after every p-* request arrived. State reset per call would
// erase the u-* marks; state never reset would leak them into the p-* calls.
// The Store's per-call options have spare capacity, so a call appending to
// them in place races with the others under -race.
func TestSharedStoreClassifiesEachWriteOnItsOwnAttempts(t *testing.T) {
	t.Parallel()

	const perKind = 20

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	var (
		mu          sync.Mutex
		requests    = map[string]int{}
		uRetried    int
		pArrived    int
		allURetried = make(chan struct{})
		allPArrived = make(chan struct{})
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		key := strings.TrimPrefix(r.URL.Path, "/"+testBucket+"/")

		mu.Lock()
		requests[key]++
		n := requests[key]

		switch {
		case strings.HasPrefix(key, "p-"):
			if pArrived++; pArrived == perKind {
				close(allPArrived)
			}
		case n == 2:
			if uRetried++; uRetried == perKind {
				close(allURetried)
			}
		}
		mu.Unlock()

		switch {
		case strings.HasPrefix(key, "p-"):
			writeError(w, http.StatusPreconditionFailed, "PreconditionFailed")
		case n == 1:
			writeError(w, http.StatusInternalServerError, "InternalError")
		default:
			select {
			case <-allPArrived:
				writeError(w, http.StatusPreconditionFailed, "PreconditionFailed")
			case <-r.Context().Done():
			}
		}
	}))
	t.Cleanup(server.Close)

	store, err := s3store.New(t.Context(), s3store.Config{
		Bucket:       testBucket,
		Region:       "us-east-1",
		Endpoint:     server.URL,
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	shared := s3store.WithSpareOptionCapacity(s3store.WithoutRetryDelay(store))

	write := func(ctx context.Context, i int, key string) error {
		switch i % 3 {
		case 0:
			_, err := shared.CreateIfAbsent(ctx, key, []byte("d"))
			return err
		case 1:
			_, err := shared.PutIfMatch(ctx, key, []byte("d"), storage.Version(`"v"`))
			return err
		default:
			return shared.DeleteIfMatch(ctx, key, storage.Version(`"v"`))
		}
	}

	results := make(map[string]error, 2*perKind)

	var (
		wg        sync.WaitGroup
		resultsMu sync.Mutex
	)

	run := func(key string, i int, wait <-chan struct{}) {
		wg.Go(func() {
			if wait != nil {
				select {
				case <-wait:
				case <-ctx.Done():
				}
			}

			err := write(ctx, i, key)

			resultsMu.Lock()
			results[key] = err
			resultsMu.Unlock()
		})
	}

	for i := range perKind {
		run(fmt.Sprintf("u-%02d", i), i, nil)
		run(fmt.Sprintf("p-%02d", i), i, allURetried)
	}

	wg.Wait()

	if err := ctx.Err(); err != nil {
		t.Fatalf("writes did not finish: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	for key, err := range results {
		if strings.HasPrefix(key, "u-") {
			if !errors.Is(err, storage.ErrUnknownOutcome) || errors.Is(err, storage.ErrPreconditionFailed) {
				t.Errorf("%s: error = %v, want only %v (500 then 412)", key, err, storage.ErrUnknownOutcome)
			}

			if requests[key] != 2 {
				t.Errorf("%s: requests = %d, want 2", key, requests[key])
			}

			continue
		}

		if !errors.Is(err, storage.ErrPreconditionFailed) || errors.Is(err, storage.ErrUnknownOutcome) {
			t.Errorf("%s: error = %v, want only %v (first-attempt 412)", key, err, storage.ErrPreconditionFailed)
		}

		if requests[key] != 1 {
			t.Errorf("%s: requests = %d, want exactly 1", key, requests[key])
		}
	}
}
