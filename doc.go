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
// incarnation. On a returned creation failure, compensation attempts to release
// only that call's carve-out using a context detached from caller cancellation,
// bounded to five seconds. Release is refused while either child document of
// that incarnation remains. The original error identity is preserved; unexpected
// cleanup failures are joined to it. This is not crash-proof creation: process
// death before publication can leave a carve-out without any child documents.
// A later CreatePool uses a fresh incarnation and does not reclaim that space.
// The space remains reserved and therefore cannot overlap another allocation.
//
// Child deletion removes empty reservations first, releases the parent carve-out,
// then conditionally deletes the child pool document. The pool document retains
// the parent reference until release is confirmed, so a fresh client's DeletePool
// can finish after a failed release or final delete. Until then GetPool reports
// ErrNotFound and CreatePool reports pool_incomplete. A private deletion proof
// checks the child ID, incarnation, parent association and CIDR, and rechecks
// that no reservations of that incarnation exist before each parent CAS attempt.
// Only this deletion path permits the retained pool document during release;
// public Release is unchanged and refuses while either matching document exists.
//
// DeletePool deliberately never scans unrelated pools for abandoned carve-outs.
// With no surviving parent reference (including old leaks or process death
// before child publication), recovery is manual. An operator may release a
// carve-out explicitly by its complete
// pool:<child>:<incarnation> ID. Release refuses while a pool or reservations
// document of that child incarnation still exists. Releasing a carve-out is
// only safe when no CreatePool for that child can still be running: a creator
// between committing the carve-out and writing the child's reservations
// document cannot be detected, and would publish the child on released space.
//
// Each document chooses its schema version independently on every write. The
// pool document stores the allocation strategy: it is v1 when the strategy is
// lowest and v2 (with a strategy field) otherwise, so new pools, which default
// to best-fit, are v2. The reservations document stores the cooldown and the
// release records, a map from each released CIDR to its cooldown expiry taken
// from the writing client's clock; it is v1 when there is no cooldown and no
// release record, and v2 otherwise. Expired records are pruned on write.
// UpdatePoolPolicy writes the pool document and then the reservations document;
// the two writes are not atomic, and repeating the call completes a partial
// update.
package ipam
