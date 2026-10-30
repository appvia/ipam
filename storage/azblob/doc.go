// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package azblob implements storage.Storage on an Azure Blob Storage container
// using ETag conditions: creates send If-None-Match: *, updates and deletes
// send If-Match. Blob leases are never used.
//
// Accounts with hierarchical namespace (ADLS Gen2) are unsupported: a rename
// can move a blob past the path-based role-assignment conditions that confine
// each environment to its own reservations. Use a flat-namespace account.
//
// Authentication uses an azcore.TokenCredential, normally built by
// AuthConfig.Credential through github.com/Azure/entrauth/aztfauth with the
// azapi provider's ARM_* settings. Shared-key authentication is accepted only
// from the AZURE_STORAGE_CONNECTION_STRING environment variable, and only when
// it targets a loopback endpoint such as the Azurite emulator; account keys
// are never accepted as configuration.
package azblob
