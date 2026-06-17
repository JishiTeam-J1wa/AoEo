// main_integration_test.go provides subprocess-based integration tests for the
// AoEo CLI. It builds the binary once (with coverage instrumentation) and runs it
// with various arguments to exercise main(), loadClient(), and every cmd* function.
//
// This approach is necessary because the CLI functions call os.Exit(), which cannot
// be intercepted in-process. Go 1.20+ integration coverage is captured via
// GOCOVERDIR and merged into the final profile.
//
// Coverage workflow:
//
//	go test ./cmd/aoeo/ -coverprofile=cover.out -count=1
//	# Integration coverage is auto-merged into /tmp/aoeo_combined_coverage.out
//	# To view combined: go tool cover -func=/tmp/aoeo_combined_coverage.out
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Binary build (once per test run via TestMain)
// ---------------------------------------------------------------------------

var (
	integrationBinary string
	integrationOnce   sync.Once
	coverageDir       string
)

// buildBinary lazily compiles the AoEo CLI with -cover so that integration
// coverage data is written to coverageDir on each subprocess exit.
func buildBinary(t *testing.T) string {
	t.Helper()
	integrationOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "aoeo-integ-bin-*")
		if err != nil {
			panic(fmt.Sprintf("integration: mkdir temp: %v", err))
		}

		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		integrationBinary = filepath.Join(tmpDir, "aoeo"+ext)

		// Directory for integration coverage data (Go 1.20+ feature).
		coverageDir, err = os.MkdirTemp("", "aoeo-integ-cov-*")
		if err != nil {
			os.RemoveAll(tmpDir)
			panic(fmt.Sprintf("integration: mkdir cov: %v", err))
		}

		// Build with -cover for integration coverage collection.
		cmd := exec.Command("go", "build", "-cover", "-o", integrationBinary, ".")
		cmd.Dir = filepath.Join(projectRoot(), "cmd", "aoeo")
		if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
			os.RemoveAll(tmpDir)
			os.RemoveAll(coverageDir)
			panic(fmt.Sprintf("integration: build failed: %v\n%s", buildErr, out))
		}
	})
	return integrationBinary
}

// projectRoot returns the repository root (two levels above cmd/aoeo).
func projectRoot() string {
	// When running via "go test ./cmd/aoeo/", the working directory is the
	// package directory itself.  Walk up two levels to reach the module root.
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// ---------------------------------------------------------------------------
// TestMain: build binary, run tests, merge coverage, clean up.
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	code := m.Run()

	// Merge integration coverage into the text profile (best-effort).
	if coverageDir != "" {
		mergeCoverage()
	}

	// Clean up temporary directories.
	if integrationBinary != "" {
		os.RemoveAll(filepath.Dir(integrationBinary))
	}
	if coverageDir != "" {
		os.RemoveAll(coverageDir)
	}

	os.Exit(code)
}

// mergeCoverage converts the binary coverage data in coverageDir to a text
// profile and, when a unit-test coverprofile exists, merges the two into a
// single combined profile.  All output goes to os.TempDir() so it survives
// after TestMain cleans up the temp directories.
//
// Output files (best-effort; silently skipped on failure):
//
//	<os.TempDir()>/aoeo_integration_coverage.out  – text profile from integration runs
//	<os.TempDir()>/aoeo_combined_coverage.out     – merged unit + integration profile
func mergeCoverage() {
	root := projectRoot()
	integOut := filepath.Join(os.TempDir(), "aoeo_integration_coverage.out")

	// Step 1: Convert binary coverage data from subprocess runs to text format.
	// Must run from the module root so that go tool can resolve the module.
	convert := exec.Command("go", "tool", "covdata", "textfmt",
		"-i", coverageDir,
		"-o", integOut,
	)
	convert.Dir = root
	if out, err := convert.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: covdata textfmt failed: %v\n%s\n", err, out)
		return
	}

	// Step 2: If a unit-test coverprofile was requested, merge it with the
	// integration profile.  go test writes the coverprofile to the path given
	// by -coverprofile; we scan for common names in the working directory and
	// module root.
	unitProfile := findUnitCoverProfile()
	if unitProfile == "" {
		return
	}

	combined := filepath.Join(os.TempDir(), "aoeo_combined_coverage.out")
	mergeCoverProfiles(unitProfile, integOut, combined)
}

// findUnitCoverProfile looks for a coverprofile produced by "go test
// -coverprofile=..." in the current or parent directories.
func findUnitCoverProfile() string {
	root := projectRoot()
	candidates := []string{
		filepath.Join(root, "cover.out"),
		filepath.Join(root, "coverage.out"),
		"cover.out",
		"coverage.out",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// mergeCoverProfiles merges two text-format coverage profiles into one.
// Both files must share the same "mode:" header (typically "set" or "count").
// When a block appears in both profiles the counts are summed.
func mergeCoverProfiles(unit, integ, out string) {
	unitData, err := os.ReadFile(unit)
	if err != nil {
		return
	}
	integData, err := os.ReadFile(integ)
	if err != nil {
		return
	}

	mode, unitBody := splitCoverMode(unitData)
	_, integBody := splitCoverMode(integData)

	// Merge lines: use the block coordinate string as key (file:start.end).
	type entry struct {
		numStmt int
		count   int
	}
	merged := make(map[string]entry)

	parseLines := func(body string) {
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Format: <file>:<startLine>.<startCol>,<endLine>.<endCol> <numStmt> <count>
			parts := strings.Fields(line)
			if len(parts) != 3 {
				continue
			}
			key := parts[0]
			var numStmt, cnt int
			fmt.Sscanf(parts[1], "%d", &numStmt)
			fmt.Sscanf(parts[2], "%d", &cnt)
			if prev, ok := merged[key]; ok {
				merged[key] = entry{prev.numStmt, prev.count + cnt}
			} else {
				merged[key] = entry{numStmt, cnt}
			}
		}
	}

	parseLines(unitBody)
	parseLines(integBody)

	f, err := os.Create(out)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "mode: %s\n", mode)
	for key, e := range merged {
		fmt.Fprintf(f, "%s %d %d\n", key, e.numStmt, e.count)
	}
}

// splitCoverMode separates the "mode: ..." header from the rest of a
// coverprofile.
func splitCoverMode(data []byte) (mode, body string) {
	s := string(data)
	if strings.HasPrefix(s, "mode:") {
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			return strings.TrimSpace(s[5:]), ""
		}
		return strings.TrimSpace(s[5:idx]), s[idx+1:]
	}
	return "set", s
}

// ---------------------------------------------------------------------------
// Helper: runCLI executes the compiled binary with the given args in a clean
// environment (no AOEO_PROVIDER_* vars, privacy disabled).
// ---------------------------------------------------------------------------

func runCLI(t *testing.T, args ...string) (output string, exitCode int) {
	t.Helper()
	bin := buildBinary(t)

	cmd := exec.Command(bin, args...)
	cmd.Env = cleanEnv(t)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		}
		t.Fatalf("unexpected exec error: %v", err)
	}
	return string(out), 0
}

// cleanEnv returns a copy of the current environment with all AOEO_PROVIDER_*
// variables removed and AOEO_PRIVACY_ENABLED forced to "false".  This gives
// every test a predictable baseline.
func cleanEnv(t *testing.T) []string {
	t.Helper()
	var env []string
	for _, e := range os.Environ() {
		k := envKey(e)
		if strings.HasPrefix(k, "AOEO_PROVIDER_") {
			continue
		}
		if k == "AOEO_PRIVACY_ENABLED" {
			continue // will be overridden below
		}
		env = append(env, e)
	}
	env = append(env, "AOEO_PRIVACY_ENABLED=false")
	// Point integration runs at the shared coverage directory.
	if coverageDir != "" {
		env = append(env, "GOCOVERDIR="+coverageDir)
	}
	return env
}

// envKey extracts the key portion ("KEY") from a "KEY=VALUE" env string.
func envKey(e string) string {
	if i := strings.IndexByte(e, '='); i >= 0 {
		return e[:i]
	}
	return e
}

// ---------------------------------------------------------------------------
// Helper: runCLIEnv is like runCLI but adds extra environment variables.
// ---------------------------------------------------------------------------

func runCLIEnv(t *testing.T, extra []string, args ...string) (string, int) {
	t.Helper()
	bin := buildBinary(t)

	cmd := exec.Command(bin, args...)
	cmd.Env = append(cleanEnv(t), extra...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		}
		t.Fatalf("unexpected exec error: %v", err)
	}
	return string(out), 0
}

// ---------------------------------------------------------------------------
// Tests: main() dispatch
// ---------------------------------------------------------------------------

// TestMain_NoArgs verifies that running with no arguments prints usage and
// exits with code 1.
func TestMain_NoArgs(t *testing.T) {
	out, code := runCLI(t)
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "AoEo CLI") {
		t.Errorf("output missing 'AoEo CLI':\n%s", out)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("output missing 'Usage:':\n%s", out)
	}
	if !strings.Contains(out, "Commands:") {
		t.Errorf("output missing 'Commands:':\n%s", out)
	}
}

// TestMain_Help_Integration verifies the "help" subcommand prints usage and exits 0.
func TestMain_Help_Integration(t *testing.T) {
	out, code := runCLI(t, "help")
	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(out, "AoEo CLI") {
		t.Errorf("output missing 'AoEo CLI':\n%s", out)
	}
	if !strings.Contains(out, "list-models") {
		t.Errorf("output missing 'list-models':\n%s", out)
	}
	if !strings.Contains(out, "chat") {
		t.Errorf("output missing 'chat':\n%s", out)
	}
	if !strings.Contains(out, "stream") {
		t.Errorf("output missing 'stream':\n%s", out)
	}
}

// TestMain_HelpDashH verifies "-h" is accepted as help.
func TestMain_HelpDashH(t *testing.T) {
	out, code := runCLI(t, "-h")
	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(out, "AoEo CLI") {
		t.Errorf("output missing 'AoEo CLI':\n%s", out)
	}
}

// TestMain_HelpDashDashHelp verifies "--help" is accepted as help.
func TestMain_HelpDashDashHelp(t *testing.T) {
	out, code := runCLI(t, "--help")
	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(out, "AoEo CLI") {
		t.Errorf("output missing 'AoEo CLI':\n%s", out)
	}
}

// TestMain_UnknownCommand verifies that an unrecognized subcommand prints an
// error, prints usage, and exits 1.
func TestMain_UnknownCommand(t *testing.T) {
	out, code := runCLI(t, "unknown")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "unknown command: unknown") {
		t.Errorf("output missing 'unknown command':\n%s", out)
	}
	// Should also print usage after the error.
	if !strings.Contains(out, "Usage:") {
		t.Errorf("output missing 'Usage:':\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Tests: list-models subcommand
// ---------------------------------------------------------------------------

// TestMain_ListModels_NoConfig verifies list-models exits 1 when no providers
// are configured.
func TestMain_ListModels_NoConfig(t *testing.T) {
	out, code := runCLI(t, "list-models")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// TestMain_ModelsAlias_NoConfig verifies the "models" alias behaves the same
// as "list-models".
func TestMain_ModelsAlias_NoConfig(t *testing.T) {
	out, code := runCLI(t, "models")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Tests: test subcommand
// ---------------------------------------------------------------------------

// TestMain_TestCmd_NoConfig verifies "test" exits 1 when no providers are
// configured.
func TestMain_TestCmd_NoConfig(t *testing.T) {
	out, code := runCLI(t, "test")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Tests: status subcommand
// ---------------------------------------------------------------------------

// TestMain_Status_NoConfig verifies "status" exits 1 when no providers are
// configured.
func TestMain_Status_NoConfig(t *testing.T) {
	out, code := runCLI(t, "status")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Tests: chat subcommand
// ---------------------------------------------------------------------------

// TestMain_Chat_NoMessage verifies "chat" without -message exits 1 with an
// appropriate error.
func TestMain_Chat_NoMessage(t *testing.T) {
	out, code := runCLI(t, "chat")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "-message is required") {
		t.Errorf("output missing '-message is required':\n%s", out)
	}
}

// TestMain_Chat_EmptyMessage verifies "chat -message ''" is rejected.
func TestMain_Chat_EmptyMessage(t *testing.T) {
	out, code := runCLI(t, "chat", "-message", "")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "-message is required") {
		t.Errorf("output missing '-message is required':\n%s", out)
	}
}

// TestMain_Chat_NoConfig verifies "chat -message hello" exits 1 when no
// providers are configured (loadClient fails).
func TestMain_Chat_NoConfig(t *testing.T) {
	out, code := runCLI(t, "chat", "-message", "hello")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// TestMain_Chat_WithModel_NoConfig verifies that specifying -model still fails
// gracefully when no providers are configured.
func TestMain_Chat_WithModel_NoConfig(t *testing.T) {
	out, code := runCLI(t, "chat", "-message", "hello", "-model", "gpt-4")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// TestMain_Chat_WithProvider_NoConfig verifies that specifying -provider still
// fails gracefully when no providers are configured.
func TestMain_Chat_WithProvider_NoConfig(t *testing.T) {
	out, code := runCLI(t, "chat", "-message", "hello", "-provider", "openai")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// TestMain_Chat_WithTemperature_NoConfig verifies custom temperature flag.
func TestMain_Chat_WithTemperature_NoConfig(t *testing.T) {
	out, code := runCLI(t, "chat", "-message", "hello", "-temperature", "0.5")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Tests: stream subcommand
// ---------------------------------------------------------------------------

// TestMain_Stream_NoMessage verifies "stream" without -message exits 1.
func TestMain_Stream_NoMessage(t *testing.T) {
	out, code := runCLI(t, "stream")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "-message is required") {
		t.Errorf("output missing '-message is required':\n%s", out)
	}
}

// TestMain_Stream_EmptyMessage verifies "stream -message ''" is rejected.
func TestMain_Stream_EmptyMessage(t *testing.T) {
	out, code := runCLI(t, "stream", "-message", "")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "-message is required") {
		t.Errorf("output missing '-message is required':\n%s", out)
	}
}

// TestMain_Stream_NoConfig verifies "stream -message hello" exits 1 when no
// providers are configured.
func TestMain_Stream_NoConfig(t *testing.T) {
	out, code := runCLI(t, "stream", "-message", "hello")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// TestMain_Stream_WithModel_NoConfig verifies specifying -model for stream.
func TestMain_Stream_WithModel_NoConfig(t *testing.T) {
	out, code := runCLI(t, "stream", "-message", "hello", "-model", "gpt-4")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// TestMain_Stream_WithProvider_NoConfig verifies specifying -provider for stream.
func TestMain_Stream_WithProvider_NoConfig(t *testing.T) {
	out, code := runCLI(t, "stream", "-message", "hello", "-provider", "openai")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// TestMain_Stream_WithTemperature_NoConfig verifies custom temperature for stream.
func TestMain_Stream_WithTemperature_NoConfig(t *testing.T) {
	out, code := runCLI(t, "stream", "-message", "hello", "-temperature", "0.9")
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}
	if !strings.Contains(out, "no providers configured") {
		t.Errorf("output missing 'no providers configured':\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Tests: privacy subcommand
// ---------------------------------------------------------------------------

// TestMain_Privacy_Disabled verifies the "privacy" subcommand outputs the
// disabled message when AOEO_PRIVACY_ENABLED is not "true".
func TestMain_Privacy_Disabled(t *testing.T) {
	out, code := runCLI(t, "privacy")
	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(out, "AoEo Privacy Filter") {
		t.Errorf("output missing 'AoEo Privacy Filter':\n%s", out)
	}
	if !strings.Contains(out, "Enabled:     false") {
		t.Errorf("output missing 'Enabled:     false':\n%s", out)
	}
	if !strings.Contains(out, "Privacy filter is disabled") {
		t.Errorf("output missing 'Privacy filter is disabled':\n%s", out)
	}
}

// TestMain_Privacy_WithEndpoint verifies the endpoint is displayed correctly
// when set via environment variable.
func TestMain_Privacy_WithEndpoint(t *testing.T) {
	out, code := runCLIEnv(t,
		[]string{"AOEO_PRIVACY_ENDPOINT=http://127.0.0.1:9999"},
		"privacy",
	)
	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(out, "http://127.0.0.1:9999") {
		t.Errorf("output missing custom endpoint:\n%s", out)
	}
	if !strings.Contains(out, "Privacy filter is disabled") {
		t.Errorf("output missing disabled message:\n%s", out)
	}
}

// TestMain_Privacy_WithPolicyAndFailOpen verifies policy and fail-open display.
func TestMain_Privacy_WithPolicyAndFailOpen(t *testing.T) {
	out, code := runCLIEnv(t,
		[]string{
			"AOEO_PRIVACY_POLICY=strict",
			"AOEO_PRIVACY_FAILOPEN=true",
		},
		"privacy",
	)
	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(out, "strict") {
		t.Errorf("output missing policy 'strict':\n%s", out)
	}
	// FailOpen shows as "true" because we set AOEO_PRIVACY_FAILOPEN=true.
	if !strings.Contains(out, "FailOpen:    true") {
		t.Errorf("output missing 'FailOpen:    true':\n%s", out)
	}
}
