// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// staleETag is a syntactically valid ETag that no object written by the probe can have.
const staleETag = `"00000000000000000000000000000000"`

const concurrentWriters = 20

// statusError carries the HTTP status of a failed storage request.
type statusError struct {
	code int
	err  error
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d: %v", e.code, e.err) }

func (e *statusError) Unwrap() error { return e.err }

func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.code
	}

	return 0
}

func readAndClose(rc io.ReadCloser) ([]byte, error) {
	body, readErr := io.ReadAll(rc)
	if err := errors.Join(readErr, rc.Close()); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return body, nil
}

type condition struct {
	ifMatch         string
	ifNoneMatchStar bool
}

// store is the minimal conditional-object API each emulator adapter implements.
type store interface {
	put(ctx context.Context, key string, body []byte, cond condition) (etag string, err error)
	del(ctx context.Context, key, ifMatch string) error
	get(ctx context.Context, key string) (body []byte, etag string, err error)
	// createOnlyRejected reports whether err is the backend's documented answer to
	// an If-None-Match: * write over an existing object.
	createOnlyRejected(err error) bool
}

type checkResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type check struct {
	name string
	run  func(ctx context.Context, s store, key string) error
}

var checks = []check{
	{"stale_if_match_put_412", checkStaleIfMatchPut},
	{"if_none_match_star_existing_rejected", checkIfNoneMatchExisting},
	{"stale_if_match_delete_412", checkStaleIfMatchDelete},
	{"if_match_put_missing_404_or_412_creates_nothing", checkIfMatchPutMissing},
	{"if_match_delete_missing_404_or_412", checkIfMatchDeleteMissing},
	{"concurrent_if_match_put_exactly_one_wins", checkConcurrentPut},
}

func runChecks(ctx context.Context, s store, prefix string, perCheck time.Duration) []checkResult {
	results := make([]checkResult, 0, len(checks))

	for _, c := range checks {
		cctx, cancel := context.WithTimeout(ctx, perCheck)
		err := c.run(cctx, s, prefix+"/"+c.name)

		cancel()

		r := checkResult{Name: c.name, OK: err == nil}
		if err != nil {
			r.Detail = err.Error()
		}

		results = append(results, r)
	}

	return results
}

func expectStatus(err error, want ...int) error {
	if err == nil {
		return fmt.Errorf("request succeeded, want HTTP %v", want)
	}

	got := statusOf(err)
	for _, w := range want {
		if got == w {
			return nil
		}
	}

	return fmt.Errorf("want HTTP %v, got: %w", want, err)
}

func expectBody(ctx context.Context, s store, key string, want []byte) error {
	got, _, err := s.get(ctx, key)
	if err != nil {
		return fmt.Errorf("read back %s: %w", key, err)
	}

	if !bytes.Equal(got, want) {
		return fmt.Errorf("read back %s: body %q, want %q", key, got, want)
	}

	return nil
}

func expectMissing(ctx context.Context, s store, key string) error {
	_, _, err := s.get(ctx, key)
	if err == nil {
		return fmt.Errorf("object %s exists, want it absent", key)
	}

	if statusOf(err) != http.StatusNotFound {
		return fmt.Errorf("read back %s: want HTTP 404, got: %w", key, err)
	}

	return nil
}

// putTwo writes v1 then v2 unconditionally and returns v1's (now stale) ETag.
func putTwo(ctx context.Context, s store, key string) (string, error) {
	e1, err := s.put(ctx, key, []byte("v1"), condition{})
	if err != nil {
		return "", fmt.Errorf("setup put v1: %w", err)
	}

	e2, err := s.put(ctx, key, []byte("v2"), condition{})
	if err != nil {
		return "", fmt.Errorf("setup put v2: %w", err)
	}

	if e1 == "" || e1 == e2 {
		return "", fmt.Errorf("setup: ETag did not change between writes (%q, %q)", e1, e2)
	}

	return e1, nil
}

func checkStaleIfMatchPut(ctx context.Context, s store, key string) error {
	stale, err := putTwo(ctx, s, key)
	if err != nil {
		return err
	}

	_, err = s.put(ctx, key, []byte("v3"), condition{ifMatch: stale})
	if err := expectStatus(err, http.StatusPreconditionFailed); err != nil {
		return err
	}

	return expectBody(ctx, s, key, []byte("v2"))
}

func checkIfNoneMatchExisting(ctx context.Context, s store, key string) error {
	if _, err := s.put(ctx, key, []byte("v1"), condition{}); err != nil {
		return fmt.Errorf("setup put: %w", err)
	}

	_, err := s.put(ctx, key, []byte("v2"), condition{ifNoneMatchStar: true})
	if err == nil {
		return errors.New("create-only write over an existing object succeeded")
	}

	if !s.createOnlyRejected(err) {
		return fmt.Errorf("unexpected answer to create-only write: %w", err)
	}

	return expectBody(ctx, s, key, []byte("v1"))
}

func checkStaleIfMatchDelete(ctx context.Context, s store, key string) error {
	stale, err := putTwo(ctx, s, key)
	if err != nil {
		return err
	}

	if err := expectStatus(s.del(ctx, key, stale), http.StatusPreconditionFailed); err != nil {
		return err
	}

	return expectBody(ctx, s, key, []byte("v2"))
}

func checkIfMatchPutMissing(ctx context.Context, s store, key string) error {
	_, err := s.put(ctx, key, []byte("v1"), condition{ifMatch: staleETag})
	if err := expectStatus(err, http.StatusNotFound, http.StatusPreconditionFailed); err != nil {
		return err
	}

	return expectMissing(ctx, s, key)
}

func checkIfMatchDeleteMissing(ctx context.Context, s store, key string) error {
	return expectStatus(s.del(ctx, key, staleETag), http.StatusNotFound, http.StatusPreconditionFailed)
}

func checkConcurrentPut(ctx context.Context, s store, key string) error {
	base, err := s.put(ctx, key, []byte("v0"), condition{})
	if err != nil {
		return fmt.Errorf("setup put: %w", err)
	}

	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		errs    = make([]error, concurrentWriters)
		bodies  = make([][]byte, concurrentWriters)
		winners []int
	)

	for i := range concurrentWriters {
		bodies[i] = fmt.Appendf(nil, "writer-%02d", i)

		wg.Go(func() {
			<-start

			_, errs[i] = s.put(ctx, key, bodies[i], condition{ifMatch: base})
		})
	}

	close(start)
	wg.Wait()

	var unexpected []string

	for i, err := range errs {
		switch {
		case err == nil:
			winners = append(winners, i)
		case statusOf(err) == http.StatusPreconditionFailed, statusOf(err) == http.StatusConflict:
		default:
			unexpected = append(unexpected, fmt.Sprintf("writer %d: %v", i, err))
		}
	}

	if len(unexpected) > 0 {
		return fmt.Errorf("losers must get 412/409; unexpected errors: %v", unexpected)
	}

	if len(winners) != 1 {
		return fmt.Errorf("%d of %d same-version writers succeeded (writers %v), want exactly 1",
			len(winners), concurrentWriters, winners)
	}

	return expectBody(ctx, s, key, bodies[winners[0]])
}
