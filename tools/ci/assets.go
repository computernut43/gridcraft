package main

// Asset attribution, ported from `cargo xtask assets` (xtask/src/assets.rs): every non-code asset
// must be listed as `path` in ATTRIBUTION.md. An asset is any file under assets/, docs/brand/ or
// docs/images/; any file in an examples/ directory with a sample-data extension; and anywhere else,
// any image, icon or font file, or a workbook.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// assetExt are image, icon, font and workbook extensions: an asset wherever it lives.
var assetExt = []string{"png", "jpg", "jpeg", "gif", "webp", "bmp", "tif", "tiff", "ico", "icns", "svg", "pdf", "ttf", "otf", "woff", "woff2",
	"xlsx", "xlsm", "xls"}

// exampleExt are the extra extensions that count as assets inside examples/ (sample data).
var exampleExt = []string{"csv", "tsv", "json"}

// assetDirs: every file under these directories is an asset.
var assetDirs = []string{"assets/", "docs/brand/", "docs/images/"}

func isAsset(path string) bool {
	e := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	for _, d := range assetDirs {
		if strings.HasPrefix(path, d) {
			return true
		}
	}
	if slices.Contains(assetExt, e) {
		return true
	}
	return (strings.HasPrefix(path, "examples/") || strings.Contains(path, "/examples/")) && slices.Contains(exampleExt, e)
}

// missingAttribution lists the asset paths that ATTRIBUTION.md doesn't mention as `path`.
func missingAttribution(files []string, attribution string) []string {
	var miss []string
	for _, f := range files {
		if isAsset(f) && !strings.Contains(attribution, "`"+f+"`") {
			miss = append(miss, f)
		}
	}
	return miss
}

// gitPaths splits `git ls-files -z` output: NUL-separated and never quoted, so names with
// non-ASCII letters or newlines are still checked.
func gitPaths(stdout []byte) []string {
	var out []string
	for _, p := range bytes.Split(stdout, []byte{0}) {
		if len(p) > 0 {
			out = append(out, string(p))
		}
	}
	return out
}

func checkAssets(root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A failed listing is no listing: read as empty, it would pass with nothing checked (#1364).
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git ls-files failed (%v): %s", err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for _, f := range gitPaths(stdout.Bytes()) {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			files = append(files, f)
		}
	}
	md, err := os.ReadFile(filepath.Join(root, "ATTRIBUTION.md"))
	if err != nil {
		return fmt.Errorf("ATTRIBUTION.md: %w", err)
	}
	if miss := missingAttribution(files, string(md)); len(miss) > 0 {
		return fmt.Errorf("%d asset file(s) lack an ATTRIBUTION.md entry (author, source, licence):\n  %s", len(miss), strings.Join(miss, "\n  "))
	}
	count := 0
	for _, f := range files {
		if isAsset(f) {
			count++
		}
	}
	fmt.Printf("assets: all %d asset files attributed in ATTRIBUTION.md\n", count)
	return nil
}
