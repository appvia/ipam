// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package storage

import "errors"

var (
	// ErrNotFound indicates that an object does not exist.
	ErrNotFound = errors.New("storage: object not found")
	// ErrPreconditionFailed indicates that a conditional operation did not match.
	ErrPreconditionFailed = errors.New("storage: precondition failed")
	// ErrAccessDenied indicates that an operation was not authorised.
	ErrAccessDenied = errors.New("storage: access denied")
	// ErrUnknownOutcome indicates that a conditional request may have reached the backend.
	ErrUnknownOutcome = errors.New("storage: conditional operation outcome unknown")
)
