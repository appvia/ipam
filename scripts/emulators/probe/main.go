// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Command probe verifies that the local storage emulators honour the
// conditional-write semantics the ipam storage backends rely on, and writes
// the result to .omo/evidence/emulators.json.
//
// S3 selection: start-s3.sh runs this command with -select-s3, which probes
// MinIO and then the in-process gofakes3 fallback and records the first one
// that passes every probe in s3.json. When none passes, s3.kind is "none": S3
// is not a selected emulator and S3 integration runs only against real AWS
// (IPAM_TEST_S3_BUCKET). An explicit -s3-endpoint is always a selected emulator.
//
// Known limitation (2026-10): no tested S3 emulator honours If-Match on
// DELETE. MinIO (final upstream commit, built from source) and gofakes3 v1.2.0
// both delete the object despite a stale ETag, so S3 conditional delete is only
// provable against real AWS via IPAM_TEST_S3_BUCKET.
//
// Exit status: 0 when Azurite and the selected S3 emulator, if any, pass every
// probe; 1 when any selected emulator fails a probe; 2 when the report cannot
// be written.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

const (
	devConnectionString = "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;" +
		"AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==;" +
		"BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;"
	minioDevKey = "minioadmin"
)

type options struct {
	stateDir       string
	out            string
	azureConn      string
	azureContainer string
	s3Endpoint     string
	s3Bucket       string
	s3Kind         string
	s3Fallback     string
	selectS3       bool
	repeat         int
	checkTimeout   time.Duration
}

type azuriteReport struct {
	OK           bool          `json:"ok"`
	Version      string        `json:"version"`
	Source       string        `json:"source"`
	Endpoint     string        `json:"endpoint"`
	FailedProbes []string      `json:"failed_probes"`
	Checks       []checkResult `json:"checks"`
}

type s3Attempt struct {
	Kind         string        `json:"kind"`
	Source       string        `json:"source"`
	Endpoint     string        `json:"endpoint"`
	OK           bool          `json:"ok"`
	FailedProbes []string      `json:"failed_probes"`
	Checks       []checkResult `json:"checks"`
}

type s3Report struct {
	Kind         string      `json:"kind"`
	Source       string      `json:"source"`
	Selected     bool        `json:"selected"`
	OK           bool        `json:"ok"`
	FailedProbes []string    `json:"failed_probes"`
	Attempts     []s3Attempt `json:"attempts"`
}

type report struct {
	GeneratedAt string        `json:"generated_at"`
	Repeat      int           `json:"repeat"`
	Azurite     azuriteReport `json:"azurite"`
	S3          s3Report      `json:"s3"`
}

func main() {
	os.Exit(run())
}

func run() int {
	opts := parseFlags()

	hc := &http.Client{Timeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(opts.repeat)*5*time.Minute)

	defer cancel()

	if opts.selectS3 {
		return runSelectS3(ctx, opts, hc)
	}

	rep := report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Repeat:      opts.repeat,
		Azurite:     probeAzurite(ctx, opts, hc),
		S3:          probeS3(ctx, opts, hc),
	}

	if err := writeJSON(opts.out, rep); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)

		return 2
	}

	fmt.Fprintf(os.Stderr, "report written to %s\n", opts.out)

	if !rep.S3.Selected {
		fmt.Fprintf(os.Stderr, "WARNING: no S3 emulator selected (failed probes %v); %s\n",
			rep.S3.FailedProbes, rep.S3.Source)
	}

	if !rep.Azurite.OK || (rep.S3.Selected && !rep.S3.OK) {
		fmt.Fprintf(os.Stderr, "FAIL: azurite ok=%t failed=%v; s3 kind=%s selected=%t ok=%t failed=%v\n",
			rep.Azurite.OK, rep.Azurite.FailedProbes, rep.S3.Kind, rep.S3.Selected, rep.S3.OK, rep.S3.FailedProbes)

		return 1
	}

	fmt.Fprintf(os.Stderr, "PASS: every selected emulator passed every probe (azurite %s, s3 kind=%s selected=%t)\n",
		rep.Azurite.Version, rep.S3.Kind, rep.S3.Selected)

	return 0
}

// runSelectS3 writes only the S3 selection to -out. It exits 0 whenever the
// selection completed, including when no emulator qualified (kind "none").
func runSelectS3(ctx context.Context, o options, hc *http.Client) int {
	var st s3State
	if err := readState(o.stateDir, "s3.json", &st); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)

		return 2
	}

	sel := selectS3(ctx, o, hc, st)
	if err := writeJSON(o.out, sel); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)

		return 2
	}

	return 0
}

func parseFlags() options {
	defaultState := os.Getenv("IPAM_EMULATOR_STATE_DIR")
	if defaultState == "" {
		defaultState = filepath.Join(os.TempDir(), "ipam-emulators")
	}

	var o options

	flag.StringVar(&o.stateDir, "state-dir", defaultState, "directory holding azurite.json and s3.json from the start scripts")
	flag.StringVar(&o.out, "out", filepath.Join(".omo", "evidence", "emulators.json"), "report path")
	flag.StringVar(&o.azureConn, "azure-connection-string", "", "override the Azurite connection string")
	flag.StringVar(&o.azureContainer, "azure-container", "ipam-probe", "container used by the probe")
	flag.StringVar(&o.s3Endpoint, "s3-endpoint", "", "override the S3 endpoint from s3.json")
	flag.StringVar(&o.s3Bucket, "s3-bucket", "", "override the S3 bucket from s3.json")
	flag.StringVar(&o.s3Kind, "s3-kind", "", "override the S3 emulator kind recorded in the report")
	flag.StringVar(&o.s3Fallback, "s3-fallback", "gofakes3", "fallback when the S3 emulator fails: gofakes3 or none")
	flag.BoolVar(&o.selectS3, "select-s3", false, "only select the S3 emulator and write the selection to -out")
	flag.IntVar(&o.repeat, "repeat", 1, "run every probe this many times; any failure fails the probe")
	flag.DurationVar(&o.checkTimeout, "check-timeout", time.Minute, "deadline for each individual probe")
	flag.Parse()

	if o.repeat < 1 {
		o.repeat = 1
	}

	return o
}

type azuriteState struct {
	Version          string `json:"version"`
	Source           string `json:"source"`
	ConnectionString string `json:"connection_string"`
}

type s3State struct {
	Kind         string      `json:"kind"`
	Source       string      `json:"source"`
	Endpoint     string      `json:"endpoint"`
	Bucket       string      `json:"bucket"`
	Selected     *bool       `json:"selected"`
	FailedProbes []string    `json:"failed_probes"`
	Attempts     []s3Attempt `json:"attempts"`
}

func readState(dir, name string, v any) error {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}

	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}

	return nil
}

func probeAzurite(ctx context.Context, o options, hc *http.Client) azuriteReport {
	var st azuriteState
	if err := readState(o.stateDir, "azurite.json", &st); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return azuriteReport{FailedProbes: []string{"setup"}, Checks: []checkResult{{Name: "setup", Detail: err.Error()}}}
	}

	conn := firstNonEmpty(o.azureConn, st.ConnectionString, devConnectionString)
	rep := azuriteReport{Source: firstNonEmpty(st.Source, "connection string"), Endpoint: blobEndpoint(conn)}

	if o.azureConn != "" {
		rep.Source = "connection string override"
	}

	version, err := serverVersion(ctx, hc, rep.Endpoint)
	if err != nil {
		version = "unknown (" + err.Error() + ")"
	}

	rep.Version = version

	store, err := newAzureStore(conn, o.azureContainer, hc)
	if err == nil {
		err = store.ensureContainer(ctx)
	}

	if err != nil {
		rep.FailedProbes = []string{"setup"}
		rep.Checks = []checkResult{{Name: "setup", Detail: err.Error()}}

		return rep
	}

	rep.Checks = repeatChecks(ctx, store, o)
	rep.FailedProbes = failed(rep.Checks)
	rep.OK = len(rep.FailedProbes) == 0

	return rep
}

const noS3Source = "S3 integration runs only against real AWS (IPAM_TEST_S3_BUCKET)"

// probeS3 verifies the S3 emulator: an explicit -s3-endpoint, else the
// selection start-s3.sh recorded in s3.json, else a fresh selection.
func probeS3(ctx context.Context, o options, hc *http.Client) s3Report {
	var st s3State

	if err := readState(o.stateDir, "s3.json", &st); err != nil && !errors.Is(err, fs.ErrNotExist) {
		setup := s3Attempt{Kind: "minio", Source: "s3.json", FailedProbes: []string{"setup"},
			Checks: []checkResult{{Name: "setup", Detail: err.Error()}}}

		return s3Report{Kind: "minio", Source: "s3.json", Selected: true, FailedProbes: setup.FailedProbes,
			Attempts: []s3Attempt{setup}}
	}

	bucket := firstNonEmpty(o.s3Bucket, st.Bucket, "ipam-test")

	if o.s3Endpoint != "" {
		a := probeS3Endpoint(ctx, o, hc, s3Attempt{
			Kind:     firstNonEmpty(o.s3Kind, "custom"),
			Source:   "endpoint override " + o.s3Endpoint,
			Endpoint: o.s3Endpoint,
		}, bucket)

		return selectedReport(a, nil)
	}

	if st.Selected == nil {
		return selectS3(ctx, o, hc, st)
	}

	if !*st.Selected {
		return s3Report{Kind: "none", Source: firstNonEmpty(st.Source, noS3Source),
			FailedProbes: st.FailedProbes, Attempts: st.Attempts}
	}

	var a s3Attempt
	if st.Kind == "gofakes3" {
		a = probeGofakes3(ctx, o, hc, bucket)
	} else {
		a = probeS3Endpoint(ctx, o, hc, s3Attempt{Kind: st.Kind, Source: st.Source, Endpoint: st.Endpoint}, bucket)
	}

	var rejected []s3Attempt

	for _, prev := range st.Attempts {
		if !prev.OK {
			rejected = append(rejected, prev)
		}
	}

	return selectedReport(a, rejected)
}

func selectedReport(a s3Attempt, rejected []s3Attempt) s3Report {
	return s3Report{Kind: a.Kind, Source: a.Source, Selected: true, OK: a.OK,
		FailedProbes: a.FailedProbes, Attempts: append(rejected, a)}
}

// selectS3 probes the S3 emulator recorded in s3.json, then the gofakes3
// fallback, and selects the first that passes every probe.
func selectS3(ctx context.Context, o options, hc *http.Client, st s3State) s3Report {
	bucket := firstNonEmpty(o.s3Bucket, st.Bucket, "ipam-test")
	primary := s3Attempt{Kind: firstNonEmpty(st.Kind, "minio"), Source: st.Source, Endpoint: st.Endpoint}

	if primary.Endpoint == "" {
		primary.Checks = []checkResult{{Name: "setup", Detail: "no S3 emulator endpoint in s3.json (" + st.Source + ")"}}
		primary.FailedProbes = []string{"setup"}
	} else {
		primary = probeS3Endpoint(ctx, o, hc, primary, bucket)
	}

	attempts := []s3Attempt{primary}

	if !primary.OK && o.s3Fallback == "gofakes3" {
		attempts = append(attempts, probeGofakes3(ctx, o, hc, bucket))
	}

	for _, a := range attempts {
		if a.OK {
			return s3Report{Kind: a.Kind, Source: a.Source, Selected: true, OK: true,
				FailedProbes: []string{}, Attempts: attempts}
		}
	}

	failedProbes := []string{}
	seen := map[string]bool{}

	for _, a := range attempts {
		for _, p := range a.FailedProbes {
			if !seen[p] {
				seen[p] = true
				failedProbes = append(failedProbes, p)
			}
		}
	}

	return s3Report{Kind: "none", Source: noS3Source, FailedProbes: failedProbes, Attempts: attempts}
}

func probeS3Endpoint(ctx context.Context, o options, hc *http.Client, a s3Attempt, bucket string) s3Attempt {
	store := newS3Store(a.Endpoint, bucket, minioDevKey, minioDevKey, hc)
	if err := store.ensureBucket(ctx); err != nil {
		a.Checks = []checkResult{{Name: "setup", Detail: err.Error()}}
		a.FailedProbes = []string{"setup"}

		return a
	}

	a.Checks = repeatChecks(ctx, store, o)
	a.FailedProbes = failed(a.Checks)
	a.OK = len(a.FailedProbes) == 0

	return a
}

func probeGofakes3(ctx context.Context, o options, hc *http.Client, bucket string) s3Attempt {
	srv := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	defer srv.Close()

	a := s3Attempt{
		Kind:     "gofakes3",
		Source:   "in-process github.com/johannesboyne/gofakes3@" + moduleVersion("github.com/johannesboyne/gofakes3"),
		Endpoint: srv.URL,
	}

	return probeS3Endpoint(ctx, o, hc, a, bucket)
}

func repeatChecks(ctx context.Context, s store, o options) []checkResult {
	var agg []checkResult

	for i := range o.repeat {
		results := runChecks(ctx, s, runPrefix(), o.checkTimeout)
		if agg == nil {
			agg = make([]checkResult, len(results))
			for j, r := range results {
				agg[j] = checkResult{Name: r.Name, OK: true}
			}
		}

		for j, r := range results {
			if !r.OK && agg[j].OK {
				agg[j].OK = false
				agg[j].Detail = fmt.Sprintf("run %d/%d: %s", i+1, o.repeat, r.Detail)
			}
		}
	}

	return agg
}

func failed(results []checkResult) []string {
	out := []string{}

	for _, r := range results {
		if !r.OK {
			out = append(out, r.Name)
		}
	}

	return out
}

// runPrefix gives every run fresh keys so leftovers from earlier runs never matter.
func runPrefix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error.

	return fmt.Sprintf("probe-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

func blobEndpoint(conn string) string {
	for part := range strings.SplitSeq(conn, ";") {
		if k, v, ok := strings.Cut(part, "="); ok && strings.EqualFold(k, "BlobEndpoint") {
			return v
		}
	}

	return ""
}

func moduleVersion(path string) string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == path {
				return dep.Version
			}
		}
	}

	return "unknown"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}

	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}
