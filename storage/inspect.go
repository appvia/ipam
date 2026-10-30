// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package storage

import "context"

// TriState is a backend property that may be unknown, for example because the
// caller lacks permission to read it.
type TriState int

const (
	// Unknown means the property could not be determined.
	Unknown TriState = iota
	// Yes means the property is enabled.
	Yes
	// No means the property is disabled or not supported.
	No
)

// String returns "unknown", "yes" or "no".
func (t TriState) String() string {
	switch t {
	case Yes:
		return "yes"
	case No:
		return "no"
	default:
		return "unknown"
	}
}

// Info describes the data-protection features of a backend.
type Info struct {
	// Versioning reports whether overwritten objects keep prior versions.
	Versioning TriState
	// SoftDelete reports whether deleted objects are retained for recovery.
	SoftDelete TriState
	// HierarchicalNamespace reports an Azure Data Lake Gen2 account, whose
	// conditional-write semantics this library does not support.
	HierarchicalNamespace TriState
}

// Inspector is implemented by backends that can report their data-protection
// features. Inspect only reads; a property the caller may not read is Unknown.
type Inspector interface {
	Inspect(ctx context.Context) (Info, error)
}
