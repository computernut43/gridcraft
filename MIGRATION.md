# GridCraft: Rust → Go migration plan

> **Status:** proposed 2026-10-20; G0 done (gates, reference harness, CI); G1 next · **Created:** 2026-10-20 · **Rust baseline:** `23829d8`
> (computernut43/gridcraft `main`)
>
> This plan covers the migration of GridCraft from Rust to Go. Go code lands component by component; no Rust
> code is removed until the matching Go code builds, passes its tests and has been reviewed (§9). It follows
> the plan already running for the sibling [WordCraft](https://github.com/computernut43/wordcraft) (its
> `MIGRATION.md`), and shares its tooling and decisions through
> [`computernut43/craft-libs`](https://github.com/computernut43/craft-libs).

## 1. Goal and decisions so far

| Question | Owner's answer | What it means for the plan |
|---|---|---|
| End state | "Do the same thing with that repo as you just did with the last. Move the codebase from rust to go" (2026-10-20) | A full replacement, as for WordCraft: formulas, functions, calculation, XLSX and the other formats, charts, PDF, engine, CLI, MCP, UI, desktop and browser. Port first, extend later |
| Decisions carried over from WordCraft (same owner, same day) | Browser build required, in Go; cgo allowed "but let's be secure about it"; no upstream merging; code goes to the owner's forks only; port incrementally, verify against Rust, never invent behaviour, keep licences, attribution and clean-room rules, document what isn't ported | Every package below the UI builds for `GOOS=js GOARCH=wasm` (a gate from G0). cgo only for OS integration where `purego`/`x/sys` can't do it (§5). The Rust tree is **frozen** at the baseline as the reference. All work is pushed to `computernut43/gridcraft` (and `computernut43/craft-libs` for shared code) on branch `claude/charming-hopper-wtmlhw` until merged |
| craft-libs | Already being ported to Go in the owner's fork | GridCraft uses `craft-ui` and `craft-print` today; their Go ports land there (craft-libs milestones C4, C5). Shared Go tooling (`devtools/nopanic`, `devtools/gates`, `serdejson`) comes from there; GridCraft requires the module by commit |
| Where the Go code lives | As for WordCraft | **This repository, Go module at the root**, side by side with the frozen Rust tree until one reviewed cutover (§2) |
| The "Rust only" rule and `cargo xtask ci` | Replaced for the port | G0 rewrites `CLAUDE.md`/`AGENTS.md` for the dual-language period and adds the Go gates (§7) |

## 2. Repository layout and how the Rust code gets replaced

**One repository, Go module at the root, Rust frozen beside it, removed in one reviewed cutover** (the reasoning
is WordCraft's §2: the final module path from day one makes cutover a pure deletion, and the Rust reference stays
runnable for the comparisons in §8).

- `go.mod` at the root, module `github.com/computernut43/gridcraft`. Cargo ignores the Go directories and Go
  ignores `crates/`, `apps/`, `xtask/`.
- Shared, language-neutral material stays where it is: `assets/`, `ATTRIBUTION.md`, `NOTICE`, `LICENSE-*`,
  `docs/`, `packaging/`, and the i18n catalogs (moved to `assets/i18n/` when the Go UI needs them).
- **Freeze:** baseline `23829d8c67990e6b52a27bab6a38daf83e07dd54`, pinned in the Go workflow and the agent
  instructions. The Rust code changes only to fix bugs that break its use as the reference.
- **Removal:** the whole Rust tree stays until parity (§6), then `crates/ apps/ xtask/ Cargo.toml Cargo.lock
  clippy.toml rustfmt.toml` go in one cutover PR, after tagging `rust-final`.

```
go.mod                      module github.com/computernut43/gridcraft
cmd/
  gridcraft/                desktop app                     (apps/gridcraft)
  gridcraft-cli/            CLI + MCP entry                 (apps/gridcraft-cli)
  gridcraft-web/            browser build                   (apps/gridcraft-web)
internal/
  core/          L0   addresses, ranges, values, errors, date serials   (crates/core)
  numfmt/        L0   number format codes, General, TEXT()              (crates/numfmt)
  pdf/           L0   PDF writer                                         (crates/pdf)
  formula/       L1   lexer, parser, printer, reference adjustment      (crates/formula)
  functions/     L1   worksheet function library                        (crates/functions)
  model/         L2   workbook, sheets, cells, styles, stores           (crates/model)
  calc/          L2   evaluation, recalculation, dependency graph       (crates/calc)
  xlsx/          L2   XLSX/XLSM/XLSB/ODS/CSV read+write, encryption     (crates/xlsx)
  chart/         L3   chart layout and rasterising                      (crates/chart)
  engine/        L5   Session, undo, command registry, I/O, pivots…     (crates/engine)
  mcp/           L6   MCP server                                         (crates/mcp)
  ui/            L6   front end                                          (crates/ui-egui)
  platform/      L6   per-OS integration                                 (apps + ui-egui OS code)
  rustref/       test harness that runs the frozen Rust CLI as a reference (§8); never linked into apps
tools/
  ci/                       Go counterpart of `cargo xtask ci` (shared gates + layers + assets)
```

The layer table is the Rust gate's (`xtask/src/layers.rs`, checked 2026-10-20): `core`, `numfmt`, `pdf` L0;
`formula`, `functions` L1; `model`, `calc`, `xlsx` L2; `chart` L3; `engine` L5; `ui`, `mcp` L6; apps L7. Within a
layer only the chains `core → numfmt`, `formula → functions`, `model → calc`, `model → xlsx` are allowed, and the
format package `xlsx` may import only `core`, `formula` and `model`. Nothing below L6 imports a UI toolkit.
`tools/ci layers` enforces all of it from `go list -deps`.

## 3. Rust → Go component mapping

Sizes are lines of Rust including in-file tests at the baseline; tests are `#[test]` functions.

| Rust crate | Lines | Tests | Responsibility | Go package | Approach | Difficulty |
|---|---|---|---|---|---|---|
| `core` | 1.5k | 22 | `A1`/`R1C1` addresses, ranges, `Value`, error values, date serials (1900/1904, leap bug), parsing | `internal/core` | Straight port; verified value by value | Low |
| `numfmt` | 3.1k | 35 | Excel number format codes: parsing, sections, conditions, dates, fractions, General, `TEXT()` | `internal/numfmt` | Straight port; f64 formatting must match digit for digit (§8) | Medium |
| `formula` | 2.5k | 30 | Lexer, parser, AST, printer, reference adjustment on insert/delete/move | `internal/formula` | Straight port; parse → print round trip compared with Rust | Medium |
| `functions` | 15.7k | 125 | 513 worksheet functions (math, stat, text, date, lookup, dynamic arrays, financial, engineering, database, ETS), array lifting, criteria | `internal/functions` | Port category by category; each function compared with Rust on generated arguments | High (size, numerics §4) |
| `model` | 3.0k | 14 | Workbook, sheets, sparse cell store, styles, defined names, protection hashes, preserved parts | `internal/model` | Straight port | Medium |
| `calc` | 4.7k | 61 | Evaluator, recalculation order, dependency graph, iteration, dynamic-array spill, TRIMRANGE | `internal/calc` | Port; goroutines where Rust uses rayon, with a deterministic result order | High |
| `xlsx` | 21.9k | 181 | XLSX/XLSM read+write, XLSB, ODS, CSV, OLE/CFB, [MS-OFFCRYPTO] decryption, charts, drawings, pivots, tables, rich data, passthrough | `internal/xlsx` | Port. XML read with the quick-xml-0.41-compatible tokenizer and zip reader built for WordCraft (moved to craft-libs so both apps share them, §4); hand-written XML writer | High (size, fidelity) |
| `chart` | 4.0k | 34 | Chart model resolution, axes, trend lines, sparklines, rasterising | `internal/chart` | Port; rasteriser decision shared with WordCraft's render spike | High |
| `pdf` | 1.1k | 14 | PDF output for printing/export | `internal/pdf` | Straight port (own writer, ISO 32000) | Medium |
| `engine` | 33.6k | 345 | `Session`, selection, undo, **about 340 commands** (`cmd!` registrations), Excel feature catalog (274/290 ribbon features), conditional formats, fill, pivots, tables, solver, what-if, samples, I/O dispatch | `internal/engine` | Port the framework, then commands namespace by namespace, each compared through both CLIs | High (size) |
| `mcp` | 1.6k | 21 | MCP server: every command as a tool, headless or bridged | `internal/mcp` | Port onto `modelcontextprotocol/go-sdk`; tool names and schemas identical | Low–medium |
| `ui-egui` | 36.5k | 699 | Grid canvas, ribbon, formula bar, dialogs, charts UI, i18n, control channel, a11y | `internal/ui` | Rewrite on the toolkit WordCraft's spike chooses (one decision for both apps) | **Very high** |
| `apps/gridcraft` | 2.0k | 28 | eframe shell, OS integration, craft-print | `cmd/gridcraft`, `internal/platform` | Rewrite | High |
| `apps/gridcraft-cli` | 2.5k | 29 | `info convert eval cat check run commands functions mcp send version` | `cmd/gridcraft-cli` | Port with identical flags and output; it is also the comparison harness's other half | Low |
| `apps/gridcraft-web` | 0.6k | 0 | eframe web runner | `cmd/gridcraft-web` | Rewrite with `syscall/js` | High |
| `xtask` | 1.6k | 32 | ci, layers, assets, parity, wasm | `tools/ci` | Port the gates; `parity` regenerates `docs/parity-checklist.md` from the Go catalog | Low–medium |
| craft-libs `craft-ui`, `craft-print` | n/a | n/a | Shared UI pieces, native printing | `github.com/computernut43/craft-libs/{ui,print}` | Ported in craft-libs | High |

## 4. Library decisions

As for WordCraft (its §4, checked 2026-10-20 against the module proxy and source); allowed licences MIT, BSD,
Apache-2.0, ISC, Unlicense, Zlib; **no GPL/LGPL/AGPL code is linked**. Go `1.26` in `go.mod`, toolchain `go1.27.3`.

| Need (Rust crate) | Go choice | Notes |
|---|---|---|
| ZIP (`zip` 3, deflate) | Own reader matching zip 3.0 (from WordCraft's `internal/docx`, moved to craft-libs); `archive/zip` + `compress/flate` for writing | `archive/zip` refuses and accepts different damaged archives than the zip crate (found and measured in WordCraft M1c.2) |
| XML (`quick-xml` 0.41) | Own tokenizer matching quick-xml 0.41 (from WordCraft's `internal/docx/xml.go`, moved to craft-libs); hand-written writer | `encoding/xml` gives different trees (line ends, attribute whitespace) |
| JSON (`serde_json`, `float_roundtrip`) | craft-libs `serdejson` | Reads and writes exactly as serde/serde_json 1.0.151; verified there and in WordCraft |
| Crypto (`aes`, `sha1`, `sha2`) | `crypto/aes`, `crypto/cipher`, `crypto/sha1`, `crypto/sha512`, `crypto/sha256` | Standard library |
| Regex (`regex`) | `regexp` | Both RE2-family, linear time. Syntax differences (Unicode classes, flags) are listed per function in `REGEXTEST`/`REGEXEXTRACT`/`REGEXREPLACE` and compared with Rust; anything Go can't express is reported, not invented |
| Images (`image`, png/jpeg) | `image/png`, `image/jpeg` | Standard library |
| Parallelism (`rayon`) | goroutines + `sync` | Results gathered in index order so output is deterministic |
| Logging (`log`) | `log/slog` | |
| MCP | `github.com/modelcontextprotocol/go-sdk` | Same choice as WordCraft |
| UI, rasteriser, platform | Decided once for both apps by WordCraft's spikes (§4 there) | GridCraft's grid canvas is part of the spike's acceptance list |
| `js-sys` (date `now()` on wasm) | `time.Now()` (works under `GOOS=js`) | |

**Floating point — the main numeric risk.** Excel results are IEEE f64, and the Rust code relies on Rust's f64
semantics, which differ from Go's in places that matter here:
- `f64::max`/`min` ignore NaN (Go's built-ins propagate it); `clamp` keeps NaN; float → int casts saturate (Go's
  are undefined out of range); `round` is half away from zero (Go's `math.Round` too). WordCraft's
  `internal/doc/rustmath.go` already has these helpers; they move to craft-libs.
- Formatting: Rust's `{}`/`{:e}`/`{:.N}` and `to_string()` differ from `strconv`; every place a number becomes
  text gets a helper with the Rust rule, compared digit for digit.
- Transcendentals (`ln`, `exp`, `powf`, `sin`, …) call the platform libm in Rust, so the Rust reference itself
  differs by an ulp across operating systems. Go's `math` is portable. **Rule:** arithmetic, rounding and
  formatting must match exactly; a transcendental result is compared within 2 ulp (and its displayed value,
  after Excel's 15-digit rounding, exactly). Any function whose displayed result differs is a bug.

## 5. Security rules for cgo and native code

WordCraft's §5 applies unchanged: cgo only for OS integration that `purego`, `x/sys` or `godbus/dbus` can't
reach; no cgo below L6; no Rust linked into Go (the Rust reference runs only as a subprocess in tests, with an
empty environment, a time limit and capped output — `internal/rustref`); every cgo file reviewed and listed.

## 6. Feature parity checklist

Status values: `not started` · `in progress` · `ported` (Go tests pass) · `verified` (checked against the Rust
reference as §8 describes).

| Feature | Rust source | Status |
|---|---|---|
| Cell addresses, ranges, values, errors, date serials | `crates/core` | not started |
| Number formats, General, `TEXT()` | `crates/numfmt` | not started |
| Formula lexer, parser, printer, reference adjustment | `crates/formula` | not started |
| Worksheet functions (513) | `crates/functions` | not started |
| Workbook model, styles, names, protection | `crates/model` | not started |
| Calculation: evaluation, recalc, spill, iteration | `crates/calc` | not started |
| XLSX/XLSM read+write | `crates/xlsx` | not started |
| XLSB, ODS, CSV import; encrypted workbooks | `crates/xlsx` | not started |
| Charts and sparklines | `crates/chart` | not started |
| PDF output | `crates/pdf` | not started |
| Session, undo, command registry | `crates/engine` | not started |
| Commands (about 340), by ribbon tab | `crates/engine/src/cmd/*` | not started |
| Pivots, tables, conditional formats, fill, solver, what-if | `crates/engine` | not started |
| MCP server | `crates/mcp` | not started |
| CLI (`info convert eval cat check run commands functions mcp send version`) | `apps/gridcraft-cli` | not started |
| UI | `crates/ui-egui` | not started |
| Desktop app, OS integration | `apps/gridcraft` | not started |
| Browser build | `apps/gridcraft-web` | not started |
| Gates (fmt, vet, nopanic, layers, assets, staticcheck, tests, wasm, licences, vuln) | `xtask` | ported (G0): `go run ./tools/ci`; layers (with the `xlsx` format rule) and assets ported from `xtask` with its tests; shared gates from craft-libs. `internal/rustref` runs the baseline `gridcraft-cli` (checked against it: `eval`, `run --sample`, `convert` to JSON) |

## 7. Engineering rules for the Go code

The repository's rules carry over unchanged in intent; the Go form is the same as WordCraft's §7:
- **Never crash.** No `panic`, no `Must…` with non-constant arguments, no `log.Fatal*`/`log.Panic*`, no `os.Exit`
  outside `package main` (craft-libs `devtools/nopanic`, a gate). `recover` guards every boundary
  (`Session.Execute`, I/O, MCP, control channel). Input-derived numbers are hostile: bounds-checked indexing,
  saturating arithmetic, capped allocations (huge ranges, `REPT`, `SEQUENCE`, whole-column references), bounded
  recursion (formula nesting, dependency chains, names). Every crash fix lands with a regression test; every
  reader has a `go test -fuzz` target.
- **Everything is a command**, ids unchanged; programmatic calls never open dialogs.
- **Assets:** the absolute rule stands. No vendor iconography or files, ever; every asset has an
  `ATTRIBUTION.md` row (`go run ./tools/ci assets`).
- **Clean room** unchanged: observation only, public specs, no GPL/AGPL/LGPL code.
- **Gates before every commit:** `go run ./tools/ci` (gofmt, vet, nopanic, layers, assets, staticcheck, tests,
  wasm, dependency licences, govulncheck); `cargo xtask ci` only when Rust is touched (reference fixes).
- One task id per commit (`G1.2: date serials`).

## 8. Testing and compatibility strategy

The same method that verified WordCraft's model byte for byte:
1. **Port the Rust tests** of a component first, then implement until they pass.
2. **Differential testing against the frozen Rust code.** For each component a small reference program in
   `internal/<pkg>/testdata/*-oracle` links the baseline crate by path (exact dependency versions pinned to the
   baseline's `Cargo.lock`) and answers generated inputs; the Go test (`TestMatchesRust`, run when an
   environment variable names the reference binary, and in CI) compares answers byte for byte, or within the
   ulp rule of §4 for transcendental results. Generators cover valid inputs, near-misses and hostile inputs.
3. **Mutation checks:** deliberate bugs are planted in the Go code to prove each comparison catches them; the
   counts are recorded here.
4. **Whole-program comparison:** `internal/rustref` runs the baseline `gridcraft-cli` (`eval`, `run`, `cat`,
   `convert`); random formulas and command scripts go through both CLIs and must print the same results and
   save the same workbooks.
5. **Fuzzing** of every reader (formulas, number formats, XLSX, CSV, ODS, XLSB) with no crash and stable
   round trips.
6. **Corpus:** `gridcraft-cli check` (cached-value calc oracle and round-trip fidelity) is ported early so the
   Go side can be run over the same pinned real-world corpus as the Rust side.

## 9. Milestones

| Milestone | Scope | Exit criteria |
|---|---|---|
| G0 | This plan; Go module; `tools/ci` gates; `internal/rustref`; Go workflow (gates, Windows, macOS, Linux builds, reference comparisons); agent instructions | Gates green in CI; plan reviewed |
| G1 | `core`, `numfmt` | Verified against Rust (values, addresses, dates, every format code generated) |
| G2 | `formula`, `functions` | Parse/print verified; every function verified on generated arguments under the §4 rule |
| G3 | `model`, `calc`, `gridcraft-cli eval` | Random formulas over random sheets evaluate identically through both CLIs |
| G4 | `xlsx` (XLSX/XLSM first, then CSV, ODS, XLSB, encryption) | Read → JSON and write → Rust re-read identical on generated and corpus workbooks |
| G5 | `engine` framework + commands by tab, `cli run`, `mcp` | Random command scripts identical through both CLIs; hostile-params test; fuzzers |
| G6 | `chart`, `pdf` | Chart models identical; raster output within the visual-diff threshold |
| G7 | UI, desktop, browser | UI parity checklist (`docs/ui-parity.md`) reached on the Go toolkit |
| G8 | Cutover | Parity reached, owner review, `rust-final` tag, Rust tree removed |

Each milestone ends with its rows in §6 updated, a commit per task, and a push to the owner's fork.

## 10. Known risks and open items

| Risk | Impact | Mitigation |
|---|---|---|
| f64 behaviour differs between Rust and Go (NaN in min/max, saturating casts, formatting, libm) | Wrong numbers or displays | Rust-semantics helpers, digit-for-digit formatting comparison, the ulp rule for transcendentals (§4) |
| 513 functions × edge cases | Long tail of mismatches | One generator per argument kind; per-function comparison counts recorded in `docs/function-parity.md` |
| Regex dialect differences (`regex` vs `regexp`) | REGEX* functions differ | Compared with Rust; differences listed, not invented |
| UI toolkit not decided | G7 blocked | Shared decision with WordCraft's spike |
| Shared code drifts between WordCraft and GridCraft | Duplicate bugs | XML tokenizer, zip reader, Rust-math helpers and serdejson live in craft-libs |

## Revision history

| Date | Change | Summary |
|---|---|---|
| 2026-10-20 | minor | G0: Go module, `tools/ci`, `internal/rustref`, Go workflow (gates, Windows, macOS, reference), agent instructions |
| 2026-10-20 | major | First version: audit at `23829d8`, decisions carried over from WordCraft, mapping, library decisions, numeric rules, milestones |
