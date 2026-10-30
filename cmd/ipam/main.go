// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Command ipam manages IPAM pools and allocations in a storage backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/appvia/ipam"
	"github.com/appvia/ipam/storage"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	exitOK        = 0
	exitError     = 1
	exitUsage     = 2
	exitNotFound  = 3
	exitConflict  = 4
	exitExhausted = 5
	// exitDoctorFailed is returned by 'ipam doctor' when any check failed.
	exitDoctorFailed = 6
)

type openFunc func(ctx context.Context, rawURL string) (*ipam.Client, error)

type openStorageFunc func(ctx context.Context, rawURL string) (storage.Storage, error)

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv, openClient)

	stop()
	os.Exit(code)
}

func openClient(ctx context.Context, rawURL string) (*ipam.Client, error) {
	return ipam.Open(ctx, rawURL)
}

// run executes the CLI and returns the process exit code. Errors raised before
// a command body starts (unknown commands, bad flags, wrong argument counts)
// are usage errors.
func run(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	getenv func(string) string,
	open openFunc,
) int {
	return runCLI(ctx, args, stdout, stderr, getenv, open, ipam.OpenStorage)
}

// runCLI is run with the raw storage opener used by 'ipam doctor' injectable.
func runCLI(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	getenv func(string) string,
	open openFunc,
	openStorage openStorageFunc,
) int {
	cli := &cli{stdout: stdout, getenv: getenv, open: open, openStorage: openStorage}
	defer cli.closeAll()

	root := cli.rootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}

	code := exitCode(err)
	if !cli.started {
		code = exitUsage
	}

	message := err.Error()
	if !strings.HasPrefix(message, "ipam: ") {
		message = "ipam: " + message
	}

	_, _ = fmt.Fprintln(stderr, message)
	if code == exitUsage {
		_, _ = fmt.Fprintln(stderr, "Run 'ipam --help' for usage.")
	}

	return code
}

func exitCode(err error) int {
	var (
		usage  *usageError
		doctor *doctorFailedError
	)

	switch {
	case errors.As(err, &doctor):
		return exitDoctorFailed
	case errors.As(err, &usage),
		errors.Is(err, ipam.ErrInvalidArgument),
		errors.Is(err, ipam.ErrInvalidCIDR):
		return exitUsage
	case errors.Is(err, ipam.ErrNotFound):
		return exitNotFound
	case errors.Is(err, ipam.ErrPoolExhausted):
		return exitExhausted
	case errors.Is(err, ipam.ErrConflict):
		return exitConflict
	default:
		return exitError
	}
}
