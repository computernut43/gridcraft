package main

import (
	"slices"
	"strings"
	"testing"
)

func n(name string, deps ...dep) unit { return unit{name: name, deps: deps} }
func ws(name string) dep              { return dep{name: name, workspace: true} }
func wsTest(name string) dep          { return dep{name: name, kind: testDep, workspace: true} }
func ext(name string) dep             { return dep{name: name} }

// The cases of xtask/src/layers.rs's tests, in Go unit names.
func TestCheckLayers(t *testing.T) {
	for _, c := range []struct {
		name  string
		units []unit
		want  []string
	}{
		{"clean downward graph", []unit{
			n("core", ext("fmt")), n("numfmt", ws("core")), n("formula", ws("core")), n("functions", ws("core"), ws("formula")),
			n("model", ws("formula")), n("calc", ws("model"), ws("functions"), ws("numfmt")), n("xlsx", ws("core"), ws("formula"), ws("model")),
			n("chart", ws("model")), n("engine", ws("calc"), ws("xlsx"), ws("chart")), n("ui", ws("engine"), ext("gioui.org/app")),
			n("cmd/gridcraft-cli", ws("ui"), ws("mcp")),
		}, nil},
		{"upward", []unit{n("formula", ws("engine"))}, []string{"formula (L1) -> engine (L5) [normal]: may only depend on strictly lower layers"}},
		{"sideways", []unit{n("ui", ws("mcp"))}, []string{"ui (L6) -> mcp (L6) [normal]: may only depend on strictly lower layers"}},
		{"intra-layer chain one way", []unit{n("numfmt", ws("core"))}, nil},
		{"intra-layer chain reversed", []unit{n("core", ws("numfmt"))}, []string{"core (L0) -> numfmt (L0) [normal]: may only depend on strictly lower layers"}},
		{"calc and xlsx don't share a chain", []unit{n("xlsx", ws("calc"))}, []string{
			"xlsx (L2) -> calc (L2) [normal]: may only depend on strictly lower layers",
			"xlsx: format units may only depend on core/formula/model, not calc"}},
		{"format unit below its layer", []unit{n("xlsx", ws("numfmt"))}, []string{"xlsx: format units may only depend on core/formula/model, not numfmt"}},
		{"format unit's tests may import more", []unit{n("xlsx", wsTest("numfmt"))}, nil},
		{"UI below L6", []unit{n("chart", ext("gioui.org/widget"))}, []string{"chart (L3) -> gioui.org/widget: UI toolkits are only allowed from L6"}},
		{"unregistered", []unit{n("zebra")}, []string{"zebra: unknown unit; register it in tools/ci/layers.go layerTable (see MIGRATION.md §2)"}},
		{"apps are exempt", []unit{n("cmd/gridcraft", ext("fyne.io/fyne/v2"), ws("engine"))}, nil},
		{"testkit only from tests", []unit{n("calc", wsTest("rustref")), n("engine", ws("rustref"))}, []string{"engine: rustref may only be imported from tests"}},
	} {
		got := checkLayers(c.units)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}

func TestUnitsFromGoList(t *testing.T) {
	const m = "github.com/computernut43/gridcraft"
	input := `{"ImportPath":"` + m + `/internal/xlsx","Imports":["fmt","` + m + `/internal/model"],"XTestImports":["` + m + `/internal/rustref"]}
{"ImportPath":"` + m + `/internal/xlsx/xml","Imports":["bytes","` + m + `/internal/model"]}
{"ImportPath":"` + m + `/cmd/gridcraft-cli","Imports":["` + m + `/internal/engine"]}
{"ImportPath":"` + m + `/tools/ci","Imports":["os"]}`
	units, err := unitsFromGoList(m, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []unit{
		{name: "xlsx", deps: []dep{ext("fmt"), ws("model"), wsTest("rustref"), ext("bytes")}},
		{name: "cmd/gridcraft-cli", deps: []dep{ws("engine")}},
		{name: "tools", deps: []dep{ext("os")}},
	}
	if len(units) != len(want) {
		t.Fatalf("got %+v", units)
	}
	for i := range want {
		if units[i].name != want[i].name || !slices.Equal(units[i].deps, want[i].deps) {
			t.Errorf("unit %d: got %+v, want %+v", i, units[i], want[i])
		}
	}
}
