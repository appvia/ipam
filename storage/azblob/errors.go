// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package azblob

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"syscall"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"github.com/appvia/ipam/storage"
)

const (
	codeAuthorizationPermissionMismatch = "AuthorizationPermissionMismatch"
	codeAuthorizationFailure            = "AuthorizationFailure"
)

func mapError(err error) error {
	var sentinel error

	var respErr *azcore.ResponseError

	switch {
	case bloberror.HasCode(err, bloberror.ConditionNotMet, bloberror.BlobAlreadyExists):
		sentinel = storage.ErrPreconditionFailed
	case bloberror.HasCode(err, bloberror.BlobNotFound):
		sentinel = storage.ErrNotFound
	case errors.As(err, &respErr) && respErr.StatusCode == http.StatusPreconditionFailed:
		sentinel = storage.ErrPreconditionFailed
	case errors.As(err, &respErr) && respErr.StatusCode == http.StatusForbidden:
		sentinel = storage.ErrAccessDenied
	default:
		return err
	}

	return fmt.Errorf("%w: %w", sentinel, err)
}

// shouldRetry replaces azcore's default retry predicate, so it reproduces the
// defaults (any error azcore did not already rule out, and 408/429/500/502/
// 503/504) and adds access-denied retries only for contexts marked with
// storage.WithAccessPropagationRetry. 412, 409 and 404 are never retried.
func shouldRetry(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}

	if resp == nil {
		return false
	}

	switch resp.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusForbidden:
		code := resp.Header.Get("x-ms-error-code")

		return (code == codeAuthorizationPermissionMismatch || code == codeAuthorizationFailure) &&
			resp.Request != nil && storage.AccessPropagationRetry(resp.Request.Context())
	default:
		return false
	}
}

type outcomeKey struct{}

// outcome records whether any attempt of one conditional write may have been
// applied by the service even though the caller saw no success.
type outcome struct {
	traced         bool
	mayHaveApplied atomic.Bool
}

// outcomePolicy runs per retry after authentication, so requests that fail
// before reaching it (token acquisition, request building) never mark an outcome.
//
// The boundary for a failed attempt is the moment request bytes may have left
// the client. With a net/http transport this is httptrace's WroteHeaders: it
// fires only after DNS, dial, TLS handshake and connection reuse have
// succeeded, so failures before it (cancelled context, DNS, refused or failed
// dial, TLS handshake) cannot have reached the service. A response with 408 or
// 5xx proves the request arrived and may have been applied. Other transports
// cannot be traced, so any of their errors except DNS and connection refused
// counts as possibly applied.
type outcomePolicy struct{}

func (outcomePolicy) Do(req *policy.Request) (*http.Response, error) {
	o, tracked := req.Raw().Context().Value(outcomeKey{}).(*outcome)
	if !tracked {
		return req.Next()
	}

	var wroteHeaders atomic.Bool

	if o.traced {
		req = req.WithContext(httptrace.WithClientTrace(req.Raw().Context(), &httptrace.ClientTrace{
			WroteHeaders: func() { wroteHeaders.Store(true) },
		}))
	}

	resp, err := req.Next()

	switch {
	case err == nil:
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= http.StatusInternalServerError {
			o.mayHaveApplied.Store(true)
		}
	case o.traced:
		if wroteHeaders.Load() {
			o.mayHaveApplied.Store(true)
		}
	default:
		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) && !errors.Is(err, syscall.ECONNREFUSED) {
			o.mayHaveApplied.Store(true)
		}
	}

	return resp, err
}

// traceable reports whether requests go through net/http's Transport, whose
// httptrace hooks outcomePolicy relies on. nil selects azcore's default
// client, which is an *http.Client over an *http.Transport.
func traceable(transport policy.Transporter) bool {
	if transport == nil {
		return true
	}

	client, ok := transport.(*http.Client)
	if !ok {
		return false
	}

	switch client.Transport.(type) {
	case nil, *http.Transport:
		return true
	default:
		return false
	}
}

// write runs a conditional upload or delete. If any attempt may have been
// applied (timeout, connection reset after sending, 5xx) and the operation did
// not succeed, the result is storage.ErrUnknownOutcome: even a later 412 may be
// the answer to our own earlier write.
func (s *Store) write(ctx context.Context, op, key string, do func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("azblob %s %q: %w", op, key, err)
	}

	o := &outcome{traced: s.traced}

	err := do(context.WithValue(ctx, outcomeKey{}, o))

	switch {
	case err == nil:
		return nil
	case o.mayHaveApplied.Load():
		return fmt.Errorf("azblob %s %q: %w: %w", op, key, storage.ErrUnknownOutcome, err)
	default:
		return fmt.Errorf("azblob %s %q: %w", op, key, mapError(err))
	}
}
