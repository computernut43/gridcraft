// Package rustref runs the frozen Rust GridCraft CLI (built from the baseline commit `23829d8`) as the
// reference the Go port is compared with (MIGRATION.md §5 and §8). It is test tooling: the layer
// gate lets only tests import it, and nothing here is linked into the apps.
//
// The binary is treated as a separate, untrusted process: an absolute path whose SHA-256 is
// checked (always in CI), no shell, a minimal environment (none of the caller's variables, so no
// tokens or keys leak), a private home and temp directory, a timeout and capped output.
package rustref

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Environment variables that configure the reference.
const (
	// EnvPath is the absolute path of the reference gridcraft-cli.
	EnvPath = "GRIDCRAFT_RUST_CLI"
	// EnvSHA256 is the binary's expected SHA-256 (hex); required when CI is set.
	EnvSHA256 = "GRIDCRAFT_RUST_CLI_SHA256"
)

// Defaults for CLI.Timeout and CLI.MaxOutput.
const (
	DefaultTimeout   = 2 * time.Minute
	DefaultMaxOutput = 64 << 20
)

// ErrNotConfigured means EnvPath is unset: reference tests are skipped.
var ErrNotConfigured = errors.New(EnvPath + " is not set")

// CLI is one reference binary.
type CLI struct {
	Path      string        // absolute path to gridcraft-cli
	SHA256    string        // expected hex digest; empty skips the check (never in CI)
	Timeout   time.Duration // per invocation; zero means DefaultTimeout
	MaxOutput int64         // cap on stdout, stderr and output files; zero means DefaultMaxOutput
}

// FromEnv configures the reference from EnvPath and EnvSHA256, verifying the binary.
func FromEnv() (*CLI, error) {
	path := os.Getenv(EnvPath)
	if path == "" {
		return nil, ErrNotConfigured
	}
	c := &CLI{Path: path, SHA256: strings.ToLower(strings.TrimSpace(os.Getenv(EnvSHA256)))}
	if c.SHA256 == "" && os.Getenv("CI") != "" {
		return nil, fmt.Errorf("%s must be set in CI so the reference binary is the one that was built", EnvSHA256)
	}
	if err := c.Verify(); err != nil {
		return nil, err
	}
	return c, nil
}

// Require returns the configured reference, skipping the test when there is none.
func Require(t testing.TB) *CLI {
	t.Helper()
	c, err := FromEnv()
	if errors.Is(err, ErrNotConfigured) {
		t.Skipf("reference not configured: set %s (and %s) to a gridcraft-cli built from commit 23829d8", EnvPath, EnvSHA256)
	}
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Verify checks that Path is absolute, a regular file, and matches SHA256 when one is set.
func (c *CLI) Verify() error {
	if !filepath.IsAbs(c.Path) {
		return fmt.Errorf("reference path %q must be absolute", c.Path)
	}
	info, err := os.Stat(c.Path)
	if err != nil {
		return fmt.Errorf("reference binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("reference binary %q is not a regular file", c.Path)
	}
	if c.SHA256 == "" {
		return nil
	}
	f, err := os.Open(c.Path)
	if err != nil {
		return fmt.Errorf("reference binary: %w", err)
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("hashing reference binary: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != c.SHA256 {
		return fmt.Errorf("reference binary %s has SHA-256 %s, want %s", c.Path, got, c.SHA256)
	}
	return nil
}

func (c *CLI) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

func (c *CLI) maxOutput() int64 {
	if c.MaxOutput > 0 {
		return c.MaxOutput
	}
	return DefaultMaxOutput
}

// errTooMuchOutput is returned when a stream exceeds MaxOutput.
var errTooMuchOutput = errors.New("reference output exceeds the size cap")

// cappedBuffer collects up to max bytes and fails the write beyond that, which ends the child.
type cappedBuffer struct {
	buf bytes.Buffer
	max int64
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len())+int64(len(p)) > b.max {
		return 0, errTooMuchOutput
	}
	return b.buf.Write(p)
}

// environment is the child's whole environment: a private home and temp directory, a fixed
// language, and (on Windows) the system root programs need to start.
func environment(dir string) []string {
	env := []string{
		"HOME=" + dir, "USERPROFILE=" + dir, "XDG_CONFIG_HOME=" + dir, "XDG_DATA_HOME=" + dir, "XDG_CACHE_HOME=" + dir,
		"TMPDIR=" + dir, "TMP=" + dir, "TEMP=" + dir,
		"LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8",
	}
	for _, k := range []string{"SystemRoot", "SYSTEMROOT"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// Run runs the reference with args in dir (which also becomes its home and temp directory) and
// returns its standard output. A non-zero exit, a timeout or too much output is an error that
// includes the start of its standard error.
func (c *CLI) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	res, err := c.Exec(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		msg := string(res.Stderr)
		if len(msg) > 4096 {
			msg = msg[:4096] + "…"
		}
		return nil, fmt.Errorf("reference %v: exit status %d\n%s", args, res.Code, strings.TrimSpace(msg))
	}
	return res.Stdout, nil
}

// Result is a finished reference run.
type Result struct {
	Stdout, Stderr []byte
	Code           int // exit status
}

// Exec runs the reference like Run but returns a non-zero exit as a Result, for tests that compare
// failures too. The error is for runs that didn't finish: not started, timed out, killed, or too
// much output.
func (c *CLI) Exec(ctx context.Context, dir string, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.Dir = dir
	cmd.Env = environment(dir)
	stdout := &cappedBuffer{max: c.maxOutput()}
	stderr := &cappedBuffer{max: c.maxOutput()}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("reference %v: %w (after %s)", args, ctx.Err(), c.timeout())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		return Result{Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes(), Code: exit.ExitCode()}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("reference %v: %w", args, err)
	}
	return Result{Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes()}, nil
}

// readCapped reads a file the reference wrote, failing if it is larger than MaxOutput.
func (c *CLI) readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, c.maxOutput()+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > c.maxOutput() {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), errTooMuchOutput)
	}
	return data, nil
}

// ConvertToJSON opens input with the reference and returns its workbook model as JSON
// (`gridcraft-cli convert input out.json`).
func (c *CLI) ConvertToJSON(ctx context.Context, input string) ([]byte, error) {
	abs, err := filepath.Abs(input)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "rustref-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "out.json")
	if _, err := c.Run(ctx, dir, "convert", abs, out); err != nil {
		return nil, err
	}
	return c.readCapped(out)
}

// NormalizeJSON rewrites JSON into a canonical form for comparison: object keys sorted,
// indented, and numbers in one spelling (so Rust's 12.0 and Go's 12 compare equal).
func NormalizeJSON(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("normalizing JSON: %w", err)
	}
	if dec.More() {
		return nil, errors.New("normalizing JSON: trailing data")
	}
	v, err := canonicalNumbers(v, 0)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(v, "", "  ")
}

// maxJSONDepth bounds recursion over hostile JSON.
const maxJSONDepth = 512

func canonicalNumbers(v any, depth int) (any, error) {
	if depth > maxJSONDepth {
		return nil, errors.New("normalizing JSON: nested too deeply")
	}
	switch x := v.(type) {
	case json.Number:
		if i, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return json.Number(strconv.FormatInt(i, 10)), nil
		}
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			return nil, fmt.Errorf("normalizing JSON: number %q: %w", x, err)
		}
		if f == 0 {
			f = 0 // -0 and 0 are the same value
		}
		return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
	case []any:
		for i, e := range x {
			n, err := canonicalNumbers(e, depth+1)
			if err != nil {
				return nil, err
			}
			x[i] = n
		}
	case map[string]any:
		for k, e := range x {
			n, err := canonicalNumbers(e, depth+1)
			if err != nil {
				return nil, err
			}
			x[k] = n
		}
	}
	return v, nil
}
