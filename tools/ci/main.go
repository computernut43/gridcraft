// Command ci runs GridCraft's Go quality gates: `go run ./tools/ci [gate…]`, where a gate is one
// of fmt, vet, nopanic, staticcheck, test, layers, assets, wasm, licences, vuln, or all (the
// default). The shared gates come from craft-libs (devtools/gates); layers and assets are
// GridCraft's own, ported from `cargo xtask`. `cargo xtask ci` stays the gate for Rust changes.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/computernut43/craft-libs/devtools/gates"
)

const module = "github.com/computernut43/gridcraft"

// order is the order `all` runs the gates in: cheap checks first.
var order = []string{"fmt", "vet", "nopanic", "layers", "assets", "staticcheck", "test", "wasm", "licences", "vuln"}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ci:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	c := gates.Config{Dir: root, SkipDirs: []string{"target", "testdata"}}
	steps := map[string]func() error{
		"fmt":     c.Gofmt,
		"vet":     c.Vet,
		"nopanic": func() error { return c.NoPanic("github.com/computernut43/craft-libs/devtools/nopanic/cmd/nopanic") },
		"layers": func() error {
			out, err := c.Output(nil, "go", "list", "-json", "./...")
			if err != nil {
				return err
			}
			units, err := unitsFromGoList(module, bytes.NewReader(out))
			if err != nil {
				return err
			}
			if v := checkLayers(units); len(v) > 0 {
				return fmt.Errorf("%d layering violation(s):\n  %s", len(v), strings.Join(v, "\n  "))
			}
			fmt.Printf("layers: %d unit(s) checked\n", len(units))
			return nil
		},
		"assets":      func() error { return checkAssets(root) },
		"staticcheck": c.Staticcheck,
		"test":        c.Test,
		"wasm": func() error {
			// Everything below the UI must build for the browser (MIGRATION.md §1), as must the web
			// app; the desktop app, CLI and tooling needn't.
			pkgs, err := c.Packages(module+"/tools", module+"/cmd/gridcraft", module+"/cmd/gridcraft-cli", module+"/internal/platform")
			if err != nil {
				return err
			}
			return c.Wasm(pkgs)
		},
		"licences": c.Licences,
		"vuln":     c.Vuln,
	}
	if len(args) == 0 || (len(args) == 1 && args[0] == "all") {
		args = order
	}
	for _, name := range args {
		step, ok := steps[name]
		if !ok {
			return fmt.Errorf("unknown gate %q (want one of %v or all)", name, order)
		}
		fmt.Printf("\n=== ci: %s ===\n", name)
		err := step()
		switch {
		case errors.Is(err, gates.ErrNothingToCheck):
			fmt.Printf("ci: %s: skipped (%v)\n", name, err)
		case err != nil:
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	fmt.Printf("\nci: all %d gate(s) passed\n", len(args))
	return nil
}

// moduleRoot finds the directory holding this module's go.mod, from the working directory up.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && isModule(data) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run inside the GridCraft module (no go.mod for " + module + " found)")
		}
		dir = parent
	}
}

// isModule reports whether go.mod's first line declares this module. Git on Windows checks files
// out with CRLF line endings, so the line is compared without surrounding whitespace.
func isModule(gomod []byte) bool {
	first, _, _ := strings.Cut(string(gomod), "\n")
	return strings.TrimSpace(first) == "module "+module
}
