# Copyright 2026 Appvia Ltd.
# SPDX-License-Identifier: Apache-2.0

BINARY := ipam
VERSION ?= dev

.PHONY: build test test-integration lint

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/ipam

test:
	go test ./... -count=1 -race -timeout 20m

test-integration:
	go test -tags integration ./... -count=1

lint:
	golangci-lint run
