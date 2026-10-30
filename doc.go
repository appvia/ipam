// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package ipam is the client entrypoint for pool and allocation management.
//
// A pool is stored as two documents: pools/<id>.json (the definition) and
// reservations/<id>.json (its allocations). Both carry the same random
// incarnation, and a pool is usable only while both exist with equal
// incarnations. CreatePool generates a fresh incarnation on every call and
// writes it only with CreateIfAbsent: the child carve-out first, then the
// reservations document, and the pool document last. It never adopts or
// rewrites a document it did not create. Residue of an interrupted create or
// delete makes CreatePool return ErrConflict with Reason pool_incomplete;
// DeletePool clears every residue shape, after which CreatePool succeeds.
//
// Child-pool creation reserves space in the parent before publishing the child
// pool document. The carve-out ID pool:<child>:<incarnation> includes the child
// incarnation. If a process stops between those writes, that incarnation's
// carve-out remains reserved and therefore cannot overlap another allocation.
// A later CreatePool uses a fresh incarnation and leaves the earlier carve-out
// safely abandoned.
//
// DeletePool deliberately never scans unrelated pools for abandoned carve-outs.
// An operator may release one explicitly by its complete
// pool:<child>:<incarnation> ID. Release refuses while a pool or reservations
// document of that child incarnation still exists. Releasing a carve-out is
// only safe when no CreatePool for that child can still be running: a creator
// between committing the carve-out and writing the child's reservations
// document cannot be detected, and would publish the child on released space.
package ipam
