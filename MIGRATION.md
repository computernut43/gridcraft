# GridCraft: migration from Rust to Go

> **Status:** proposed, awaiting owner approval · **Created:** 2026-10-20 · **Change:** major (first version, from the Phase 1 audit of `main` at `23829d8`)

This is the plan for moving GridCraft from Rust to Go. It is an incremental port: the Rust tree
stays the shipping product, and the reference to compare against, until the Go code that replaces
each part builds, passes its tests, matches Rust's behaviour on the same inputs, and has been
reviewed. Nothing in the Rust tree is deleted before that.

**Goal (owner decision, 2026-10-20):** a full Go replacement, including the desktop app and the
browser build. Go only where it can be done; keeping some non-Go code is acceptable when the
alternative is clearly worse, and every such exception is recorded in
[Exceptions](#13-exceptions-to-go-only).

What does not change: the product (an Excel-class spreadsheet with XLSX as its native format),
file compatibility, the command ids and their JSON params, the control protocol, the MCP tools, the
CLI, the never-crash standard, the clean-room rules, the asset rules and attribution, and the
licence (MIT OR Apache-2.0).

## Contents

1. [Starting point](#1-starting-point)
2. [Where the Go code lives](#2-where-the-go-code-lives)
3. [Go module and package structure](#3-go-module-and-package-structure)
4. [Rust-to-Go component mapping](#4-rust-to-go-component-mapping)
5. [Dependency and library decisions](#5-dependency-and-library-decisions)
6. [Design decisions that are not translations](#6-design-decisions-that-are-not-translations)
7. [Never crash, in Go](#7-never-crash-in-go)
8. [Testing and compatibility strategy](#8-testing-and-compatibility-strategy)
9. [Platform support plan](#9-platform-support-plan)
10. [Feature parity checklist](#10-feature-parity-checklist)
11. [Milestones](#11-milestones)
12. [Risks](#12-risks)
13. [Exceptions to Go only](#13-exceptions-to-go-only)
14. [Open items](#14-open-items)

## 1. Starting point

A Cargo workspace with about 136k lines of Rust in 16 members and 1,670 `#[test]` functions
(counts measured 2026-10-20; `docs/architecture.md` has older numbers).

| Layer | Crate | Lines | Tests | Third-party dependencies |
|---|---|---|---|---|
| L0 | `core` | 1.5k | 22 | serde, thiserror |
| L0 | `numfmt` | 3.1k | 35 | none |
| L1 | `formula` | 2.5k | 30 | serde, thiserror |
| L1 | `functions` | 15.7k | 125 | regex |
| L2 | `model` | 3.0k | 14 | sha2, serde |
| L2 | `calc` | 4.7k | 61 | none |
| L2 | `xlsx` | 21.9k | 181 | quick-xml, zip, aes, sha1, sha2 |
| L2 | `pdf` | 1.1k | 14 | none |
| L3 | `chart` | 4.0k | 34 | serde |
| L5 | `engine` | 33.6k | 345 + 17 integration files | image, serde_json |
| L6 | `ui-egui` | 36.5k | 699 | egui, skrifa, `craft-ui` (storytold/craft-libs) |
| L6 | `mcp` | 1.6k | 21 | serde_json |
| L7 | `apps/gridcraft` | 2.0k | 28 | eframe/wgpu, muda, fmv-macos-events, rfd, arboard, `craft-print`, winresource |
| L7 | `apps/gridcraft-cli` | 2.5k | 29 | zip, quick-xml |
| L7 | `apps/gridcraft-web` | 0.6k | 0 | wasm-bindgen, web-sys, js-sys |
| — | `xtask` | 1.6k | — | serde_json |

Facts the plan relies on:

- **Everything is a command.** `Session::execute(id, params)` (`crates/engine/src/lib.rs`) checks
  `enabled`, runs the command under `catch_unwind`, restores the workbook if it panicked, journals
  it and records undo. The UI, CLI, control channel and MCP all go through it.
- **Undo is copy-on-write snapshots.** Cells live in bands of 64 rows behind `Arc`
  (`crates/model/src/store.rs`); a snapshot copies band pointers and `Arc::make_mut` copies a band
  on its first edit. Undo keeps the old `Arc<Workbook>`; `Arc::ptr_eq` detects a no-op command.
- **Order is deterministic.** Maps are `BTreeMap`; XLSX output and the fuzz invariants rely on it.
- **Text functions count Unicode scalar values** (`str::chars`), not bytes or UTF-16 units.
- **The headless engine is fully scriptable**: `gridcraft-cli run --in … --cmd id=json --out …`,
  `cat`, `info --json`, `eval --json` and `check` (calc oracle against cached values) make the
  Rust build a reference we can run the Go build against.

## 2. Where the Go code lives

**In this repository, at its root, next to the Rust tree, and then instead of it.**

- One Go module, `github.com/computernut43/gridcraft`, with `go.mod` at the repository root.
  Go and Cargo don't interfere: Go ignores directories with no `.go` files (`crates/`, `apps/`,
  `target/`), and Cargo ignores Go directories.
- Go packages go under `internal/` (libraries) and `cmd/` (binaries). These names don't collide with
  `crates/`, `apps/` or `xtask/`, so both trees can build side by side for the whole migration.
- The Rust tree is removed in **two cuts**, not crate by crate, because every Rust crate is linked
  into the Rust apps through `engine`:
  1. **Headless cut** (end of milestone G6): when the Go CLI and MCP server match the Rust ones on
     the parity checklist and the differential suite, `apps/gridcraft-cli` and `crates/mcp` are
     removed and the Go binaries take their names. The Rust library crates stay, because the
     Rust desktop and web apps still need them, and the Rust CLI is kept buildable from the last
     tag before the cut as the reference.
  2. **Full cut** (end of G10): when the Go desktop and web apps pass the UI checklist, the Cargo
     workspace (`Cargo.toml`, `Cargo.lock`, `crates/`, `apps/`, `xtask/`, `rustfmt.toml`,
     `clippy.toml`) is removed in one reviewed PR. A `rust-final` tag marks the last Rust commit.
- Each cut is its own PR, with the owner's approval recorded in it.
- Repository docs (`AGENTS.md`, `CLAUDE.md`, `README.md`, `docs/architecture.md`, CI) describe both
  trees during the migration and are rewritten for Go at the full cut.

## 3. Go module and package structure

The packages keep the existing layers and names, so the docs, the layer check and people's mental
model carry over. Arrows show allowed dependencies; a Go layer check (§8) enforces them, as
`cargo xtask layers` does today.

```text
go.mod                       module github.com/computernut43/gridcraft
internal/
  core/        L0  addresses (A1, R1C1, sheet-qualified), Value, CellError, coercion, dates, typed input
  numfmt/      L0  number-format codes, General, TEXT()
  formula/     L1  lexer, parser, AST, printer, reference adjustment
  functions/   L1  FnSpec registry and implementations; one file per category as today
  model/       L2  workbook, sheets, COW cell store, styles, names, tables, CF, validation, drawings,
                   pivots, print settings, protection, preserved parts
  calc/        L2  evaluator, dependency graph, recalc, spills, cycles
  xlsx/        L2  XLSX read/write, CSV/TSV; subpackages xlsb/, ods/, xls/ (with cfb/), crypto/
  pdf/         L2  PDF 1.7 writer
  chart/       L3  chart layout, rasteriser, sparklines
  engine/      L5  Session, history, selection, clipboard, fill, sort/filter, pivots, print, io;
                   engine/cmd: the command registry; engine/catalog: the ribbon catalog
  mcp/         L6  MCP server (JSON-RPC over stdio), headless and bridged
  ui/          L6  the desktop/web UI (toolkit decided by the UI spike, §5)
  control/     L6  the control channel server
  ordmap/      L0  small sorted-map helpers (replaces BTreeMap; no external dependency)
  crashguard/  L0  recover-and-report helpers used at every entry point (§7)
cmd/
  gridcraft/        L7  desktop app
  gridcraft-cli/    L7  CLI (same subcommands and flags as today)
  gridcraft-web/    L7  browser build
tools/
  craftci/          the quality gate: fmt, vet, lint, tests, assets, layers, wasm, parity
  lint/nopanic/     go/analysis pass: no panic() and no unchecked type assertions in non-test code
  difftest/         runs command scripts through the Rust and Go CLIs and compares results
```

Conventions:

- Each package has a `doc.go` stating its job and its layer.
- Errors are values: each package has its own error type(s); `engine` maps them to the same error
  categories the Rust `EngineError` has (unknown command, disabled, bad params, internal…), so the
  CLI, control channel and MCP report the same errors.
- No package-level mutable state except registries built once at start-up (`functions`,
  `engine/cmd`).
- Tests sit next to the code (`*_test.go`); shared fixtures are built in code, never committed as
  binaries (the corpus rule is unchanged).

## 4. Rust-to-Go component mapping

| Rust | Go | How it is ported |
|---|---|---|
| `core` | `internal/core` | Direct. `Value` becomes a small struct with a kind tag rather than an interface, to avoid allocations in hot loops. `CellError` keeps its codes and strings. |
| `numfmt` | `internal/numfmt` | Direct. Float-to-decimal formatting must match Rust's digit-for-digit (§6). |
| `formula` | `internal/formula` | Direct. AST as tagged structs; printer and adjuster ported with their tests. |
| `functions` | `internal/functions` | Category by category. The `FnSpec` registry becomes a table built at init; array lifting ported as is. |
| `model` | `internal/model` | Redesigned COW store and explicit `Clone` methods (§6). Same field set. |
| `calc` | `internal/calc` | Ported single-threaded first, then parallelised after parity (§11, G5). |
| `xlsx` | `internal/xlsx` (+ `xlsb`, `ods`, `xls`, `cfb`, `crypto`) | Ported part by part. Same lenient-reader policy, same warnings, same pass-through. |
| `pdf` | `internal/pdf` | Direct; no dependencies. |
| `chart` | `internal/chart` | Direct; drawing primitives become the shared vocabulary for the UI and PDF output. |
| `engine` (+ `cmd/*`, `catalog.rs`) | `internal/engine`, `engine/cmd`, `engine/catalog` | One `cmd/*.rs` file at a time. Command ids, params and results unchanged. |
| `mcp` | `internal/mcp` | Direct; already hand-written JSON-RPC. |
| `ui-egui` | `internal/ui` | Rewritten for the chosen toolkit, not translated (§5). Behaviour reference: `docs/ui-parity.md` and the existing UI tests. |
| `ui-egui/src/control.rs` | `internal/control` | Same JSON lines protocol (`docs/control-protocol.md`). |
| `ui-egui/src/i18n/*` | `internal/ui/i18n` | Same strings and languages, moved into a string catalog. |
| `ui-egui/src/icons.rs` | `internal/ui/icons` | Same original icons, redrawn as vector paths. No new assets. |
| `apps/gridcraft` | `cmd/gridcraft` | New shell around the chosen toolkit; platform hooks per §9. |
| `apps/gridcraft-cli` | `cmd/gridcraft-cli` | Same subcommands, flags, output and exit codes (`docs/cli.md`). |
| `apps/gridcraft-web` | `cmd/gridcraft-web` | `GOOS=js GOARCH=wasm` build (§9). |
| `xtask` | `tools/craftci` | Same gate names (`ci`, `assets`, `layers`, `wasm`, `parity`, `version`, `stats`). |
| `craft-ui`, `craft-print` (storytold/craft-libs) | Go version of craft-libs (`computernut43/craft-libs`, being rewritten by the owner) | **Blocked** until the owner says the Go craft-libs is ready. Until then, UI and print work stops at the spike (§11). |

## 5. Dependency and library decisions

Rule: the standard library first; a third-party module only when the standard library can't do the
job and the module is maintained, licence-compatible with MIT OR Apache-2.0, pure Go (no cgo)
where possible, and builds for `js/wasm`. Every module added is recorded in
[`go-dependencies`](#go-dependencies) below with its reason, and listed in `NOTICE` (with its
licence text) before a binary that embeds it is released. Versions are pinned in `go.mod` and
`go.sum`.

### Go version

Go 1.24.7 is installed in the dev environment today. The module starts at `go 1.24` and moves to
the current stable release once it is installed everywhere it is built (CI, release runners, dev
machines). We don't depend on language features newer than the `go` directive, and we don't depend
on experiments (`GOEXPERIMENT`, for example `encoding/json/v2`).

### Decisions

| Need | Rust today | Go choice | Why | Risk |
|---|---|---|---|---|
| ZIP | zip | `archive/zip` | Standard library, wasm-safe. | Low. Decompressed size must be capped per part and in total (zip bombs). |
| XML | quick-xml | `encoding/xml` with token-level streaming (`Decoder.Token`, `RawToken`) | Standard library, wasm-safe, handles namespaces. | **Medium**: much slower than quick-xml. Benchmark gate in G3; if it fails, write a small purpose-built SpreadsheetML tokenizer in `internal/xlsx` before adding a dependency. |
| JSON | serde_json | `encoding/json` | Standard library. serde_json sorts object keys (no `preserve_order`) and so does Go's map encoding; both print shortest round-trip floats. | Low. Untyped params need a small `jsonv` helper; differential tests compare bytes. |
| SHA-1 / SHA-2 / AES | sha1, sha2, aes | `crypto/sha1`, `crypto/sha256`, `crypto/sha512`, `crypto/aes`, `crypto/cipher` | Standard library. | Low. |
| PNG / JPEG | image | `image/png`, `image/jpeg` | Standard library. Image dimensions are checked before decoding (`image.DecodeConfig`) to cap memory. | Low. |
| Regex (REGEXTEST/REGEXEXTRACT/REGEXREPLACE) | regex | `regexp` | Standard library; RE2 semantics like Rust `regex` (no lookaround or backreferences in either). | Medium: flag syntax, Unicode classes and size limits differ in detail. Differential tests over the Rust test patterns; pattern length capped as today. |
| Errors | thiserror | `errors`, typed errors | Idiomatic Go. | Low. |
| Ordered maps | `BTreeMap` | `internal/ordmap` (sorted slices + `slices.BinarySearch`) | Deterministic order without a dependency. Cell rows use the banded store (§6). | Low; profiled in G2. |
| Sorting | `sort` (stable) | `slices.SortStableFunc` everywhere | Excel's sort is stable, Rust's `sort` is stable, `sort.Slice` is not. Lint rule forbids `sort.Slice`. | Low. |
| Parallel recalc | (none; rayon declared, unused) | goroutines and `sync` | Built in. | Determinism and data races: `go test -race` in CI. |
| Text shaping (UI, PDF) | none (a known gap) | Candidate: `github.com/go-text/typesetting` (BSD-3-Clause / Unlicense; shaping, bidi, font parsing; v0.3.5 published 2026-09-04). Verify when G7 starts. | Closes the Arabic/Devanagari shaping gap (localization-parity.md). | Medium; evaluated in the UI spike. |
| Font parsing | skrifa | Same candidate, or `golang.org/x/image/font/sfnt` (BSD-3-Clause) | Validates system fonts before use, as today. | Low. |
| UI toolkit | egui/eframe | **Provisional: Gio (`gioui.org`)**, decided by the UI spike (G7). Fallback: Fyne (`fyne.io/fyne/v2`). | See below. | **High.** |
| Clipboard, file dialogs, native menus, Apple events, printing | arboard, rfd, muda, fmv-macos-events, craft-print | Go craft-libs (owner is rewriting it) | Shared across the craft apps. | **Blocked** on craft-libs. |
| Static analysis | clippy | `go vet`, `staticcheck` (MIT), `nilaway` (Apache-2.0), and our own `tools/lint/nopanic` | See §7. | Low. |

Not used, and why:

- **excelize** (BSD-3-Clause) has its own workbook model. Adopting it would throw away GridCraft's
  model, lenient reader, warnings and pass-through, and it can't give byte-level control of the
  output. It may be used **in tests only**, as a second independent XLSX reader for checking our
  output.
- **Wails or another webview UI** would need handwritten JavaScript or TypeScript, against the
  repository's no-handwritten-JS rule.
- **cgo bindings to the Rust crates.** That isn't a port, and it needs `unsafe` on the Rust side,
  which the workspace forbids.

### UI toolkit

There is no Go equivalent of egui. The UI is the largest crate (36.5k lines, 699 tests), so it is
rewritten against a Go toolkit, not translated. Candidates checked on 2026-10-20:

| | Gio | Fyne |
|---|---|---|
| Model | Immediate mode, like egui: a close fit for a virtualised grid redrawn every frame | Retained widgets with custom-widget support |
| Latest release | v0.10.0, 2026-05-18 (previous v0.9.0, 2025-09-10); the maintainer has committed to regular releases | v2.8.2, 2026-10-07 |
| Licence | MIT / Unlicense | BSD-3-Clause |
| Platforms | macOS, Windows, Linux, FreeBSD, Android, iOS, browser (WebGL) | macOS, Windows, Linux, BSD, Android, iOS, browser |
| Rendering | Own GPU vector renderer | OpenGL-based |
| Main concern | A small team, few ready-made widgets: the ribbon, grid, dialogs and panes are all custom (as they mostly are in egui today) | Opinionated look and layout; matching Excel's dense UI fights the toolkit |

The **UI spike** (G7) settles it with measured criteria, on Gio first:

1. A virtualised 1,048,576 × 16,384 grid scrolling at 60 fps with 10k visible formatted cells on a
   mid-range laptop.
2. Text: system fonts, fallback for CJK, shaping for Arabic and Devanagari, IME input.
3. Accessibility: screen-reader exposure of the grid and ribbon (egui has AccessKit today; losing
   it would be a regression).
4. Web: the same code in the browser, with download size and start-up time recorded.
5. Headless rendering to PNG for the screenshot tests (`snapshot` example and `ui.screenshot`).

If Gio fails a criterion that Fyne passes, Fyne is chosen. If both fail one, the owner decides
(including whether to keep the Rust UI longer, see [Exceptions](#13-exceptions-to-go-only)).

### Go dependencies

Filled in as modules are added. Empty today.

| Module | Version | Licence | Used by | Why |
|---|---|---|---|---|

## 6. Design decisions that are not translations

These are the places where Rust idioms carry the design, and Go needs a different mechanism for the
same behaviour.

1. **Copy-on-write snapshots without `Arc::make_mut`.**
   - Each document has a history generation counter. Each band, and each other shared part of the
     model (styles, names, tables, drawings, pivots…), records the generation that owns it.
   - Writing to a part owned by an older generation copies it first (`clone-on-write`), then stamps
     it with the current generation. Taking an undo snapshot advances the generation, so nothing
     is copied until something is written.
   - "Did this command change anything?" (today `Arc::ptr_eq`) becomes a per-document dirty flag set
     by every write path.
   - Every model type gets an explicit `Clone()` that deep-copies slices and maps. A test walks each
     type with reflection and fails if a field holding a slice, map or pointer isn't copied, so a new
     field can't silently alias an undo snapshot.
   - Model fields are unexported where practical; writes go through methods that do the
     copy-on-write, so callers can't bypass it.
2. **Deterministic order.** No Go map is iterated where order reaches output, a result or a test.
   `ordmap` or sorted keys everywhere else; a lint rule flags `range` over a map in `xlsx`,
   `engine/io` and command results.
3. **Numbers.** `float64` is the same IEEE type, but:
   - Excel's 15-significant-digit display and the Rust shortest-round-trip conversions are matched
     digit for digit by `numfmt` tests generated from the Rust implementation.
   - Go's pure-Go `math` functions may differ from Rust's (platform libm) in the last bit for
     `exp`, `log`, `pow`, trig and gamma. Function tests use the tolerances the Rust tests use,
     and the differential suite reports any difference beyond 1e-12 relative for review.
   - Integer conversions from floats use checked helpers (Go's float-to-int conversion of an
     out-of-range value is implementation-defined, not a panic, so it fails silently).
4. **Strings.** Character-based functions work on runes (`utf8.RuneCountInString`, `[]rune` only
   where needed), matching Rust's `chars()`. No byte slicing of user text except at
   `utf8` boundaries.
5. **Commands.** `CommandSpec` becomes a struct with function fields (`Enabled func(*Session) error`,
   `Run func(*Session, jsonv.Object) (any, error)`), registered in a table. Ids, labels, menu paths,
   shortcuts, params docs, `journal` and `undoable` are copied unchanged so the catalog, parity
   checklist and `commands --json` output are identical.
6. **Errors instead of panics for every unfinished path.** A command or function that isn't ported
   yet is not registered at all (it reports "unknown command"), so a partial Go build can never
   claim a command it doesn't implement.

## 7. Never crash, in Go

The standard (`craftrules/standards/never-crash.md`, and the rules in `AGENTS.md`) stays in force.
Go can't rule out panics at compile time the way `#![deny(clippy::unwrap_used…)]` plus
`forbid(unsafe_code)` does: an out-of-range index, a nil map write, a nil pointer dereference, an
unchecked type assertion or integer division by zero all compile and panic at run time. So the Go
version is enforced in layers:

1. **No explicit panics.** `tools/lint/nopanic` (a `go/analysis` pass in the gate) rejects
   `panic(…)`, `log.Fatal*`, `os.Exit` outside `cmd/*/main.go`, unchecked type assertions
   (`x.(T)` without `, ok`), `sort.Slice` and `regexp.MustCompile` with a non-constant pattern, in
   non-test code.
2. **No `unsafe`, no cgo** in `internal/` (checked by the layer tool); `reflect` only in tests.
3. **Hostile numbers.** Same rules as today: bounds-checked access helpers for rows, columns and
   ranges; checked arithmetic (`internal/core` helpers) for sizes derived from input; caps on
   allocations (whole-column ranges, `REPT`, `SEQUENCE`, decompressed parts, image sizes); bounded
   recursion with explicit depth counters (formula nesting, names referring to names, dependency
   chains).
4. **Static checks.** `go vet`, `staticcheck` and `nilaway` with no warnings.
5. **Last-resort guard.** `Session.Execute` runs each command inside `defer recover()`; on a panic
   it restores the snapshot taken before the command, rebuilds calc state and returns an
   internal error, as Rust does today. Every goroutine (recalc workers, control channel, MCP,
   file loading) starts through `crashguard.Go`, which recovers and reports; an unrecovered
   panic in any goroutine kills the whole Go process, so this is mandatory, not optional.
6. **Fuzzing.** Go native fuzzing (`testing.F`) replaces and extends the Rust fuzz tests:
   `every_command_survives_empty_params` and the hostile-params tests, the command-sequence fuzz
   (undo/redo, XLSX round-trip and full-recalc invariants), the formula parser, number-format
   parser, XLSX/XLSB/ODS/XLS/CSV readers and the decryptor. Seeds come from the Rust tests'
   inputs. CI runs each fuzz target for a short fixed time; `GRIDCRAFT_FUZZ_*` soak settings are
   kept.
7. **Every crash fix lands with a regression test**, as today.

## 8. Testing and compatibility strategy

"Parity" for a component means all three of the following, and the parity checklist (§10) only
marks a component done when all three are recorded:

1. **Ported tests pass.** Each Rust test for the component is either ported, or listed in the
   component's port note as not applicable with the reason (for example a test of Rust-only API
   shape). The port note goes in the PR description.
2. **Differential tests pass.** `tools/difftest` runs the same input through the Rust and Go builds
   and compares results:
   - **Command scripts**: `gridcraft-cli run --in … --cmd …` on both, then compare `cat --formulas`,
     `cat`, `info --json` and the saved XLSX.
   - **Formulas**: `eval --json` on both for every function's test cases plus generated cases.
   - **Files**: XLSX output compared part by part after canonicalising XML (attribute order,
     whitespace), plus byte equality of pass-through parts.
   - **Calc oracle**: `gridcraft-cli check` on both over the same workbooks.

   The Rust CLI is built from the same commit (`cargo build --release -p gridcraft-cli`), located
   by `GRIDCRAFT_RUST_CLI`; difftest is skipped with a clear message when it isn't set, and CI
   always sets it until the full cut.
3. **Fuzzing finds nothing** in the component's fuzz targets for the CI time budget.

Also:

- **Test workbooks are built in code**, never committed as binary fixtures; the Excel-authored
  corpus is fetched pinned and sha256-verified (craftrules `standards/test-corpora.md`), as today.
  It is used once access is set up (the owner has approved depending on it).
- **Excel itself** remains a black-box observer on the dev machine for checking that our output
  opens without a repair prompt; the clean-room rules are unchanged.
- **The quality gate** (`go run ./tools/craftci ci`) runs `gofmt -l` (no output), `go vet ./...`,
  `staticcheck ./...`, `nilaway ./...`, `nopanic`, `go test -race ./...`, short fuzz runs, the
  assets check, the layer check, the wasm build and `parity --check`. CI runs it next to the
  existing `cargo xtask ci` job; both must pass until the full cut.
- **Performance** is a compatibility property: `engine/examples/perf.rs` is ported to Go
  benchmarks, and a Go component may not be more than 1.5× slower than Rust on the same benchmark
  without the owner's agreement (open, save, recalc, 100k-dependent edit, scroll).

## 9. Platform support plan

| Target | Rust today | Go plan | Notes |
|---|---|---|---|
| Linux x86_64 / aarch64 | yes | yes | Native Go targets. |
| Linux riscv64 | yes | yes, headless first | Go supports `linux/riscv64`; UI depends on the toolkit's GPU backend there. |
| macOS universal (arm64 + x86_64) | yes | yes | Two Go builds joined with `lipo`. Apple events and native menus via the Go craft-libs. |
| Windows x64 / x86 / arm64 | yes | yes | Native Go targets; exe icon and version resources via a resource step in packaging (§14 lists the tool choice as open). |
| FreeBSD | yes | yes | Native Go target; UI depends on the toolkit. |
| Browser (WASM) | yes (WebGPU / WebGL2) | yes (`GOOS=js GOARCH=wasm`) | See below. |
| Packaging (AppImage, Flatpak, deb, rpm, tarball, macOS app, Windows installer) | yes | yes | Scripts in `packaging/` are updated to take Go binaries; formats unchanged. |

**Browser.** The owner chose Go for the web build. Known costs:

- Standard Go `js/wasm` binaries are large (the engine, 513 functions, XLSX and the UI will likely
  be tens of MB before compression) and run Go's garbage collector in the browser.
- TinyGo produces smaller binaries but has limited reflection and `encoding/json` support; it is
  evaluated in the UI spike only if download size is a problem.
- The web build is built in CI from G2 on (headless engine in a test page driven by Go tests via
  `wasmbrowsertest`-style tooling or Node's wasm runner), so a wasm break is caught on the day it
  happens, as with `cargo xtask wasm` today.
- The size and start-up time are measured in the UI spike and reported to the owner before the
  web UI is built.

## 10. Feature parity checklist

States: **—** not started · **Doc** Rust behaviour documented · **Go** implemented, tests ported ·
**Diff** differential tests pass · **Done** fuzzed, reviewed, parity recorded.

| # | Component | Rust source | State | Notes |
|---|---|---|---|---|
| 1 | Addresses, ranges, A1/R1C1 | `core/addr.rs` | — | G1 |
| 2 | Values, errors, coercion, comparison | `core/value.rs` | — | G1 |
| 3 | Date serials (1900/1904) | `core/date.rs` | — | G1 |
| 4 | Typed-input parsing | `core/parse.rs` | — | G1 |
| 5 | Banded COW cell store, snapshots | `model/store.rs`, `cellmap.rs` | — | G1 (slice) |
| 6 | Workbook, sheets, cells | `model/workbook.rs`, `sheet.rs`, `cell.rs` | — | G1 (subset) → G2 |
| 7 | Styles (interned) | `model/style.rs` | — | G2 |
| 8 | Names, tables, CF, validation, comments, links | `model/features.rs` | — | G2 |
| 9 | Charts, shapes, images, sparklines (model) | `model/features.rs` | — | G4 |
| 10 | Pivots (model) | `model/features.rs` | — | G4 |
| 11 | Print settings, protection, passwords | `model/features.rs`, `password.rs` | — | G2 |
| 12 | Preserved parts (pass-through) | `model/preserved.rs`, `xlsx/passthrough.rs` | — | G1 (raw parts) → G3 |
| 13 | Number formats | `numfmt/*` | — | G2 |
| 14 | Formula lexer, parser, printer | `formula/*` | — | G2 |
| 15 | Reference adjustment | `formula/adjust.rs` | — | G2 |
| 16 | Functions: math, stat, text, logical, info | `functions/*` | — | G4 |
| 17 | Functions: lookup, dynamic arrays, database, criteria | `functions/*` | — | G4 |
| 18 | Functions: date, financial, engineering, ETS | `functions/*` | — | G4 |
| 19 | Evaluator, LET/LAMBDA, special forms | `calc/eval.rs` | — | G4 |
| 20 | Dependency graph, recalc, spills, cycles | `calc/recalc.rs` | — | G4 |
| 21 | Multithreaded recalc | — (gap) | — | G5, beyond Rust parity |
| 22 | XLSX read: package, workbook, sheets, shared strings | `xlsx/read.rs`, `sheet_read.rs`, `package.rs` | — | G1 (subset) → G3 |
| 23 | XLSX write | `xlsx/write.rs`, `sheet_write.rs` | — | G1 (subset) → G3 |
| 24 | XLSX styles, theme | `xlsx/styles.rs`, `theme.rs` | — | G3 |
| 25 | XLSX tables, CF, validation, rich data, array formulas | `xlsx/*` | — | G3 |
| 26 | XLSX drawings, charts, chartex, ink | `xlsx/drawing*`, `chart.rs`, `chartex.rs` | — | G4 |
| 27 | XLSX pivot caches | `xlsx/pivot.rs` | — | G4 |
| 28 | CSV/TSV read and write | `xlsx/csv.rs` | — | G3 |
| 29 | XLSB import | `xlsx/xlsb.rs` | — | G3 |
| 30 | ODS import | `xlsx/ods.rs` | — | G3 |
| 31 | XLS import (CFB, BIFF) | `xlsx/xls/*`, `cfb.rs` | — | G3 |
| 32 | Encrypted workbooks (MS-OFFCRYPTO) | `xlsx/crypto*` | — | G3 |
| 33 | Session, history, selection | `engine/lib.rs`, `selection.rs` | — | G1 (slice) → G2 |
| 34 | Command registry and catalog | `engine/cmd/mod.rs`, `catalog.rs` | — | G1 (slice) → G6 |
| 35 | Commands: edit, format, sheet, view | `engine/cmd/*` | — | G6 |
| 36 | Commands: data (sort, filter, fill, tables, outline) | `engine/cmd/data.rs`, `fill.rs`, `outline.rs`, `tables.rs` | — | G6 |
| 37 | Commands: formulas, review, spelling, inspect | `engine/cmd/*` | — | G6 |
| 38 | Commands: insert, draw, charts, slicers | `engine/cmd/*` | — | G6 |
| 39 | Commands: pivots, what-if, solver, forecast, analysis | `engine/cmd/*`, `pivot*.rs` | — | G6 |
| 40 | Print, pagination, paper | `engine/cmd/print.rs`, `paper.rs` | — | G6 |
| 41 | Display formatting | `engine/display.rs` | — | G2 |
| 42 | Clipboard (incl. HTML) | `engine/clip_html.rs` | — | G6 |
| 43 | Samples | `engine/sample.rs` | — | G6 |
| 44 | File I/O dispatch, sniffing, HTML export | `engine/io.rs` | — | G1 (XLSX) → G3 |
| 45 | PDF writer | `pdf/*` | — | G4 |
| 46 | Chart layout, rasteriser, sparklines | `chart/*` | — | G4 |
| 47 | CLI (all subcommands) | `apps/gridcraft-cli` | — | G1 (`run`, `cat`) → G6 |
| 48 | MCP server | `mcp/*` | — | G6 |
| 49 | Control channel | `ui-egui/control.rs`, `apps/gridcraft/control_server.rs` | — | G8 |
| 50 | UI: shell, ribbon, QAT, backstage | `ui-egui/*` | — | G8 (blocked on craft-libs) |
| 51 | UI: grid, editor, formula bar, tabs, status bar | `ui-egui/*` | — | G8 |
| 52 | UI: dialogs, task panes, chart view, pivot pane | `ui-egui/*` | — | G9 |
| 53 | UI: KeyTips, keymap, themes, dark mode | `ui-egui/*` | — | G9 |
| 54 | UI: i18n (nine languages), formula locale | `ui-egui/i18n*`, `formula_locale.rs` | — | G9 |
| 55 | Accessibility | eframe AccessKit | — | G8 |
| 56 | Desktop integration: menus, file open events, dialogs, clipboard, printing | `apps/gridcraft/*` | — | G10 (blocked on craft-libs) |
| 57 | Web build | `apps/gridcraft-web` | — | G10 |
| 58 | Packaging and release workflows | `packaging/`, `.github/workflows/` | — | G10 |
| 59 | Quality gate (`craftci`) | `xtask` | — | G0 |

## 11. Milestones

Each milestone is a series of small PRs (one component per PR, with a task id in every commit,
for example `G1.3: COW cell store`). Hour estimates aren't given yet: they are calibrated after G1
from measured slice effort, and then added to `ROADMAP.md`.

**G0: groundwork.** `go.mod`, the package skeleton with `doc.go` files, `tools/craftci` with fmt,
vet, staticcheck, nilaway, nopanic, the layer check and the assets check, a Go job in
`.github/workflows/ci.yml`, and `tools/difftest` able to run the Rust CLI. No behaviour yet.
*Exit:* the gate is green on an empty skeleton in CI, next to the Rust job.

**G1: first vertical slice — open, edit, save.** The smallest end-to-end path that proves the
architecture, with its limits stated:
1. `internal/core`: addresses, ranges, `Value`, `CellError`, coercion, date serials; all 22 Rust
   `core` tests ported.
2. `internal/model` subset: workbook, sheets, the banded COW store, cells with value, formula text
   and style index; snapshots, undo and redo; the alias-checking `Clone` test.
3. `internal/xlsx` subset: read and write the package, workbook, sheets, shared and inline strings,
   numbers, booleans, errors and formula text with cached values. Every part the slice doesn't
   model (`styles.xml`, theme, drawings…) is kept and written back unchanged, so the slice never
   loses formatting it doesn't understand. Hostile-input caps from day one.
4. `internal/engine` subset: `Session.Execute` with the recover-and-restore guard, `cell.set`,
   `edit.undo`, `edit.redo`, `file.open`, `file.save` with the same ids, params and errors as Rust.
5. `cmd/gridcraft-cli run --in --cmd --out` and `cat` with the Rust flags and output.
6. Verification: ported tests; fuzzing of the reader and of command params; difftest over
   generated workbooks (Go output reopened by the Rust CLI with no warnings, `cat` equal); one
   Rust-saved sample (`--sample sales`) opened, edited and saved by Go, then reopened by Rust and
   compared.

*Not in G1:* recalc (formulas keep their cached values; edited formulas are saved without a cached
value and marked for full recalc on load), style editing, any other command. These are stated in
the G1 PR and in this checklist.

**G2: model, formulas and number formats.** The rest of `core`, `numfmt`, `formula`, `model`;
display formatting; selection.

**G3: file formats.** Full XLSX read/write and pass-through, CSV/TSV, XLSB, ODS, XLS, encryption.
XML benchmark gate (§5). Corpus run once available.

**G4: calculation and output.** `functions` (by category), `calc`, charts and drawings in XLSX,
pivot caches, `chart`, `pdf`.

**G5: parallel recalc.** Beyond Rust parity (gap #2): multithreaded recalc behind the same
results, proven by differential and race tests.

**G6: commands, CLI and MCP; headless cut.** Every registered command (313 per `docs/architecture.md` on 2026-10-10; about 338 `cmd!` entries on 2026-10-20; the exact list comes from `gridcraft-cli commands --json` in G0), the catalog and parity checklist,
every CLI subcommand, the MCP server. *Exit:* the headless cut (§2), with owner approval.

**G7: UI spike** (can start in parallel with G3 on the toolkit-only parts). Gio prototype against
the five criteria in §5; wasm size and start-up measured; written up with a recommendation for the
owner. **Craft-libs-dependent parts wait for the Go craft-libs.**

**G8–G9: UI.** Shell, ribbon, grid, editor, control channel, accessibility (G8); dialogs, panes,
KeyTips, themes, i18n (G9). Checked against `docs/ui-parity.md` and the observed-behaviour notes by
screenshot comparison, as today. *Blocked on the Go craft-libs.*

**G10: desktop integration, web, packaging; full cut.** Native hooks through craft-libs, the web
build, packaging and release workflows. *Exit:* the full cut (§2), with owner approval.

## 12. Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Undo/model aliasing bugs from the COW redesign silently corrupt history or saved files | Medium | High | §6.1 design proven in G1; reflection alias test; command-sequence fuzz with undo/redo and round-trip invariants before any command beyond the slice. |
| Run-time panics Go can't rule out statically | High | High | §7 layers: lint, nilaway, recover at every entry, fuzzing, regression tests. |
| No Go toolkit reaches egui's level for this UI (density, IME, accessibility, web) | Medium | High | Measured spike before any UI work; Fyne fallback; owner decision if both fail. |
| WASM download too large or too slow to start | Medium | Medium | Measured in G7; TinyGo evaluated; web build can lag desktop (owner's call). |
| `encoding/xml` too slow for large workbooks (already a gap at 100 MB) | High | Medium | Benchmark gate in G3; purpose-built tokenizer if needed. |
| Floating-point last-bit differences in functions | Medium | Low–Medium | Tolerance-based tests; differential reports; Excel observation where they disagree. |
| Craft-libs rewrite not ready when the UI is | Medium | Medium | UI and desktop integration milestones are explicitly blocked; headless work (G0–G6) doesn't need it. |
| Two codebases drift: Rust keeps changing upstream while the port is in progress | High | Medium | Differential tests run on every PR against the Rust build at the same commit; any Rust change to a ported component needs the Go change in the same PR (rule added to `AGENTS.md` at G0). |
| cgo creeping in through UI or platform modules breaks cross-compilation | Medium | Medium | `internal/` is cgo-free (checked); any cgo is confined to `cmd/gridcraft` and craft-libs, and listed in [Exceptions](#13-exceptions-to-go-only). |
| A third-party Go module is abandoned or has an incompatible licence | Low | Medium | Standard library first; the dependency table with reasons; licence check in `craftci`. |
| Partial Go build claims features it doesn't have | Low | Medium | Unported commands and functions aren't registered (§6.6); parity checklist states are evidence-based (§8). |

## 13. Exceptions to Go only

None yet. Any non-Go code that stays (for example a platform shim, or the Rust UI kept past G10)
is listed here with the reason, the owner's approval and a plan to remove it, if there is one.

| Code | Language | Why | Approved | Removal plan |
|---|---|---|---|---|

## 14. Open items

Not blocking G0–G1; settled before the milestone named.

- **Go craft-libs** (`computernut43/craft-libs`): the owner will say when it's ready. Needed for G7's
  native parts, G8–G10. Its Go API decides how clipboard, dialogs, menus, file-open events and
  printing are wired.
- **Excel corpus access**: approved; credentials and fetch mechanism to be set up before G3's
  corpus run.
- **Windows resource tool** for exe icons and version info (G10).
- **Upstream**: `storytold/gridcraft` is the upstream of this fork; whether the migration is offered
  upstream is the owner's decision and doesn't change this plan.

## Revision history

| Date | Change | Summary |
|---|---|---|
| 2026-10-20 | major | Created from the Phase 1 audit: structure, mapping, dependency decisions, never-crash in Go, testing, platforms, checklist, milestones G0–G10, risks |
