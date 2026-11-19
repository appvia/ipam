// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestConstitutionCIII4Toolchain(t *testing.T) {
	t.Run("C-III.4: exact tools locked for every CI platform", func(t *testing.T) {
		manifest := readToolchainFile(t, "mise.toml")
		lock := readToolchainFile(t, "mise.lock")

		pins := regexp.MustCompile(`(?m)^([a-z][a-z0-9-]*|"[^"]+") = "([^"\n]+)"$`).FindAllStringSubmatch(manifest, -1)
		if len(pins) == 0 {
			t.Fatal("mise.toml must declare exact tool versions")
		}

		for _, tool := range []string{"go", "golangci-lint", "goreleaser", "jq", "node", `"npm:azurite"`, `"go:github.com/minio/minio"`} {
			if !strings.Contains(manifest, "\n"+tool+" = ") {
				t.Errorf("mise.toml must declare the CI tool %s", tool)
			}
		}

		for _, pin := range pins {
			tool := strings.Trim(pin[1], `"`)
			if !regexp.MustCompile(`^v?\d+\.\d+\.\d+([+-][A-Za-z0-9.-]+)?$`).MatchString(pin[2]) {
				t.Errorf("%s must have an exact version, got %q", tool, pin[2])
			}

			section := "[[tools." + tool + "]]"
			if strings.Contains(tool, ":") || strings.Contains(tool, "/") {
				section = `[[tools."` + tool + `"]]`
			}

			_, entry, found := strings.Cut(lock, section)
			if !found {
				t.Errorf("mise.lock has no entry for %s", tool)
				continue
			}

			entry, _, _ = strings.Cut(entry, "[[tools.")
			if !strings.Contains(entry, `version = "`+pin[2]+`"`) {
				t.Errorf("mise.lock does not match %s's exact pin", tool)
			}

			if strings.HasPrefix(tool, "go:") {
				if !strings.Contains(entry, `backend = "`+tool+`"`) {
					t.Errorf("%s must use the checksum-verifying Go module backend", tool)
				}

				continue
			}

			if pkgName, ok := strings.CutPrefix(tool, "npm:"); ok {
				sidecar := filepath.Join(".mise", "locks", "npm-"+pkgName, pin[2])
				if !strings.Contains(entry, `path = "`+filepath.ToSlash(sidecar)+`"`) || !strings.Contains(entry, `digest = "sha256:`) {
					t.Errorf("%s must lock its dependency graph with a content digest", tool)
				}

				var pkg struct {
					Dependencies map[string]string `json:"dependencies"`
				}

				if err := json.Unmarshal([]byte(readToolchainFile(t, filepath.Join(sidecar, "package.json"))), &pkg); err != nil {
					t.Fatal(err)
				}

				if pkg.Dependencies[pkgName] != pin[2] {
					t.Errorf("%s sidecar must match the mise pin", tool)
				}

				readToolchainFile(t, filepath.Join(sidecar, "aube-lock.yaml"))

				continue
			}

			for _, platform := range []string{"linux-x64", "linux-arm64", "macos-arm64", "macos-x64", "windows-x64"} {
				_, artifact, found := strings.Cut(entry, `"platforms.`+platform+`"]`)

				artifact, _, _ = strings.Cut(artifact, "\n[tools.")
				if !found || !strings.Contains(artifact, `checksum = "sha256:`) || !strings.Contains(artifact, `url = "https://`) {
					t.Errorf("%s is not locked for CI platform %s", tool, platform)
				}
			}
		}
	})

	t.Run("C-III.4: CI installs exclusively through locked mise", func(t *testing.T) {
		for _, path := range []string{".github/workflows/test.yml", ".github/workflows/release.yml"} {
			workflow := readToolchainFile(t, path)
			for _, forbidden := range []string{"actions/setup-", "golangci/golangci-lint-action@", "goreleaser/goreleaser-action@", "go-version", "version: v2."} {
				if strings.Contains(workflow, forbidden) {
					t.Errorf("%s installs or pins tools outside mise: %s", path, forbidden)
				}
			}

			for _, required := range []string{`MISE_LOCKED: "1"`, "GOTOOLCHAIN: local"} {
				if !strings.Contains(workflow, required) {
					t.Errorf("%s is missing %s", path, required)
				}
			}

			_, jobs, found := strings.Cut(workflow, "\njobs:\n")
			if !found {
				t.Fatalf("%s must define CI jobs", path)
			}

			starts := regexp.MustCompile(`(?m)^  [A-Za-z0-9_-]+:\n`).FindAllStringIndex(jobs, -1)
			if len(starts) == 0 {
				t.Fatalf("%s has no build jobs", path)
			}

			for i, start := range starts {
				end := len(jobs)
				if i+1 < len(starts) {
					end = starts[i+1][0]
				}

				job := jobs[start[0]:end]

				setups := regexp.MustCompile(`jdx/mise-action@[0-9a-f]{40} # v[0-9.]+`).FindAllStringIndex(job, -1)
				if len(setups) != 1 || strings.Count(job, "cache: false") != 1 {
					t.Errorf("%s job %s needs one SHA-pinned mise setup with caching disabled", path, strings.TrimSpace(jobs[start[0]:start[1]]))
				}

				if strings.Contains(job, "install_args:") && regexp.MustCompile(`install_args:.*@`).MatchString(job) {
					t.Errorf("%s must not pin tool versions in install_args", path)
				}
			}
		}
	})

	t.Run("C-III.4: emulator launchers do not install tools", func(t *testing.T) {
		for _, path := range []string{"scripts/emulators/start-azurite.sh", "scripts/emulators/start-s3.sh"} {
			script := readToolchainFile(t, path)
			for _, forbidden := range []string{"npx ", "go install ", "brew install ", "curl -fsSL -o", "IPAM_AZURITE_VERSION", "IPAM_MINIO_GO_VERSION"} {
				if strings.Contains(script, forbidden) {
					t.Errorf("%s installs or pins tools outside mise: %s", path, forbidden)
				}
			}
		}
	})
}

func readToolchainFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}
