# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Nothing yet.

## [0.5.0] - 2026-08-20

Text fidelity, end to end. Real font metrics for the 14 standard PDF
fonts, correct glyph geometry, cell content that no longer loses
characters at a table's edge, and the first measured evaluation of table
behaviour against a public dataset.

One default changes behaviour for every existing caller — see
**Changed (behaviour)** — so this is a minor bump, not a patch.

### Changed (behaviour)

- **`DefaultWordOpts()` now enables `UseExplicitSpaces`.** pdfplumber's
  `WordExtractor` ends a word *at* a whitespace glyph, before any gap
  test runs; pdfgrab dropped spaces and re-inferred boundaries purely
  from the gap. At small type that silently over-merges: an 8pt space is
  278/1000 × 8 = 2.22pt, under the 3pt `XTolerance`, so
  `Wim illegible 3,142 (16,048)` came back as one run while pdfplumber
  returned four words. Body type in real documents is routinely 8–9pt, so
  this was not an edge case. With it on, pdfgrab matches pdfplumber
  word-for-word and coordinate-for-coordinate across all 12 Latin
  standard fonts at 8/12/24pt. The low-level `WordExtractor` is
  unchanged and stays pdfplumber-exact.

### Fixed

- fix: standard-14 fonts now resolve to their true Adobe AFM advance
  widths. PDF 1.7 §9.6.2.2 lets those 14 fonts omit `/Widths` entirely,
  on the assumption that a consumer already knows their metrics —
  pdfgrab did not, so every glyph in such a font fell through to a flat
  500/1000 guess. `i` and `m` came out the same width, and the error
  accumulated across a line into up to ~10pt of word-bbox drift. Since
  the `text` and `lines_strict` table strategies infer column boundaries
  from word positions, that drift could move a column split. Common
  substitute names (`Arial`, `Times New Roman`, `Courier New`, subset
  tags, case and whitespace variants) alias to their metric equivalents.
  Narrow and Condensed variants deliberately do **not** match: they
  share a family name but not the metrics, and returning regular-width
  numbers would produce a confident, wrong bbox rather than an honest
  fallback.
- fix: Symbol and ZapfDingbats are decoded with their own built-in
  encodings instead of StandardEncoding. Symbol code `0x61` previously
  decoded as `a` rather than `alpha`, so these fonts extracted as
  mis-mapped Latin — a text-correctness bug, not only a metrics one.
  Greek and the wider maths repertoire (`Alpha`, `universal`, `club`, …)
  now resolve through the shared Adobe Glyph List path, which also fixes
  `/Differences` arrays in ordinary fonts that name those glyphs.
  ZapfDingbats' `aNN` names stay font-scoped on purpose: Adobe ships
  them separately from the AGL because they are font-specific, and
  resolving them globally would corrupt any font whose `/Differences`
  happens to name `a1`.
- fix: all 14 standard fonts now resolve every AFM glyph to a distinct
  rune (229 each for the Latin twelve, 190 Symbol, 202 ZapfDingbats). A
  coverage test asserts those counts, so bundled-but-unreachable metrics
  cannot recur silently.
- fix: glyph bounding boxes rest on the font's real descender. The
  standard-14 exemption that permits omitting `/Widths` also permits
  omitting `/FontDescriptor`, so `Ascent`/`Descent` were unavailable and
  a glyph's box collapsed to `[baseline, baseline+size]`, sitting
  `descent*size` too high — 2.484pt at 12pt text, 4.968pt at 24pt. A
  second, independent bug compounded it: descent was scaled by 0.001 but
  not by the font size, so even a font that *did* supply a descriptor got
  a descender contribution short by a factor of the font size. Both are
  fixed. This governs row detection — `lines_strict` and `text` infer row
  boundaries from word Y extents — so it affected table structure, not
  just reported coordinates.
- fix: a glyph straddling a table's outer edge is no longer discarded.
  Cell assignment picks the cell containing a glyph's centre, which is
  correct for an interior boundary but deletes content at the table's
  own edge, where no competing cell exists. On a real 10-K balance sheet
  the closing `)` of `(16,048)` sat 0.008pt beyond the last column and
  was dropped, turning accounting notation for −16,048 into +16,048;
  across five financial statements it flipped the sign of 19% of all
  negative numbers while leaving every magnitude correct.
- fix: `MergeSplitTokens` is decided once per table, not per row. A
  column boundary is a property of the table, so a per-row decision
  sheared the grid: on 3M's 2018 10-K page 58 the header band contains
  the split (`December 3` + `1,`) and the data rows below it do not, so
  the header merged to 3 cells while the data rows stayed at 5 and the
  second date sat above the first column of figures. The decision is now
  made once per boundary across every row and applied uniformly, keeping
  the table rectangular. The trade is that one row containing a split
  collapses that boundary for the whole table — the right direction,
  since rectangularity matters more to a positional consumer than
  per-cell purity.

### Added

- `TableSettings.MergeSplitTokens` (default **off**) merges two adjacent
  cells when the column boundary between them falls inside a single
  token. The `text` strategy derives columns by clustering word edges, so
  a narrow band that happens to align down the page becomes a column even
  when it cuts a value in half — a real 10-K balance sheet yields
  `| ( | 16,135) |` for `(16,135)` and `| December 3 | 1, |` for
  `December 31,`. Off by default because pdfplumber produces the same
  splits (verified against pdfplumber 0.11.9), so enabling it silently
  would break parity. Merging is bounded by `TextTolerance`, the same
  threshold word grouping uses, so a genuine column gutter is left alone.
  Cell bboxes are merged too, which matters when they drive a citation
  highlight.
- `StrategyAuto` (**opt-in**, not a default) picks a strategy per axis:
  `lines` where that axis is ruled, `text` where it is not *but the other
  axis is*, and `lines` when neither is. That last branch is
  load-bearing — falling back to `text` on an unruled page is what makes
  a naive `lines`→`text` fallback score worse than `lines` alone
  (precision 0.223 against 0.865), because prose has word alignment too
  and `text` will happily report a table for it. Rulings on the other
  axis are the evidence that a table is genuinely present; without that
  evidence `Auto` declines to guess. It stays opt-in because the
  hypothesis it was built to test came back negative — see
  [the negative result](docs/evaluations/2026-08-02-strategy-auto-negative-result.md).
- `BBox.Viewport(pageHeight, scale)` and `BBox.Normalized(pageWidth,
  pageHeight)` return a `ViewRect` in viewer coordinates (origin
  top-left, Y down) for drawing citation highlights over a rendered
  page. Every coordinate the package reports is already normalised —
  MediaBox origin translated to (0,0) and `/Rotate` applied — so the
  only conversion needed is the Y flip, now done once and tested.

### Changed

- Golden position parity is now asserted at **0.01pt on both axes**,
  down from a 15pt envelope. Measured drift against the fixtures is
  exactly 0.0000pt. The old envelope was wide enough to pass with the
  font-metric bugs fully present, so it could not have caught them.
- A font-coverage test corpus replaces the previous golden corpus of 26
  words in a single font, which could not validate extraction fidelity.

### Evaluation

First measurement of pdfgrab against a public dataset. Harness in
[`bench/icdar2013`](bench/icdar2013/), write-ups in
[`docs/evaluations/`](docs/evaluations/).

ICDAR 2013 Table Competition (Smock's corrected edition — 125
born-digital PDFs, 39,524 ground-truth adjacency relations), against
pdfplumber 0.11.9:

| system | precision | recall | F1 |
| --- | --- | --- | --- |
| pdfgrab (`lines`) | 0.865 | 0.229 | **0.362** |
| pdfplumber (`lines`) | 0.868 | 0.235 | **0.370** |
| **pdfgrab + oracle boundaries** | **0.948** | **0.922** | **0.935** |

Two conclusions:

- **Parity with pdfplumber holds at the table level**, not only for text.
- **0.362 → 0.935 given a correct grid.** Essentially the entire
  end-to-end gap is table *structure*, not text extraction — cell
  filling, coordinates and text fidelity are not the limiting factor. A
  layout model supplying row/column geometry converts almost the whole
  gap, and pdfgrab keeps what a generative model cannot give: exact cell
  text and exact coordinates. Integration uses `StrategyExplicit` with
  `MergeSplitTokens = false`; leaving it on drops 0.935 to 0.726, because
  that setting exists to repair a geometric guess and there is nothing to
  repair in a correct grid.

### CI

- The test matrix runs on every pull request, deliberately unfiltered by
  base branch: a `branches: [main]` filter means stacked PRs (base = a
  feature branch) silently get no CI at all.

## [0.4.0] - 2026-07-03

Word segmentation that adapts to font size, and honours the PDF's own
whitespace.

### Added

- `TextOpts.XToleranceRatio` (default 0.15) makes the word-gap tolerance
  size-relative — ratio × glyph size — instead of a fixed 3pt applied
  uniformly across every font size in a document. Matches pdfplumber's
  `x_tolerance_ratio` semantics. A fixed 3pt was too coarse for small
  body text, so tightly-set design PDFs merged words outright
  (`Vectorsarefuzzy`, `whatevershapeyourstackprefers`).
- `TextOpts.UseExplicitSpaces` honours the PDF's own whitespace glyphs as
  authoritative word boundaries instead of dropping them and re-inferring
  from gaps. Strictly more robust for the many PDFs that emit real
  spaces. Enabled by `DefaultTextOpts` here; `DefaultWordOpts()` follows
  in 0.5.0.
- The Vectorless whitepaper and technical docs as regression fixtures.

Both generalizations are enabled through `DefaultTextOpts`. The
low-level `WordExtractor` is untouched and stays pdfplumber-exact.

## [0.3.1] - 2026-05-29

Performance fix for the cell-finding stage. The v0.3.0 public API
surface is unchanged; v0.3.1 only adds two optional `TableSettings`
safety-cap fields (both default to "unset", so existing callers are
unaffected) and makes `FindTables` / `ExtractTables` dramatically
faster on densely-ruled pages.

### Performance

- perf: grid-indexed cell finding — `intersectionsToCells` goes from
  O(n²/n³) to O(cells). Intersections lie on a lattice (unique X
  positions × unique Y positions); the finder now indexes points into
  that grid so each anchor locates its `below` / `right` candidates and
  the closing corner in O(1) instead of rescanning the entire
  intersection suffix. Dense financial pages (a fine ruling grid with
  hundreds of rulings per axis → tens of thousands of intersections)
  that previously hung for minutes now finish in milliseconds. On a
  synthetic 200×200 lattice (40,401 intersections, 40,000 cells) the
  cell finder drops from ~78 s to a few ms. The cell-selection order
  (nearest-first outward walk, below-outer / right-inner,
  first-close-wins, one cell per anchor) is preserved byte-for-byte, so
  the emitted cell set is identical — the golden fixtures
  (`issue-466-example`, `table-3x4-borderless`) produce the same tables
  as before.
- perf: `edgesToIntersections` replaced its `V×H` pairwise scan with a
  sweep — horizontal edges are sorted by Y and each vertical edge only
  tests the band of horizontals whose Y lies within its span (located by
  binary search). The intersection-tolerance semantics are unchanged.

### Added

- `TableSettings.MaxEdgesPerAxis` (default 1000) and
  `TableSettings.MaxIntersections` (default 50000): defense-in-depth
  caps. If a page yields more than `MaxEdgesPerAxis` vertical OR
  horizontal edges after merging, or more than `MaxIntersections` edge
  crossings, table finding is skipped for that page (no tables returned)
  and a warning is logged. A real table never has this many rulings or
  crossings on one axis; the caps bound the work even if some future
  input defeats the grid optimization. Both treat zero as "unset"
  (filled with the default) and a negative value as "disabled".
- `finder_bench_test.go`: `BenchmarkIntersectionsToCellsDenseGrid` and
  `BenchmarkEdgesToIntersectionsDenseGrid` over a 200×200 lattice, plus
  `TestDenseGridTerminatesQuickly` — a hard wall-clock assertion
  (< 2 s for 200×200) that fails CI if the quadratic behaviour ever
  returns.

## [0.3.0] - 2026-05-27

Phase 1.3.D + 1.3.E — text and explicit table-finding strategies, the
`pdfgrab` CLI. Completes pdfplumber parity for the four canonical
table strategies. The v0.2.x public API surface is unchanged; v0.3.0
only widens what's valid in `TableSettings` and adds the new CLI
binary, so existing callers compile and run as-is.

### Added

- `StrategyText`: infer table edges from word alignment. Vertical
  edges come from clusters of words sharing X0 (left), X1 (right), or
  centre position with the per-axis tolerance hardcoded to 1 PDF
  point (matching pdfplumber's `words_to_edges_v`). Horizontal edges
  come from clusters sharing visual top, with both the top and
  bottom of each cluster emitted so the last row gets captured
  (matching `words_to_edges_h`). Threshold via
  `TableSettings.MinWordsVertical` (default 3) and
  `MinWordsHorizontal` (default 1).
- `StrategyExplicit`: caller-supplied edges via
  `TableSettings.ExplicitVerticalLines` /
  `ExplicitHorizontalLines`. When the strategy is `explicit` on an
  axis, the supplied coordinates are the ONLY source of edges on
  that axis; at least two coordinates are required (matching
  pdfplumber's validation). Non-finite values (NaN, Inf) are skipped
  with a `log` warning rather than crashing.
- Mixed strategies: every combination of the four strategies across
  the two axes works (16 combinations total). The two axes' base
  edges are derived independently then merged together for the
  intersection pipeline — no orientation-specific logic leaks
  between them.
- `pdfgrab` CLI binary at `cmd/pdfgrab/`. Subcommand surface
  mirrors pdfplumber's: `extract <file.pdf> [flags]` with
  `--pages 1,3-5`, `--tables`, `--text`, `--format json|text`,
  `--vertical-strategy`, `--horizontal-strategy`, the full set of
  tolerance flags, `--min-words-vertical / horizontal`,
  `--explicit-vertical-lines / horizontal-lines`, and `--indent`.
  Stdlib `flag` package only — no third-party CLI dependencies.
  Positional argument can appear before OR after flags
  (pdfplumber-style invocation). Tested via
  `cmd/pdfgrab/main_test.go` against the existing golden fixtures.
- New `layout.SourceText` enum value tagging edges produced by the
  text strategy. `layout.SourceExplicit` was already in place from
  v0.2.0; the explicit-strategy implementation now writes through
  to it as the primary source.
- Hand-crafted borderless fixture `testdata.TableBorderless()`
  (3-column × 4-row narrative table conveyed by whitespace alignment
  only, no rules drawn). Used by the new text-strategy unit tests
  and pdfplumber parity test. The generated PDF is in
  `testdata/golden/table-3x4-borderless.pdf`.
- Golden-file parity test `TestGoldenTablesTextStrategyAgainstPdfplumber`
  driven by `*.tables-text.expected.json` files. The
  `table-3x4-borderless` fixture matches pdfplumber's
  `find_tables({text, text})` cell-for-cell. Regenerate via the new
  `scripts/capture_pdfplumber_text_golden.py` helper.
- `scripts/capture_pdfplumber_text_golden.py`: tiny Python helper
  that captures pdfplumber's text-strategy output for every fixture
  with a sibling `.tables-text.target` marker. Mirrors the existing
  `scripts/gen_golden.py` workflow for the line-strategy goldens.

### Changed

- `Page.FindTables` / `Page.ExtractTables` no longer return
  `ErrUnsupported` for `text` or `explicit` strategies — all four
  strategies are now implemented. The error is still returned for
  unknown strategy strings (typo guard).
- `TableSettings` field docs updated to reflect the implemented
  semantics of `MinWordsVertical` / `MinWordsHorizontal` and the
  Explicit*Lines slices.
- README's "Tables" section restructured: side-by-side
  pdfplumber→pdfgrab examples for all four strategies, plus a
  mixed-strategy snippet and a new "CLI" section.

### Known limitations

- Cell text fidelity on the text strategy depends on the same font
  metrics as v0.2.x: PDFs that use standard-14 fonts without
  bundled AFM tables can report intra-word gaps as zero, producing
  cells like "Nohorizontal" where pdfplumber gets "No horizontal".
  Structural parity (table count, row count, column count) matches
  exactly; cell text matches verbatim on PDFs whose fonts have
  bundled metrics or `/Widths` arrays. AFM-table bundling is a
  v0.4.x goal.
- Mixed-strategy snap/join uses a single global tolerance. If a
  page mixes drawn rules at one X coordinate and word-cluster
  edges at a slightly different X, the two won't merge unless
  `SnapTolerance` is widened. This matches pdfplumber's behaviour
  but is worth noting for callers tuning a mixed pipeline.

## [0.2.0] - 2026-05-27

Phase 1.3.C — table-finding via ruled lines. Direct port of
pdfplumber's `TableFinder` + cells-from-edges algorithm (`table.py`).
The v0.1.x public API surface is unchanged; v0.2.0 only adds methods
to the `Page` interface and new top-level types, so existing callers
compile and run as-is.

### Added

- `Page.FindTables(settings TableSettings) ([]TableFinder, error)` —
  geometry-only stage of the pipeline. Returns one TableFinder per
  detected table group with the merged edges, intersections, raw
  cells, and assembled per-table CellsGrid exposed for debugging /
  custom rendering.
- `Page.ExtractTables(settings TableSettings) ([]*Table, error)` —
  wraps FindTables, runs per-cell text extraction, returns fully
  populated `Table` structs. Cell text is the dense extract\_text
  output for chars whose centre point falls inside the cell bbox,
  with leading / trailing whitespace stripped. Empty cells produce
  `""`.
- `TableSettings` struct with `DefaultTableSettings()` constructor
  carrying pdfplumber-matching defaults (snap\_tolerance=3,
  join\_tolerance=3, edge\_min\_length=3, edge\_min\_length\_prefilter=1,
  intersection\_tolerance=3, text\_tolerance=3).
- `TableStrategy` enum with constants `StrategyLines`,
  `StrategyLinesStrict`, `StrategyText`, `StrategyExplicit`. Only
  `StrategyLines` and `StrategyLinesStrict` are implemented in this
  release; `StrategyText` and `StrategyExplicit` are deferred to
  v0.3.0 and return `ErrUnsupported` (with a clear "Phase 1.3.D"
  message) so callers don't get silent empty results.
- `Table` (rows × columns of cell text + bbox + per-cell bbox grid),
  `TableFinder` (edges + intersections + cells + tables), `TableBox`
  (one assembled table's geometry: bbox + Rows × Cols grid),
  `Intersection` (one edge-crossing point with its participating
  vertical and horizontal edges).
- Internal `internal/layout` package: `Edge` type with `FromLine`,
  `FromRect`, `FromCurve` constructors, plus the snap → join →
  filter pipeline (`SnapEdges`, `JoinEdges`, `MergeEdges`,
  `FilterEdgesByLength`, `FilterEdgesBySource`,
  `FilterEdgesByOrientation`, `SortEdges`).
- Golden-file parity test against pdfplumber's `find_tables({"lines"})`
  on the `issue-466-example.pdf` fixture (4×3 + 2×3 ruled tables).
  Test infrastructure (`TestGoldenTablesAgainstPdfplumber` in
  `golden_test.go`) loads any `*.tables.expected.json` fixture in
  `testdata/golden/` and compares cell-for-cell after whitespace
  normalisation. Regenerate via `python scripts/gen_golden.py`.
- New hand-crafted fixture: `testdata.TableRuled()` — minimal
  2-column × 3-row ruled table with predictable text ("Name", "Age";
  "Alice", "30"; "Bob", "25") for unit testing the public API
  surface without depending on third-party PDFs. Generator script
  at `scripts/gen_table_fixture.go`.
- Algorithm-level unit tests in `table_test.go`: hand-crafted edge
  lists exercising `edgesToIntersections`, `intersectionsToCells`,
  `cellsToTables`, `assembleTableBox`, and the full `runTableFinder`
  pipeline.
- README "Tables" section with a side-by-side Go / pdfplumber
  example. The example is also extracted as a runnable program at
  `examples/extract_tables/main.go` so changes to the API surface
  break the example at build time.

### Deferred (planned for v0.3.0 — Phase 1.3.D)

- `StrategyText`: infer table edges from word alignment (clusters of
  words sharing x0 / x1 / centre, clusters of words sharing top /
  bottom). Useful for PDFs whose tables have no ruled lines (e.g.
  banking statements, scanned-then-OCR'd documents).
- `StrategyExplicit`: caller-supplied edges via
  `TableSettings.ExplicitVerticalLines` /
  `ExplicitHorizontalLines`. In v0.2.0 these settings are accepted
  and added on top of the derived edges (helpful when a column
  boundary isn't drawn), but they don't form the only source of
  edges yet.

### Known limitations

- The cell-text extraction shares the v0.1.x word-grouping engine,
  which depends on font metrics. Cells whose glyphs use standard-14
  fonts WITHOUT the bundled AFM tables can have intra-word gaps
  reported as "no gap" — e.g. "Hello World" comes out as
  "HelloWorld". This was already documented for v0.1.0; for v0.2.0
  it means the parity test against
  `la-precinct-bulletin-2014-p1.pdf` (which uses Helvetica-Bold)
  fails on cell text equality. The fixture is not checked in to
  avoid CI noise; it'll be re-added once the AFM bundle lands in
  v0.2.x.
- `senate-expenditures.pdf` produces 7 cells where pdfplumber finds
  10. The divergence is in how snap+join unifies edges that share a
  near-collinear endpoint but differ slightly in the perpendicular
  axis; under investigation as a follow-up issue. The fixture is
  not in the golden set yet.

## [0.1.1] - 2026-05-27

### Fixed

- StandardEncoding, WinAnsiEncoding, MacRomanEncoding, and
  PDFDocEncoding are now driven from a single source of truth
  (`encodingRows`) that mirrors pdfminer.six's `latin_enc.py` and PDF
  Reference 1.7 Appendix D.2. The previous tables silently dropped
  ~32 named glyphs per encoding outside printable ASCII — most
  visibly the smart quotes (`’ ‘ “ ”`), en/em dashes (`– —`),
  bullet (`•`), florin (`ƒ`), and dagger marks (`† ‡`). PDFs that
  used these without a `/ToUnicode` map (the common case for PDF/A
  filings, SEC 10-Ks, and most LaTeX-emitted documents) returned
  empty or garbled text where these glyphs appeared.
- `AdobeGlyphToUnicode` now resolves the full Adobe Glyph List for
  common Latin/typographic glyphs (~250 entries) instead of a minimal
  ~30-entry table. Added support for AGL §2 compound names (`f_i`
  decomposes to `fi`) and variant suffixes (`.alt`, `.sc` are
  stripped before lookup).
- StandardEncoding now correctly maps slot 0x27 to `quoteright`
  (`’`, U+2019) and 0x60 to `quoteleft` (`‘`, U+2018), matching the
  PDF spec. WinAnsi/MacRoman/PDFDoc keep ASCII `'` and `` ` `` at
  those slots, as the spec requires.

### Note

This is a behavior change for callers that depended on the pre-v0.1.1
ASCII-identity behavior of StandardEncoding at 0x27 / 0x60. The new
behavior is spec-correct and matches what pdfplumber, pdfminer.six,
and Ghostscript emit for the same input.

## [0.1.0] - 2026-05-26

Phase 1.3.B — words and text extraction. Direct port of pdfplumber's
`WordExtractor`, `extract_text`, `extract_text_simple`. The v0.0.1
public API surface is unchanged; v0.1.0 only adds methods to the
`Page` interface, so existing callers compile and run as-is.

### Added

- `Page.Words(opts WordOpts) ([]Word, error)` — extract positioned
  text runs. Each `Word` carries `Text`, `X0/Y0/X1/Y1` bbox,
  `Upright`, `Direction` (ltr/rtl/ttb/btt), `FontName`, `FontSize`,
  and an optional `Chars` slice (when `WordOpts.KeepChars=true`).
- `Page.ExtractText(opts TextOpts) (string, error)` — page text as a
  single string. Supports both dense (`Layout=false`, the default)
  and layout-preserving (`Layout=true`) modes. The layout mode emits
  a fixed-width grid mimicking `pdftotext -layout` / pdfplumber's
  `extract_text(layout=True)`.
- `Page.ExtractTextSimple(xTolerance, yTolerance float64) (string, error)` —
  no-frills extraction baseline (ports pdfplumber's
  `extract_text_simple`).
- `WordOpts` / `TextOpts` option structs with `DefaultWordOpts()` /
  `DefaultTextOpts()` constructors carrying pdfplumber-matching
  defaults (XTolerance=3, YTolerance=3, Expand=true).
- `BBox` value type with `Union`, `Intersect`, `Contains`, `Snap`,
  `MergeBBoxes`, `BBoxOfChar`, `BBoxOfChars` helpers.
- Internal clustering primitives in `clustering.go`:
  `clusterFloat1D`, `makeClusterDict`, `clusterObjects[T]`,
  `groupObjectsByAttr[T,K]`, `dedupeChars`. Ports of
  pdfplumber/utils/clustering.py.
- Ligature expansion table (ﬁ, ﬂ, ﬀ, ﬃ, ﬄ, ﬅ, ﬆ → fi/fl/ff/ffi/ffl/st).
- Golden-file parity tests against pdfplumber output on three
  fixtures (hello.pdf, rules.pdf, simple1.pdf). Regenerate via
  `python scripts/gen_golden.py`.

### Known limitations

- Word bboxes drift by up to ~10 PDF points from pdfplumber's output
  on standard-14 fonts because the AFM metrics aren't yet bundled.
  Word text + count + order match exactly. The AFM bundle is a v0.2.x
  goal.
- `extract_text_lines` (regex-based line extraction) is not yet
  ported.
- `TextMap.search` is not yet ported.

## [0.0.1] - 2026-05-26

Initial release. Phase 1.3.A — content-stream primitives layer.

### Added

- Public API: `Open`, `OpenBytes`, `OpenFile` → `Document`.
- `Document.NumPages`, `Document.Page(n)`, `Document.Pages()` iterator,
  `Document.Close`.
- `Page.Number`, `Page.Width`, `Page.Height`, `Page.Chars`, `Page.Lines`,
  `Page.Rects`, `Page.Curves`, `Page.Objects`.
- `Char`, `Line`, `Rect`, `Curve`, `Objects` data types with PDF-user-space
  coordinates (origin at bottom-left, Y growing upwards), normalised
  bounding boxes (`x0 <= x1`, `y0 <= y1`).
- Sentinel errors: `ErrInvalidPDF`, `ErrPageOutOfRange`, `ErrUnsupported`,
  `ErrEncrypted`.
- Internal content-stream interpreter (`internal/pdf`) — port of
  pdfminer.six's `PDFContentEmitter`:
  - Graphics-state stack (`q`/`Q`/`cm`/`w`).
  - Path construction (`m`/`l`/`c`/`v`/`y`/`h`/`re`).
  - Path painting (`S`/`s`/`f`/`F`/`f*`/`B`/`B*`/`b`/`b*`/`n`).
  - Text state (`Tc`/`Tw`/`Tz`/`TL`/`Tf`/`Tr`/`Ts`).
  - Text positioning (`BT`/`ET`/`Td`/`TD`/`Tm`/`T*`).
  - Text showing (`Tj`/`TJ`/`'`/`"`).
  - Form XObject inlining (`Do`).
- ToUnicode CMap parser (`bfchar`, `bfrange` with hex-string and array
  destinations, UTF-16BE decoding including surrogate pairs).
- Font subsystem: simple Type1/TrueType fonts with `/Encoding` and
  `/Differences` overlays; composite Type0 fonts with descendant
  CIDFont width arrays (`/W` short and range forms, `/DW`).
- Standard PDF encodings: StandardEncoding, WinAnsiEncoding,
  MacRomanEncoding, PDFDocEncoding (printable-ASCII identity + WinAnsi
  high-range smart quotes / dashes / euro).
- Adobe glyph-name table for common glyphs + `uni`/`u` recognisers for
  the rest.
- Test fixtures generated programmatically (`testdata/fixtures.go`):
  hand-crafted Hello-World and ruling-lines PDFs.

### Out of scope (later phases)

- `Page.ExtractText`, `Page.Words` (1.3.B).
- `Page.FindTables`, `Page.ExtractTables` (1.3.C/D).
- Encrypted PDF handling.
- Type 3 fonts (their glyph procedures are themselves content streams).
- Vertical writing mode.

[Unreleased]: https://github.com/hallelx2/pdfgrab/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/hallelx2/pdfgrab/releases/tag/v0.5.0
[0.4.0]: https://github.com/hallelx2/pdfgrab/releases/tag/v0.4.0
[0.3.1]: https://github.com/hallelx2/pdfgrab/releases/tag/v0.3.1
[0.3.0]: https://github.com/hallelx2/pdfgrab/releases/tag/v0.3.0
[0.2.0]: https://github.com/hallelx2/pdfgrab/releases/tag/v0.2.0
[0.1.1]: https://github.com/hallelx2/pdfgrab/releases/tag/v0.1.1
[0.1.0]: https://github.com/hallelx2/pdfgrab/releases/tag/v0.1.0
[0.0.1]: https://github.com/hallelx2/pdfgrab/releases/tag/v0.0.1
