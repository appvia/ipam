// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"os"
	"strings"
	"testing"
)

const supportedBackendsURL = "https://github.com/appvia/ipam#supported-backends"

func TestSupportedBackendsDocumentation(t *testing.T) {
	readme := readDocumentation(t, "README.md")
	support := markdownSection(t, readme, "## Supported backends")

	t.Run("AC-19: support policy and consent", func(t *testing.T) {
		for _, phrase := range []string{
			"`s3://<bucket>/<prefix>` with no `endpoint=` override | **Verified**",
			"S3 with an endpoint hostname matching `*.amazonaws.com` or `*.amazonaws.com.cn` | **Verified**",
			"`azblob://<account>.blob.core.windows.net/<container>/<prefix>` | **Verified**",
			"`file://` or `mem://` | **Verified**",
			"S3 with any other endpoint hostname | **Unverified**",
			"`azblob://<container>/<prefix>` | **Unverified**",
			"`--allow-unverified-backend`",
			"`IPAM_ALLOW_UNVERIFIED_BACKEND=true`",
			"`--allow-unverified-backend=false` takes precedence over the environment",
			"`WithAllowUnverifiedBackend`",
			"`Open` and `OpenStorage`",
			"`ClassifyBackend(rawURL)`",
			"loopback",
			"Operators are responsible for evaluating the endpoint",
			"storage contract suite against it, including conditional deletion",
			"does not prove an endpoint is safe",
		} {
			requireDocumentation(t, support, phrase)
		}
	})

	t.Run("AC-20: conditional-write safety", func(t *testing.T) {
		for _, phrase := range []string{
			"Conditional creation must reject an existing object",
			"Conditional replacement and deletion must reject a version mismatch",
			"concurrent writers can corrupt pool and reservation documents",
			"Amazon S3 is the reference target and the only verified S3 service",
			"Doctor cannot prove conditional deletion",
			"can change data on a non-enforcing backend",
		} {
			requireDocumentation(t, support, phrase)
		}
	})
}

func TestUnverifiedBackendMigrationDocumentation(t *testing.T) {
	changelog := readDocumentation(t, "CHANGELOG.md")

	unreleased := markdownSection(t, changelog, "## [Unreleased]")
	for _, phrase := range []string{
		"Breaking change",
		"unverified custom endpoints",
		"`--allow-unverified-backend`",
		"`IPAM_ALLOW_UNVERIFIED_BACKEND=true`",
		"`WithAllowUnverifiedBackend`",
		"do not make an endpoint safe or verified",
	} {
		requireDocumentation(t, unreleased, phrase)
	}
}

func TestBackendSupportLinks(t *testing.T) {
	readme := readDocumentation(t, "README.md")

	backendURLs := markdownSection(t, readme, "## Backend URLs and credentials")
	if strings.Count(readme, "## Supported backends") != 1 {
		t.Fatal("README must have exactly one Supported backends section")
	}

	requireDocumentation(t, backendURLs, supportedBackendsURL)

	s3Source := readDocumentation(t, "storage/s3/s3.go")

	start := strings.Index(s3Source, "// Endpoint overrides")
	if start < 0 {
		t.Fatal("missing S3 Endpoint documentation comment")
	}

	end := strings.Index(s3Source[start:], "\n\tEndpoint string")
	if end < 0 {
		t.Fatal("could not find the end of the S3 Endpoint documentation comment")
	}

	endpointComment := s3Source[start : start+end]
	requireDocumentation(t, endpointComment, supportedBackendsURL)
}

func readDocumentation(t *testing.T, path string) string {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(contents)
}

func markdownSection(t *testing.T, markdown, heading string) string {
	t.Helper()

	start := strings.Index(markdown, heading)
	if start < 0 {
		t.Fatalf("missing heading %q", heading)
	}

	section := markdown[start:]

	if next := strings.Index(section[len(heading):], "\n## "); next >= 0 {
		section = section[:len(heading)+next]
	}

	return section
}

func requireDocumentation(t *testing.T, text, phrase string) {
	t.Helper()

	if !strings.Contains(strings.Join(strings.Fields(text), " "), phrase) {
		t.Errorf("documentation is missing %q", phrase)
	}
}
