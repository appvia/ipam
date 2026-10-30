// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/appvia/ipam/internal/document"
)

func encodePoolDocument(stored poolDocument) ([]byte, error) {
	pool, err := canonicalPool(stored.Pool)
	if err != nil {
		return nil, fmt.Errorf("encode pool document: %w", err)
	}

	if !validIncarnation(stored.Incarnation) {
		return nil, fmt.Errorf("encode pool document incarnation: %w", ErrInvalidArgument)
	}

	cidrs := make([]string, len(pool.CIDRs))
	for index, prefix := range pool.CIDRs {
		cidrs[index] = prefix.String()
	}

	var parent *document.Parent
	if pool.Parent != nil {
		parent = &document.Parent{
			PoolID:       pool.Parent.PoolID,
			AllocationID: pool.Parent.AllocationID,
		}
	}

	data, err := document.EncodePool(document.Pool{
		SchemaVersion: 1,
		Kind:          "pool",
		PoolID:        pool.ID,
		Incarnation:   stored.Incarnation,
		Family:        pool.Family,
		CIDRs:         cidrs,
		Parent:        parent,
		Description:   pool.Description,
		Labels:        cloneLabels(pool.Labels),
	})
	if err != nil {
		return nil, mapDocumentError("encode pool document", err)
	}

	return data, nil
}

func decodePoolDocument(data []byte, expectedPoolID string) (poolDocument, error) {
	stored, err := document.DecodePool(data, expectedPoolID)
	if err != nil {
		return poolDocument{}, mapDocumentError("decode pool document", err)
	}

	cidrs := make([]netip.Prefix, 0, len(stored.CIDRs))
	for _, raw := range stored.CIDRs {
		prefix, parseErr := canonicalPrefix(raw)
		if parseErr != nil {
			return poolDocument{}, fmt.Errorf("decode pool document CIDR: %w", parseErr)
		}

		cidrs = append(cidrs, prefix)
	}

	pool := Pool{
		ID:          stored.PoolID,
		Family:      stored.Family,
		CIDRs:       cidrs,
		Description: stored.Description,
		Labels:      cloneLabels(stored.Labels),
	}
	if stored.Parent != nil {
		pool.Parent = &ParentRef{
			PoolID:       stored.Parent.PoolID,
			AllocationID: stored.Parent.AllocationID,
		}
	}

	canonical, err := canonicalPool(pool)
	if err != nil {
		return poolDocument{}, fmt.Errorf("decode pool document: %w", err)
	}

	if !validIncarnation(stored.Incarnation) {
		return poolDocument{}, fmt.Errorf("decode pool document incarnation: %w", ErrInvalidArgument)
	}

	return poolDocument{
		Pool:        canonical,
		Incarnation: stored.Incarnation,
	}, nil
}

func encodeReservationsDocument(stored reservationsDocument) ([]byte, error) {
	if err := validatePoolID(stored.PoolID); err != nil {
		return nil, fmt.Errorf("encode reservations document: %w", err)
	}

	if !validIncarnation(stored.Incarnation) {
		return nil, fmt.Errorf("encode reservations document incarnation: %w", ErrInvalidArgument)
	}

	allocations := make(map[string]document.Allocation, len(stored.Allocations))
	for key, allocation := range stored.Allocations {
		if key != allocation.ID {
			return nil, fmt.Errorf("encode allocation key %q: %w", key, ErrInvalidArgument)
		}

		if err := validateAllocationID(allocation.ID); err != nil {
			return nil, err
		}

		request, err := requestToDocument(allocation.Request)
		if err != nil {
			return nil, fmt.Errorf("encode allocation %q: %w", allocation.ID, err)
		}

		cidr, err := canonicalNetPrefix(allocation.CIDR)
		if err != nil || cidr != allocation.CIDR {
			return nil, fmt.Errorf("encode allocation %q CIDR: %w", allocation.ID, ErrInvalidCIDR)
		}

		allocations[key] = document.Allocation{
			Request:     request,
			CIDR:        cidr.String(),
			Description: allocation.Description,
			Labels:      cloneLabels(allocation.Labels),
			CreatedAt:   allocation.CreatedAt,
		}
	}

	data, err := document.EncodeReservations(document.Reservations{
		SchemaVersion: 1,
		Kind:          "reservations",
		PoolID:        stored.PoolID,
		Incarnation:   stored.Incarnation,
		Allocations:   allocations,
	})
	if err != nil {
		return nil, mapDocumentError("encode reservations document", err)
	}

	return data, nil
}

func decodeReservationsDocument(data []byte, expectedPoolID string) (reservationsDocument, error) {
	stored, err := document.DecodeReservations(data, expectedPoolID)
	if err != nil {
		return reservationsDocument{}, mapDocumentError("decode reservations document", err)
	}

	if err := validatePoolID(stored.PoolID); err != nil {
		return reservationsDocument{}, fmt.Errorf("decode reservations document: %w", err)
	}

	if !validIncarnation(stored.Incarnation) {
		return reservationsDocument{}, fmt.Errorf("decode reservations document incarnation: %w", ErrInvalidArgument)
	}

	allocations := make(map[string]Allocation, len(stored.Allocations))
	for id, storedAllocation := range stored.Allocations {
		if err := validateAllocationID(id); err != nil {
			return reservationsDocument{}, err
		}

		cidr, err := canonicalPrefix(storedAllocation.CIDR)
		if err != nil || cidr.String() != storedAllocation.CIDR {
			return reservationsDocument{}, fmt.Errorf("decode allocation %q CIDR: %w", id, ErrInvalidCIDR)
		}

		request, err := requestFromDocument(storedAllocation.Request)
		if err != nil {
			return reservationsDocument{}, fmt.Errorf("decode allocation %q request: %w", id, err)
		}

		allocations[id] = Allocation{
			ID:          id,
			PoolID:      stored.PoolID,
			Request:     request,
			CIDR:        cidr,
			Description: storedAllocation.Description,
			Labels:      cloneLabels(storedAllocation.Labels),
			CreatedAt:   storedAllocation.CreatedAt,
		}
	}

	return reservationsDocument{
		PoolID:      stored.PoolID,
		Incarnation: stored.Incarnation,
		Allocations: allocations,
	}, nil
}

func requestToDocument(request Request) (document.Request, error) {
	switch request.Kind {
	case RequestNext:
		if request.PrefixLength < 0 || request.PrefixLength > 128 || request.CIDR.IsValid() {
			return document.Request{}, fmt.Errorf("invalid next request: %w", ErrInvalidArgument)
		}

		return document.Request{Kind: string(RequestNext), PrefixLength: request.PrefixLength}, nil
	case RequestExplicit:
		cidr, err := canonicalNetPrefix(request.CIDR)
		if err != nil || cidr != request.CIDR || request.PrefixLength != 0 {
			return document.Request{}, fmt.Errorf("invalid explicit request: %w", ErrInvalidCIDR)
		}

		return document.Request{Kind: string(RequestExplicit), CIDR: cidr.String()}, nil
	default:
		return document.Request{}, fmt.Errorf("request kind %q: %w", request.Kind, ErrInvalidArgument)
	}
}

func requestFromDocument(stored document.Request) (Request, error) {
	switch RequestKind(stored.Kind) {
	case RequestNext:
		if stored.PrefixLength < 0 || stored.PrefixLength > 128 || stored.CIDR != "" {
			return Request{}, fmt.Errorf("invalid next request: %w", ErrInvalidArgument)
		}

		return Request{Kind: RequestNext, PrefixLength: stored.PrefixLength}, nil
	case RequestExplicit:
		cidr, err := canonicalPrefix(stored.CIDR)
		if err != nil || cidr.String() != stored.CIDR || stored.PrefixLength != 0 {
			return Request{}, fmt.Errorf("invalid explicit request: %w", ErrInvalidCIDR)
		}

		return Request{Kind: RequestExplicit, CIDR: cidr}, nil
	default:
		return Request{}, fmt.Errorf("request kind %q: %w", stored.Kind, ErrInvalidArgument)
	}
}

func mapDocumentError(operation string, err error) error {
	switch {
	case errors.Is(err, document.ErrUnsupportedSchema):
		return fmt.Errorf("%s: %w", operation, ErrUnsupportedSchema)
	case errors.Is(err, document.ErrInvalidDocument):
		return fmt.Errorf("%s: %w", operation, ErrInvalidArgument)
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
}
