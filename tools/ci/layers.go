package main

// Dependency layering for the Go packages, ported from `cargo xtask layers`
// (xtask/src/layers.rs). A unit is a top-level package directory under internal/ (with its
// subpackages); cmd/ and tools/ are exempt. A unit may import only units in strictly lower
// layers (or an earlier unit of an INTRA_LAYER_ORDER chain), a format unit only the units
// FORMAT_ALLOWED lists, the test kit is imported only from tests, and nothing below L6 imports a
// UI toolkit.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

// classKind says where a unit sits.
type classKind int

const (
	layered    classKind = iota // a regular unit in Layer
	standalone                  // layer 0, and imports no unit at all
	testkit                     // test tooling: sits at L6, imported only from tests
	exempt                      // binaries and build tooling
)

type class struct {
	kind  classKind
	layer int
}

func (c class) effectiveLayer() int {
	switch c.kind {
	case standalone:
		return 0
	case testkit:
		return 6
	case exempt:
		return 7
	}
	return c.layer
}

// layerTable mirrors xtask/src/layers.rs TABLE, with Go unit names (ui-egui is ui; the new
// platform unit is MIGRATION.md §2's).
var layerTable = map[string]class{
	"core":      {layered, 0},
	"numfmt":    {layered, 0},
	"pdf":       {layered, 0},
	"formula":   {layered, 1},
	"functions": {layered, 1},
	"model":     {layered, 2},
	"calc":      {layered, 2},
	"xlsx":      {layered, 2},
	"chart":     {layered, 3},
	"engine":    {layered, 5},
	"ui":        {layered, 6},
	"mcp":       {layered, 6},
	"platform":  {layered, 6},
	"rustref":   {kind: testkit},
}

// intraLayerOrder: within a chain, a later unit may use an earlier one (xtask INTRA_LAYER_ORDER).
var intraLayerOrder = [][]string{{"core", "numfmt"}, {"formula", "functions"}, {"model", "calc"}, {"model", "xlsx"}}

// formatAllowed: a format unit may import only these units (xtask FORMAT_ALLOWED), outside its
// tests, so a file format never pulls in calculation or engine code.
var formatAllowed = map[string][]string{"xlsx": {"core", "formula", "model"}}

// uiPrefixes are import paths of UI toolkits and windowing libraries.
var uiPrefixes = []string{"gioui.org", "fyne.io/"}

const uiMinLayer = 6

func intraLayerAllowed(from, to string) bool {
	for _, chain := range intraLayerOrder {
		f, t := -1, -1
		for i, n := range chain {
			if n == from {
				f = i
			}
			if n == to {
				t = i
			}
		}
		if f >= 0 && t >= 0 && t < f {
			return true
		}
	}
	return false
}

func isUI(path string) bool {
	for _, p := range uiPrefixes {
		root := strings.TrimSuffix(p, "/")
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

type depKind int

const (
	normalDep depKind = iota
	testDep
)

func (k depKind) String() string {
	if k == testDep {
		return "test"
	}
	return "normal"
}

type dep struct {
	name      string // unit name for module packages, import path otherwise
	kind      depKind
	workspace bool
}

type unit struct {
	name string
	deps []dep
}

// classify returns a unit's class; ok is false for an unregistered unit.
func classify(name string) (class, bool) {
	if strings.HasPrefix(name, "cmd/") || name == "tools" {
		return class{kind: exempt}, true
	}
	c, ok := layerTable[name]
	return c, ok
}

func checkLayers(units []unit) []string {
	var out []string
	for _, u := range units {
		c, ok := classify(u.name)
		if !ok {
			out = append(out, fmt.Sprintf("%s: unknown unit; register it in tools/ci/layers.go layerTable (see MIGRATION.md §2)", u.name))
			continue
		}
		if c.kind == exempt {
			continue
		}
		layer := c.effectiveLayer()
		for _, d := range u.deps {
			if d.name == u.name {
				continue
			}
			if !d.workspace {
				if layer < uiMinLayer && isUI(d.name) {
					out = append(out, fmt.Sprintf("%s (L%d) -> %s: UI toolkits are only allowed from L%d", u.name, layer, d.name, uiMinLayer))
				}
				continue
			}
			if c.kind == standalone {
				out = append(out, fmt.Sprintf("%s: standalone unit must not import unit %s", u.name, d.name))
				continue
			}
			if allowed, isFormat := formatAllowed[u.name]; isFormat && d.kind != testDep && !slices.Contains(allowed, d.name) {
				out = append(out, fmt.Sprintf("%s: format units may only depend on %s, not %s", u.name, strings.Join(allowed, "/"), d.name))
			}
			dc, ok := classify(d.name)
			switch {
			case !ok:
				// Reported on its own entry.
			case dc.kind == testkit:
				if d.kind != testDep {
					out = append(out, fmt.Sprintf("%s: %s may only be imported from tests", u.name, d.name))
				}
			default:
				to := dc.effectiveLayer()
				if to >= layer && !(to == layer && intraLayerAllowed(u.name, d.name)) {
					out = append(out, fmt.Sprintf("%s (L%d) -> %s (L%d) [%s]: may only depend on strictly lower layers", u.name, layer, d.name, to, d.kind))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// listedPackage is the part of `go list -json` the check reads.
type listedPackage struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// unitOf names the unit a module package belongs to ("" for a path outside the module).
func unitOf(module, path string) string {
	if path != module && !strings.HasPrefix(path, module+"/") {
		return ""
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(path, module), "/")
	parts := strings.Split(rel, "/")
	switch {
	case rel == "":
		return "(module root)"
	case parts[0] == "internal" && len(parts) > 1:
		return parts[1]
	case parts[0] == "cmd" && len(parts) > 1:
		return "cmd/" + parts[1]
	case parts[0] == "tools":
		return "tools"
	}
	return rel
}

// unitsFromGoList builds the model from `go list -json ./...` output.
func unitsFromGoList(module string, r io.Reader) ([]unit, error) {
	byName := map[string]*unit{}
	var order []string
	seen := map[string]map[dep]bool{}
	dec := json.NewDecoder(r)
	for {
		var p listedPackage
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("go list -json: %w", err)
		}
		name := unitOf(module, p.ImportPath)
		if name == "" {
			continue
		}
		u, ok := byName[name]
		if !ok {
			u = &unit{name: name}
			byName[name] = u
			order = append(order, name)
			seen[name] = map[dep]bool{}
		}
		add := func(imports []string, kind depKind) {
			for _, imp := range imports {
				d := dep{name: imp, kind: kind}
				if n := unitOf(module, imp); n != "" {
					d = dep{name: n, kind: kind, workspace: true}
				}
				if !seen[name][d] {
					seen[name][d] = true
					u.deps = append(u.deps, d)
				}
			}
		}
		add(p.Imports, normalDep)
		add(p.TestImports, testDep)
		add(p.XTestImports, testDep)
	}
	units := make([]unit, 0, len(order))
	for _, n := range order {
		units = append(units, *byName[n])
	}
	return units, nil
}
