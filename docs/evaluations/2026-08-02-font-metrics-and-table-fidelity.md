# Font metrics and table fidelity — audit and fixes

**Date:** 2026-08-02
**Commits:** `325e628` … `0ca65ca` (PRs #9, #13–#19)
**References:** rendered pages via `pdftoppm`, poppler `pdftotext -layout`, pdfplumber 0.11.9
**Real-world corpus:** 3M Company 2018 Form 10-K (FinanceBench), pages 56–60

## Headline

| | before | after |
|---|---|---|
| Horizontal (X) drift vs pdfplumber | 11.99pt max / 4.79pt mean | **0.0000pt** |
| Vertical (Y) drift vs pdfplumber | 4.968pt @24pt, 2.484pt @12pt | **0.0000pt** |
| Golden position tolerance | 15pt | **0.01pt, both axes** |
| Negative signs preserved (3M, 5 statements) | 83/103 (81%) | **103/103** |
| Font coverage in the test corpus | 1 font, 26 words | **12 fonts, 3 sizes, 170 words** |

## Defects found and fixed

### 1. Flat 500 width for the standard 14 fonts

PDF 1.7 §9.6.2.2 lets the 14 standard fonts omit `/Widths`; a consumer is
expected to already know their metrics. pdfgrab did not, and fell through
to a flat 500/1000 guess for every glyph — `i` (222) and `m` (833) got
identical widths, and the error accumulated along each line.

Because the `text` and `lines_strict` strategies infer column boundaries
from word positions, this was upstream of table structure, not merely
cosmetic.

### 2. Symbol and ZapfDingbats decoded as Latin

Both fonts carry their own built-in encoding and neither declares
`/Encoding`, so `readFont` handed them StandardEncoding. Symbol code
`0x61` decoded as `a` rather than `alpha` — a *text*-correctness failure,
not a metrics one. Any document using Symbol for Greek extracted nonsense.

pdfplumber 0.11.9 still exhibits this; pdfgrab now does not.

### 3. Glyph boxes sat `descent × size` too high

Two independent causes, the second of which would have survived fixing the
first:

- The same exemption that omits `/Widths` also omits `/FontDescriptor`,
  so `Ascent`/`Descent` were 0 and the glyph box collapsed to
  `[baseline, baseline+size]`.
- Descent was scaled by `0.001` but **not** by the font size, so even
  fonts that *did* supply a descriptor were short by a factor of the font
  size — 12× at 12pt.

Measured error was exactly `0.207 × size`, matching Helvetica's −207/1000
descender. Rows are derived from word Y extents, so this could merge or
split table rows.

### 4. 19% of negative numbers lost their sign

Cell assignment gives a glyph to whichever cell contains its centre. That
is correct at an *interior* boundary — it settles which of two candidates
owns a straddling glyph. At the table's *outer* edge there is no competing
cell, so the rule stopped disambiguating and started deleting.

On 3M page 58 the `)` of `(16,048)` had its centre at x=537.879 while the
last column ended at x=537.871 — **a shortfall of 0.008pt**, about a
nine-thousandth of an inch. Accounting notation for −16,048 was read back
as +16,048.

Across the five financial statements this flipped the sign of **20 of 103
negatives (19%)** while leaving every magnitude correct — which is what
made it dangerous. A missing value is detectable downstream; a plausible
wrong sign is not.

**pdfplumber has the same defect.** On the same row it returns `16,048`
without the closing paren. pdfgrab is now strictly better than its
reference here.

### 5. Small type over-merged into single runs

`DefaultWordOpts()` did not honour explicit space glyphs, so word
boundaries were inferred from inter-glyph gaps alone. At 8pt a space is
`278/1000 × 8 = 2.22pt`, under the 3pt `XTolerance`, so a whole line
collapsed into one word. Body type in real documents is routinely 8–9pt.

pdfplumber ends a word *at* a whitespace glyph before any gap test runs.
Matching that reproduces its output word-for-word and coordinate-for-coordinate.

## A finding that was wrong

`ExtractText` returns `"Consolidated Balance Shee t"` for 3M page 58. This
was filed as a defect. **It is not** — that gap is in the source document.
The rendered page shows it, and poppler extracts it identically. 3M's
filing agent typeset it that way, which is common in SEC EDGAR documents.

The error was methodological: the original comparison was pdfgrab's table
output against pdfgrab's *own* text output. Internal agreement proves the
two code paths disagree, not which one is right. Correcting it required an
external reference — a rendered page and a second extractor.

**Any fidelity claim needs an oracle the project did not write.**

## Verification method

Findings were confirmed against three independent sources before being
accepted:

1. the page rendered to PNG (`pdftoppm -r 150`) and read directly,
2. poppler `pdftotext -layout`,
3. pdfplumber 0.11.9.

The citation geometry (`BBox.Viewport` / `BBox.Normalized`) was verified by
projecting real cell bboxes onto the rendered page and confirming the
highlights land on the intended rows.

## What remains untested

- **scanned PDFs** — no text layer at all; the geometric approach cannot
  apply and an OCR path is required
- **CID/Type0 fonts** — CJK and modern subsetted fonts
- **embedded subset fonts** with their own `/Widths` — the common
  production case
- **rotated and multi-column layouts**

And separately, table *structure* accuracy, measured in the
[ICDAR 2013 evaluation](2026-08-02-icdar2013-table-structure.md).
