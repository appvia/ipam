# Copyright 2026 Appvia Ltd.
# SPDX-License-Identifier: Apache-2.0

BINARY := ipam
VERSION ?= dev

.PHONY: build test test-integration lint license-headers check-license-headers test-scripts

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/ipam

test:
	go test ./... -count=1 -race -timeout 20m

test-integration:
	go test -tags integration ./... -count=1

lint:
	golangci-lint run
	./scripts/license-headers.sh --check

license-headers:
	./scripts/license-headers.sh

check-license-headers:
	./scripts/license-headers.sh --check

test-scripts:
	./scripts/test-license-headers.sh
