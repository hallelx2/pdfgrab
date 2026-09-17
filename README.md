# pdfgrab

A Go-native port of Python's [pdfplumber](https://github.com/jsvine/pdfplumber).

`pdfgrab` reads PDF documents, walks the content streams, and surfaces
the positioned primitives — characters, lines, rectangles, curves — that
higher-level layout algorithms (text extraction, word grouping, table
detection) operate on. It is built on top of
[pdfcpu](https://github.com/pdfcpu/pdfcpu) for low-level object parsing,
xref handling, and FlateDecode decompression; everything above that
(operator dispatch, text state, glyph positioning, ToUnicode CMaps,
font encodings) is implemented here.

The library targets the gap in the Go PDF ecosystem: existing libraries
either render PDFs to images, manipulate metadata, or extract bag-of-
words text. None of them give you what pdfplumber gives Python users —
a structured per-page object model you can run table-detection
heuristics on. This is that.

## Status

`v0.3.0` — full pdfplumber parity for table-finding strategies. All four
canonical strategies are implemented: `lines`, `lines_strict`, `text`,
and `explicit`. Mix and match per-axis (e.g. `vertical="text"` +
`horizontal="lines"`) works as expected. Also ships the `pdfgrab`
CLI for extracting text and tables without writing Go.

[![Go Reference](https://pkg.go.dev/badge/github.com/hallelx2/pdfgrab.svg)](https://pkg.go.dev/github.com/hallelx2/pdfgrab)
[![CI](https://github.com/hallelx2/pdfgrab/actions/workflows/test.yml/badge.svg)](https://github.com/hallelx2/pdfgrab/actions/workflows/test.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## Install

```sh
go get github.com/hallelx2/pdfgrab@v0.3.0
```

Requires Go 1.25+ (uses the standard-library `iter` package for the `Pages()` range-over-func iterator, and pdfcpu v0.12+).

## Quickstart

```go
package main

import (
    "fmt"
    "log"

    "github.com/hallelx2/pdfgrab"
)

func main() {
    doc, err := pdfgrab.OpenFile("report.pdf")
    if err != nil {
        log.Fatal(err)
    }
    defer doc.Close()

    for n, page := range doc.Pages() {
        // Primitives (v0.0.1).
        chars, _ := page.Chars()
        rects, _ := page.Rects()
        lines, _ := page.Lines()
        fmt.Printf("page %d: %d chars, %d rects, %d lines\n",
            n, len(chars), len(rects), len(lines))

        // Words and text extraction (v0.1.0).
        words, _ := page.Words(pdfgrab.DefaultWordOpts())
        text, _ := page.ExtractText(pdfgrab.DefaultTextOpts())
        fmt.Printf("  %d words; first line: %q\n",
            len(words), firstLine(text))
    }
}

func firstLine(s string) string {
    for i, r := range s {
        if r == '\n' {
            return s[:i]
        }
    }
    return s
}
```

## API surface

```go
// Constructors.
func Open(r io.Reader) (Document, error)
func OpenBytes(b []byte) (Document, error)
func OpenFile(path string) (Document, error)

// Document.
type Document interface {
    NumPages() int
    Page(n int) (Page, error)              // 1-indexed
    Pages() iter.Seq2[int, Page]           // Go 1.23+ range-over-func
    Close() error
}

// Page.
type Page interface {
    Number() int
    Width() float64
    Height() float64
    Chars() ([]Char, error)
    Lines() ([]Line, error)
    Rects() ([]Rect, error)
    Curves() ([]Curve, error)
    Objects() (Objects, error)

    // New in v0.1.0: word + text extraction.
    Words(opts WordOpts) ([]Word, error)
    ExtractText(opts TextOpts) (string, error)
    ExtractTextSimple(xTolerance, yTolerance float64) (string, error)

    // Table finding: lines + lines_strict (v0.2.0); text + explicit (v0.3.0).
    FindTables(settings TableSettings) ([]TableFinder, error)
    ExtractTables(settings TableSettings) ([]*Table, error)
}

// Primitives.
type Char struct {
    Text                  string
    X0, Y0, X1, Y1        float64
    FontName              string
    FontSize              float64
    Upright               bool
    Advance               float64
}

type Line struct { X0, Y0, X1, Y1 float64; Stroke bool; Width float64 }

type Rect struct { X0, Y0, X1, Y1 float64; Stroke, Fill bool; Width float64 }

type Curve struct { Points [][2]float64; Stroke, Fill bool; Width float64 }

type Objects struct { Chars []Char; Lines []Line; Rects []Rect; Curves []Curve }

// Word (new in v0.1.0).
type Word struct {
    Text                string
    X0, Y0, X1, Y1      float64
    Upright             bool
    Direction           string // "ltr" | "rtl" | "ttb" | "btt"
    FontName            string
    FontSize            float64
    Chars               []Char // populated when WordOpts.KeepChars=true
}

// WordOpts: configure Page.Words. Use DefaultWordOpts() for pdfplumber-matching defaults.
type WordOpts struct {
    XTolerance         float64 // default 3
    YTolerance         float64 // default 3
    KeepBlankChars     bool
    UseTextFlow        bool
    HorizontalLTR      bool   // default true
    VerticalTTB        bool   // default true
    ExtraAttrs         []string
    SplitAtPunctuation bool
    Expand             bool   // ligature expansion; default true
    KeepChars          bool
}

// TextOpts: configure Page.ExtractText. Use DefaultTextOpts() for defaults.
type TextOpts struct {
    XTolerance, YTolerance       float64
    Layout                       bool
    LayoutWidthChars             int
    LayoutHeightChars            int
    XDensity, YDensity           float64 // PDF points per character / per line
    UseTextFlow                  bool
    HorizontalLTR                bool
    VerticalTTB                  bool
    ExtraAttrs                   []string
    Expand                       bool
}

// Sentinel errors.
var (
    ErrInvalidPDF     = errors.New("pdfgrab: invalid PDF")
    ErrPageOutOfRange = errors.New("pdfgrab: page out of range")
    ErrUnsupported    = errors.New("pdfgrab: unsupported feature")
    ErrEncrypted      = errors.New("pdfgrab: encrypted PDF (decryption not yet supported)")
)
```

## Text extraction

```go
doc, _ := pdfgrab.OpenFile("report.pdf")
defer doc.Close()
page, _ := doc.Page(1)

// Words: each Word is a contiguous text run.
words, _ := page.Words(pdfgrab.DefaultWordOpts())
for _, w := range words {
    fmt.Printf("%-20s @ (%.1f, %.1f) %s %.1fpt\n",
        w.Text, w.X0, w.Y0, w.FontName, w.FontSize)
}

// ExtractText: all text on the page as one string. Dense (no layout)
// joins words with spaces and lines with "\n".
text, _ := page.ExtractText(pdfgrab.DefaultTextOpts())
fmt.Println(text)

// Layout-preserving extraction emulates `pdftotext -layout` / pdfplumber's
// extract_text(layout=True) — column-aligned output suitable for forms.
opts := pdfgrab.DefaultTextOpts()
opts.Layout = true
laid, _ := page.ExtractText(opts)
fmt.Println(laid)
```

## Tables

`Page.ExtractTables` is the table-detection entry point. It runs the
edges → intersections → cells → tables pipeline (a direct port of
pdfplumber's `TableFinder`) and returns one `*Table` per detected
table, with cell text already extracted.

```go
doc, _ := pdfgrab.OpenFile("invoice.pdf")
defer doc.Close()
page, _ := doc.Page(1)

settings := pdfgrab.DefaultTableSettings()
// settings.VerticalStrategy = pdfgrab.StrategyLinesStrict  // ignore rect outlines

tables, _ := page.ExtractTables(settings)
for ti, t := range tables {
    fmt.Printf("table %d: %d rows × %d cols at %+v\n",
        ti, len(t.Rows), len(t.Rows[0]), t.BBox)
    for _, row := range t.Rows {
        fmt.Println(row)
    }
}
```

`TableSettings` defaults match pdfplumber's
(`snap_tolerance=3`, `join_tolerance=3`, `edge_min_length=3`,
`intersection_tolerance=3`, `text_tolerance=3`, `min_words_vertical=3`,
`min_words_horizontal=1`). Override any field on the value returned
from `DefaultTableSettings()` to tighten or loosen the heuristics.

The four implemented strategies (one per axis, chosen independently):

- `StrategyLines` — edges come from drawn `Line` segments, `Rect`
  outlines (all four sides), and axis-aligned `Curve` segments.
  Default. Best for typical PDFs whose tables have rule lines.
- `StrategyLinesStrict` — only drawn `Line` segments are used. Use
  this when your PDF draws cell BACKGROUNDS as filled rectangles
  that you do NOT want treated as row boundaries.
- `StrategyText` — edges inferred from word alignment. Vertical
  edges come from clusters of words sharing X0 / X1 / centre;
  horizontal edges from clusters sharing top-Y. Tunable via
  `MinWordsVertical` (default 3) and `MinWordsHorizontal` (default 1).
- `StrategyExplicit` — caller-supplied edges via
  `ExplicitVerticalLines` / `ExplicitHorizontalLines`. Required when
  table boundaries are known from layout analysis or manual
  annotation.

### Side-by-side: pdfplumber → pdfgrab (lines strategy)

```python
# Python (pdfplumber)
import pdfplumber

with pdfplumber.open("invoice.pdf") as pdf:
    page = pdf.pages[0]
    for table in page.find_tables({"vertical_strategy": "lines",
                                    "horizontal_strategy": "lines"}):
        for row in table.extract():
            print(row)
```

```go
// Go (pdfgrab)
import "github.com/hallelx2/pdfgrab"

doc, _ := pdfgrab.OpenFile("invoice.pdf")
defer doc.Close()
page, _ := doc.Page(1)

settings := pdfgrab.DefaultTableSettings()
settings.VerticalStrategy = pdfgrab.StrategyLines
settings.HorizontalStrategy = pdfgrab.StrategyLines

tables, _ := page.ExtractTables(settings)
for _, t := range tables {
    for _, row := range t.Rows {
        fmt.Println(row)
    }
}
```

### Side-by-side: pdfplumber → pdfgrab (text strategy)

```python
# Python (pdfplumber) — borderless tables
import pdfplumber

with pdfplumber.open("10k-filing.pdf") as pdf:
    page = pdf.pages[3]
    for table in page.find_tables({"vertical_strategy": "text",
                                    "horizontal_strategy": "text",
                                    "min_words_vertical": 3}):
        for row in table.extract():
            print(row)
```

```go
// Go (pdfgrab)
doc, _ := pdfgrab.OpenFile("10k-filing.pdf")
defer doc.Close()
page, _ := doc.Page(4)

settings := pdfgrab.DefaultTableSettings()
settings.VerticalStrategy = pdfgrab.StrategyText
settings.HorizontalStrategy = pdfgrab.StrategyText
settings.MinWordsVertical = 3

tables, _ := page.ExtractTables(settings)
for _, t := range tables {
    for _, row := range t.Rows {
        fmt.Println(row)
    }
}
```

### Side-by-side: pdfplumber → pdfgrab (explicit strategy)

```python
# Python (pdfplumber) — caller-supplied edges
import pdfplumber

with pdfplumber.open("statement.pdf") as pdf:
    page = pdf.pages[0]
    table = page.find_tables({
        "vertical_strategy": "explicit",
        "horizontal_strategy": "explicit",
        "explicit_vertical_lines":   [100, 200, 300, 400],
        "explicit_horizontal_lines": [600, 650, 700, 720],
    })[0]
    for row in table.extract():
        print(row)
```

```go
// Go (pdfgrab)
doc, _ := pdfgrab.OpenFile("statement.pdf")
defer doc.Close()
page, _ := doc.Page(1)

settings := pdfgrab.DefaultTableSettings()
settings.VerticalStrategy = pdfgrab.StrategyExplicit
settings.HorizontalStrategy = pdfgrab.StrategyExplicit
settings.ExplicitVerticalLines   = []float64{100, 200, 300, 400}
settings.ExplicitHorizontalLines = []float64{600, 650, 700, 720}

tables, _ := page.ExtractTables(settings)
for _, row := range tables[0].Rows {
    fmt.Println(row)
}
```

### Mixed strategies

Each axis picks its strategy independently. Combinations like
`vertical=text` + `horizontal=lines` (common for tables with drawn
row separators but borderless columns) work out of the box:

```go
settings := pdfgrab.DefaultTableSettings()
settings.VerticalStrategy   = pdfgrab.StrategyText
settings.HorizontalStrategy = pdfgrab.StrategyLines
tables, _ := page.ExtractTables(settings)
```

The two outputs match cell-for-cell on the parity fixtures (see
`testdata/golden/*.tables-text.expected.json` and
`*.tables.expected.json` for the regression goldens). Field naming
differs in the obvious places: pdfgrab returns a slice of `*Table`
instead of `Table` objects you have to call `.extract()` on; rows are
`[]string` instead of `list[Optional[str]]` (missing cells produce
`""` rather than `nil`); and table bboxes use `(X0, Y0, X1, Y1)` PDF
user space rather than pdfplumber's image-space
`(x0, top, x1, bottom)`.

## CLI

`pdfgrab` ships a command-line interface that mirrors pdfplumber's
CLI surface for the operations the library implements:

```sh
go install github.com/hallelx2/pdfgrab/cmd/pdfgrab@v0.3.0
```

Usage:

```sh
# Extract every table on every page as JSON.
pdfgrab extract invoice.pdf --tables --format json

# Borderless tables: use the text strategy.
pdfgrab extract 10k.pdf --tables \
    --vertical-strategy text --horizontal-strategy text \
    --min-words-vertical 4

# Extract text only (no table detection).
pdfgrab extract report.pdf --text --format text

# Subset of pages, pretty-printed JSON.
pdfgrab extract report.pdf --tables --pages 1,3-5 --indent 2

# Caller-supplied edges.
pdfgrab extract statement.pdf --tables \
    --vertical-strategy explicit --horizontal-strategy explicit \
    --explicit-vertical-lines 100,200,300,400 \
    --explicit-horizontal-lines 600,650,700,720
```

Flags:

| Flag | Default | Description |
| --- | --- | --- |
| `--pages` | all | Pages: `1,3-5` syntax. |
| `--tables` | off | Output detected tables. |
| `--text` | off | Output extracted text. |
| `--format` | `json` | `json` \| `text`. |
| `--vertical-strategy` | `lines` | `lines` \| `lines_strict` \| `text` \| `explicit`. |
| `--horizontal-strategy` | `lines` | same set. |
| `--snap-tolerance` | 3 | snap_tolerance (PDF pts). |
| `--join-tolerance` | 3 | join_tolerance (PDF pts). |
| `--edge-min-length` | 3 | drop merged edges shorter than this. |
| `--intersection-tolerance` | 3 | slack on edge crossings. |
| `--text-tolerance` | 3 | per-cell text-extraction tolerance. |
| `--min-words-vertical` | 3 | text strategy column threshold. |
| `--min-words-horizontal` | 1 | text strategy row threshold. |
| `--explicit-vertical-lines` | (none) | comma list of X coords. |
| `--explicit-horizontal-lines` | (none) | comma list of Y coords. |
| `--indent` | 0 | JSON indent (0 = compact). |

## Side-by-side comparison with pdfplumber

```python
# Python (pdfplumber)
import pdfplumber

with pdfplumber.open("report.pdf") as pdf:
    page = pdf.pages[0]
    for word in page.extract_words(x_tolerance=3, y_tolerance=3):
        print(word["text"], word["x0"], word["top"])
    print(page.extract_text())
```

```go
// Go (pdfgrab)
import "github.com/hallelx2/pdfgrab"

doc, _ := pdfgrab.OpenFile("report.pdf")
defer doc.Close()
page, _ := doc.Page(1)

words, _ := page.Words(pdfgrab.DefaultWordOpts())
for _, w := range words {
    // pdfgrab's Y is PDF user-space (origin bottom-left). The
    // pdfplumber-equivalent "top" is page.Height() - w.Y1.
    fmt.Println(w.Text, w.X0, page.Height()-w.Y1)
}
fmt.Println(must(page.ExtractText(pdfgrab.DefaultTextOpts())))
```

Three differences worth noting:

1. **Page indexing is 1-based**, matching the PDF spec and pdfplumber's
   `pdf.pages[0]` is actually the first page (Python is 0-indexed,
   pdfplumber compensates). Our `Page(1)` is the same first page.
2. **Coordinates are in PDF user space with origin at bottom-left**.
   pdfplumber by default reports `top` (origin top-left, Y growing down)
   on its chars and words; we report `Y0` / `Y1` in PDF native
   coordinates. The conversion is `top = page.Height() - Y1`.
3. **Options are explicit Go structs, not `**kwargs`**. Build a
   `WordOpts` / `TextOpts`, override the fields you care about, pass
   it through. `DefaultWordOpts()` / `DefaultTextOpts()` return
   pdfplumber-matching defaults.

## Parity with pdfplumber

The word-grouping and text-extraction algorithms are direct ports of
pdfplumber's `WordExtractor` and `extract_text` (see
[`pdfplumber/utils/text.py`](https://github.com/jsvine/pdfplumber/blob/main/pdfplumber/utils/text.py)).
Tests in [`golden_test.go`](golden_test.go) compare the Go output
against pdfplumber's reference output on shared fixture PDFs.

Behaviours that match exactly:

- Word grouping: same line-cluster-then-merge-by-gap algorithm, same
  defaults (XTolerance=3, YTolerance=3), same handling of blank-char
  filtering, ligature expansion (ﬁ→fi, etc.), and split-at-punctuation.
- Ordering: words returned in pdfplumber's order (top-to-bottom, then
  left-to-right within each line) when UseTextFlow is false.
- Direction handling: ltr / rtl / ttb / btt mapping from
  upright + HorizontalLTR + VerticalTTB.

Behaviours that intentionally differ:

- **Position precision now matches on the standard 14 fonts; residual
  drift is limited to embedded fonts that ship no metrics**.
  pdfplumber uses pdfminer.six's AFM tables for the standard 14 fonts.
  pdfgrab now bundles the same Adobe Core 14 metrics, so a font that
  omits `/Widths` — which is spec-legal for those 14, and was previously
  the source of up to ~10pt of word-bbox drift — resolves to real
  per-glyph widths instead of a flat 500 guess. Common substitute names
  (`Arial`, `Times New Roman`, `Courier New`) alias to their metric
  equivalents; Narrow and Condensed variants deliberately do not, since
  regular-width metrics would be confidently wrong there. Word text and
  order match exactly. What remains is fonts that supply neither
  `/Widths` nor a recognised standard-14 `/BaseFont` — an embedded
  subset with no metrics — which still fall back to the flat guess.
  Measured against the golden fixtures, horizontal drift went from
  **11.99pt max / 4.79pt mean to exactly 0.0000pt**, and the golden test
  now asserts X within 0.01pt.
  Vertical positions match too: the AFM `Ascent`/`Descent` are bundled
  for the same reason and by the same rule, since the standard-14
  exemption that omits `/Widths` also omits `/FontDescriptor`. Glyph
  boxes previously collapsed to `[baseline, baseline+size]` and sat
  `descent*size` too high — 2.484pt at 12pt, 4.968pt at 24pt, both
  exactly `0.207*size` for Helvetica. That mattered beyond cosmetics:
  `lines_strict` and `text` infer row boundaries from word Y extents.
  **Both axes now measure 0.0000pt drift, and the golden test asserts
  0.01pt on each.**
- **`Layout=true` output is structurally similar but not byte-equal**.
  Pdfplumber's layout algorithm has version-to-version drift; we
  produce a column-aligned grid with the same density defaults but
  don't promise byte-equal output across pdfplumber releases.

Behaviours not yet ported:

- `extract_text_lines` (regex-based line extraction).
- `search` on TextMap (regex over assembled page text with char-level
  match back-references).
- Per-character extra_attrs hooks beyond `fontname` and `size`.

## Architecture

```
pdfgrab/
├── pdfgrab.go        // Open / OpenBytes / OpenFile entry points
├── pdf.go             // Document interface + implementation
├── page.go            // Page interface + implementation
├── char.go            // Public Char / Line / Rect / Curve / Objects
├── text.go            // Word + ExtractText + ExtractTextSimple (v0.1.0)
├── table.go           // TableStrategy / TableSettings / Table types (v0.2.0)
├── finder.go          // Cells-from-edges algorithm (v0.2.0)
├── finder_text.go     // Text + explicit edge derivation (v0.3.0)
├── clustering.go      // 1-D clusterObjects, groupObjectsByAttr, dedupeChars
├── geometry.go        // BBox helpers: Union, Intersect, Contains, Snap
├── errors.go          // Sentinel errors
├── cmd/
│   └── pdfgrab/      // Command-line interface (v0.3.0)
│       └── main.go
└── internal/
    ├── layout/
    │   └── lines.go   // Edge type + snap/join/filter pipeline (v0.2.0)
    └── pdf/
        ├── reader.go      // pdfcpu bridge
        ├── content.go     // Content-stream interpreter
        ├── ops.go         // Operator dispatch table
        ├── state.go       // Graphics + text state, matrix math
        ├── font.go        // Font + encoding tables + glyph-name resolution
        └── cmap.go        // ToUnicode CMap parser
```

The public `pdfgrab` package is small and stable. The `internal/pdf`
package owns the interpreter — its types are not exposed because they
will evolve as more PDF features are added (Type 3 fonts, vertical
writing, more exotic CMaps).

## Why pdfcpu and not write a PDF parser from scratch?

PDF object parsing — xref tables, indirect-object resolution, stream
decompression (FlateDecode, LZWDecode, ASCII85Decode), encryption — is
a large amount of mostly-uninteresting code. pdfcpu is mature, well-
tested, and gives us a parsed `*model.Context` to work with. We layer
the content-stream interpreter (which pdfcpu doesn't have) on top.

If pdfcpu's dependency footprint becomes a problem (it pulls in image
codecs we don't strictly need), the blast radius of swapping it out is
limited to `internal/pdf/reader.go`. The rest of the package is
stdlib-only.

## Roadmap

- `v0.0.x` — content-stream primitives.
- `v0.1.x` — text extraction: `Page.ExtractText`, `Page.Words`,
  `Page.ExtractTextSimple`.
- `v0.2.x` — table finding via ruling lines: `Page.FindTables` /
  `Page.ExtractTables` covering the `lines` and `lines_strict`
  strategies.
- `v0.3.x` — remaining table strategies and CLI (this release):
  `text` (word-alignment edges), `explicit` (caller-supplied edges),
  and a `pdfgrab` CLI mirroring pdfplumber's surface.
- `v0.4.x` — bundle the standard-14 AFM metrics so word bboxes (and
  therefore cell text) match pdfplumber on standard fonts. **Done**: the
  Adobe Core 14 metrics ship, Symbol/ZapfDingbats decode with their own
  built-in encodings, and measured position drift against pdfplumber is
  **0.0000pt on both axes** — the golden envelope is now asserted at
  0.01pt. See
  [the evaluation](docs/evaluations/2026-08-02-font-metrics-and-table-fidelity.md).
- `v0.5.x` — **table detection**. The ICDAR 2013 benchmark puts
  end-to-end F1 at 0.362, level with pdfplumber's 0.370, and the
  diagnostic is unambiguous: precision 0.865, recall 0.229. What we
  extract is right; we miss three quarters of the tables, because the
  `lines` strategy needs *intersecting* rulings and a horizontally-ruled
  table produces none. See
  [the evaluation](docs/evaluations/2026-08-02-icdar2013-table-structure.md).
- `v0.6.x` — performance pass: parser speed against pdfminer.six and
  pdfplumber on a representative corpus.

## Repository layout

`pdfgrab` is a single Go package, so its source lives in the repository
root — that is the import path, and Go keeps `_test.go` files next to the
code they cover.

| | |
| --- | --- |
| `*.go` | the library |
| `internal/pdf/` | content-stream interpreter, fonts, encodings |
| `internal/layout/` | edge/intersection/cell geometry |
| `cmd/pdfgrab/` | CLI |
| `bench/` | accuracy benchmarks against public datasets — a **separate Go module**, so it never adds a dependency here |
| `docs/evaluations/` | dated measurement reports, with their caveats |
| `scripts/` | fixture and golden-file generators |
| `testdata/golden/` | fixtures compared against pdfplumber |
| `testdata/fonts/` | fixtures where pdfplumber is wrong, asserted directly |

## License

MIT. See [LICENSE](LICENSE).

The bundled font-metric and glyph tables in `internal/pdf/` are generated from
Adobe's Glyph List and Core 14 AFM data (BSD-3-Clause) and from pdf.js's
encoding vectors (Apache-2.0). Attribution and the upstream license texts are
in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Acknowledgements

This library is a direct port of the algorithms in
[pdfminer.six](https://github.com/pdfminer/pdfminer.six) and
[pdfplumber](https://github.com/jsvine/pdfplumber). Their authors did
the hard work of figuring out how to robustly recover structure from
the PDF wire format; this is that work translated into Go.
