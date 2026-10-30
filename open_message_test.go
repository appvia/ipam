// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"strings"
	"testing"
)

// A '//' in a backend URL path is rejected; the message must say why and how
// to fix the usual cause (a variable such as $TMPDIR ending in '/'), without
// echoing any part of the URL.
func TestOpenEmptySegmentMessageIsActionable(t *testing.T) {
	t.Parallel()

	const marker = "zqxsegmentmarker"

	cases := []struct{ name, url string }{
		{"file trailing slash variable", "file:///var/folders/T//" + marker},
		{"s3 double slash", "s3://bkt/" + marker + "//p"},
		{"azblob double slash", "azblob://cnt/" + marker + "//p"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := OpenStorage(t.Context(), tc.url)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("OpenStorage() error = %v, want ErrInvalidArgument", err)
			}

			msg := err.Error()
			for _, want := range []string{"empty segment ('//')", "trailing slash", "$TMPDIR", "${TMPDIR%/}"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not contain %q", msg, want)
				}
			}

			for _, leak := range []string{marker, "var/folders", "bkt", "cnt"} {
				if strings.Contains(msg, leak) {
					t.Errorf("message %q echoes URL part %q", msg, leak)
				}
			}
		})
	}
}

// Dot segments are a different mistake; they must not get the trailing-slash
// hint.
func TestOpenDotSegmentMessageHasNoTrailingSlashHint(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"file:///tmp/../x", "file:///tmp/./x", "s3://bkt/p/%2e%2e/q"} {
		_, err := OpenStorage(t.Context(), raw)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("OpenStorage(%q) error = %v, want ErrInvalidArgument", raw, err)
		}

		if strings.Contains(err.Error(), "TMPDIR") {
			t.Errorf("OpenStorage(%q) message %q carries the trailing-slash hint", raw, err.Error())
		}
	}
}
