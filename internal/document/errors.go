// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package document

import "errors"

var (
	// ErrUnsupportedSchema identifies an unknown document schema version.
	ErrUnsupportedSchema = errors.New("document: unsupported schema")
	// ErrInvalidDocument identifies malformed JSON or an invalid envelope.
	ErrInvalidDocument = errors.New("document: invalid document")
)
