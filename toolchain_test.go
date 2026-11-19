// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"os"
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

			for _, platform := range []string{"linux-x64", "macos-arm64", "windows-x64"} {
				if !strings.Contains(entry, "[tools.") || !strings.Contains(entry, ".platforms."+platform+"]") {
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

			jobs := regexp.MustCompile(`(?m)^    runs-on:`).FindAllStringIndex(workflow, -1)
			setups := regexp.MustCompile(`jdx/mise-action@[0-9a-f]{40} # v[0-9.]+`).FindAllStringIndex(workflow, -1)
			if len(jobs) == 0 || len(setups) != len(jobs) {
				t.Errorf("%s: every build job needs a SHA-pinned mise setup (%d jobs, %d setups)", path, len(jobs), len(setups))
			}

			if strings.Count(workflow, "cache: false") != len(jobs) {
				t.Errorf("%s: disable the setup cache in every job", path)
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
