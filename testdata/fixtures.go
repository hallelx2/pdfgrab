// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

// Package testdata builds the test PDFs used by pdfgrab's tests.
//
// We hand-craft the PDFs as byte slices rather than checking in
// binaries because:
//
//  1. It makes the test fixtures readable in the repo — every glyph,
//     position, and operator is right there in the source.
//  2. It avoids storing PDF binaries in git history (small win on
//     repo size, larger win on diff legibility).
//  3. Tests can mutate a fixture to probe edge cases without checking
//     in a separate file for each variation.
//
// The PDFs are intentionally minimal: one page each, single font
// (Helvetica from the standard 14), no compression, no encryption.
// We rely on the parser to dereference indirect objects properly so
// the structure is realistic, but we don't exercise pdfcpu's stream
// decompression here (it's exercised separately by real-world PDFs
// the engine ingests).
package testdata

import (
	"fmt"
	"strings"
)

// Hello returns a minimal PDF with one page containing the text
// "Hello, world!" at position (72, 720) with Helvetica 12pt.
//
// Construction follows the PDF 1.7 grammar precisely:
//
//   - The %PDF-1.4 header signals the version.
//   - Objects 1-5 form the standard root → pages → page → font chain.
//   - Object 6 is the page's content stream: a `BT ... ET` text
//     object with Helvetica selected via `/F1 12 Tf` and the string
//     drawn via `Tj`.
//   - The xref table is constructed by recording each object's byte
//     offset as we build the body, so the final %%EOF section is
//     byte-accurate.
//
// The function is deterministic — same input → same bytes — which
// matters because the test assertions use absolute byte offsets in
// some places.
func Hello() []byte {
	return BuildSinglePage(`BT
/F1 12 Tf
72 720 Td
(Hello, world!) Tj
ET
`)
}

// TableRuled returns a minimal PDF whose content stream draws a
// 2-column × 3-row ruled table containing predictable text. The
// table is positioned in user space so the cells are easy to reason
// about: each cell is 100×30 PDF points, the top-left cell sits at
// (100, 700), and the grid extends down to (300, 610).
//
// The grid is drawn by four horizontal rules (at Y = 610, 640, 670,
// 700) and three vertical rules (at X = 100, 200, 300). Cell content
// is placed near the top-left of each cell using `Td` offsets:
//
//	row 0:  Name       Age      <- header
//	row 1:  Alice      30
//	row 2:  Bob        25
//
// Coordinates use PDF user space (Y growing UP), so row 0 is the
// VISUALLY TOP row. The fixture is intentionally simple — no
// kerning, no rotated text, no shaded backgrounds — so the
// expected output is uncontroversial. We use Helvetica from the
// standard 14 so no font program needs to be embedded.
func TableRuled() []byte {
	// Lay out the seven ruling lines (4 horizontal, 3 vertical).
	// Each line uses a separate moveto/lineto/stroke triple. We
	// could collapse them into one painting operation but separate
	// `S` calls produce cleaner per-line objects in the parser's
	// output, which keeps the test's "we have N lines" assertions
	// straightforward.
	//
	// Horizontal rules:  Y = 610 / 640 / 670 / 700, X from 100 to 300.
	// Vertical rules:    X = 100 / 200 / 300, Y from 610 to 700.
	const grid = `1 w
% Horizontal rules.
100 610 m
300 610 l
S
100 640 m
300 640 l
S
100 670 m
300 670 l
S
100 700 m
300 700 l
S
% Vertical rules.
100 610 m
100 700 l
S
200 610 m
200 700 l
S
300 610 m
300 700 l
S
`
	// Text in each cell. Y values target a few points below the
	// top of the cell so the glyph baseline sits within the cell
	// bbox even after Helvetica's descender drops below the
	// baseline. Cell top is at Y = top of cell; we use Td to move
	// to (x_offset, top - 22) where 22 leaves room for the cap
	// height of 10pt Helvetica.
	const text = `BT
/F1 10 Tf
% Row 0 (header): Y top = 700, baseline ≈ 678.
110 678 Td
(Name) Tj
100 0 Td
(Age) Tj
% Row 1: Y top = 670, baseline ≈ 648. Move back to col 0 then down.
-100 -30 Td
(Alice) Tj
100 0 Td
(30) Tj
% Row 2: Y top = 640, baseline ≈ 618.
-100 -30 Td
(Bob) Tj
100 0 Td
(25) Tj
ET
`
	return BuildSinglePage(grid + text)
}

// TableBorderless returns a minimal PDF with a 3-column borderless
// table: the columns are conveyed by whitespace alignment alone, with
// no ruling lines drawn. The header row is at Y ~ 730 and three body
// rows are at Y ~ 710, 695, 680. Columns are at X ~ 100, 200, 300.
//
// This fixture targets the "text" strategy — it's the smallest
// possible reproducer of the borderless-table case that's common in
// 10-K filings, bank statements, scanned-then-OCR'd PDFs, and any
// other PDF whose tables aren't ruled.
//
// Content:
//
//	Item    Quantity  Price
//	Apple   3         1.50
//	Banana  6         0.75
//	Cherry  12        0.10
//
// The X positions are chosen so each header word and each body word
// in a column starts at the same X within the wordEdgeTolerance
// (=1 pt) — pdfplumber's words_to_edges_v clusters on exactly that
// tolerance.
func TableBorderless() []byte {
	// 10pt Helvetica baselines. We move the text cursor with Td so
	// the relative offsets keep the per-row, per-column positions
	// pinned to the same X coordinates within each row.
	const text = `BT
/F1 10 Tf
% Header row: baseline ~ 720.
100 720 Td
(Item) Tj
100 0 Td
(Quantity) Tj
100 0 Td
(Price) Tj
% Body row 1: y -= 20, x back to 0.
-200 -20 Td
(Apple) Tj
100 0 Td
(3) Tj
100 0 Td
(1.50) Tj
% Body row 2.
-200 -15 Td
(Banana) Tj
100 0 Td
(6) Tj
100 0 Td
(0.75) Tj
% Body row 3.
-200 -15 Td
(Cherry) Tj
100 0 Td
(12) Tj
100 0 Td
(0.10) Tj
ET
`
	return BuildSinglePage(text)
}

// Rules returns a minimal PDF whose content stream draws four lines
// (two horizontal, two vertical) and one rectangle. We use simple
// coordinates: a 100x100 box with one stroked diagonal line and one
// stroked horizontal line outside the box.
func Rules() []byte {
	// PDF content stream operators:
	//   re   = rectangle (x y w h)
	//   m    = moveto (x y)
	//   l    = lineto (x y)
	//   S    = stroke
	//   1 w  = set line width to 1pt
	return BuildSinglePage(`1 w
% A stroked rectangle, 100x100 at (100, 600).
100 600 100 100 re
S
% A horizontal line below, 50 pt long.
100 580 m
150 580 l
S
% A vertical line below, 50 pt long.
100 500 m
100 550 l
S
`)
}

// BuildSinglePage assembles a single-page PDF whose content stream is
// the provided literal text. The page is US-letter (612 x 792 pt),
// uses Helvetica from the standard 14 (so we don't need to embed a
// font program), and has no compression on the content stream.
//
// The xref table is generated correctly: we accumulate each object's
// byte offset as we write the body, then emit the xref subsection at
// the end with one entry per object. The trailer points to /Root
// (object 1) and /Size = number_of_objects + 1 (entry 0 is the
// "head of free list" sentinel).
//
// Output bytes are stable across runs.
func BuildSinglePage(content string) []byte {
	const header = "%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"

	// Object bodies. Numbered 1..6.
	objects := []string{
		// 1: Catalog.
		`<< /Type /Catalog /Pages 2 0 R >>`,
		// 2: Pages tree root.
		`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`,
		// 3: Page.
		`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> /ProcSet [/PDF /Text] >> /Contents 5 0 R >>`,
		// 4: Font (Helvetica from the standard 14; no descriptor needed).
		`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>`,
		// 5: Content stream. Length is computed below.
		"", // placeholder — filled in after we know content length.
	}

	// Build the content-stream object with its /Length set correctly.
	streamBody := []byte(content)
	objects[4] = fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(streamBody), streamBody)

	// Assemble the body, recording byte offsets for xref.
	var b strings.Builder
	b.WriteString(header)

	offsets := make([]int, len(objects))
	for i, body := range objects {
		offsets[i] = b.Len()
		b.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, body))
	}

	// xref table.
	xrefPos := b.Len()
	b.WriteString("xref\n")
	b.WriteString(fmt.Sprintf("0 %d\n", len(objects)+1))
	// Object 0 is the head of the free list.
	b.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		b.WriteString(fmt.Sprintf("%010d 00000 n \n", off))
	}

	// Trailer.
	b.WriteString("trailer\n")
	b.WriteString(fmt.Sprintf("<< /Size %d /Root 1 0 R >>\n", len(objects)+1))
	b.WriteString("startxref\n")
	b.WriteString(fmt.Sprintf("%d\n", xrefPos))
	b.WriteString("%%EOF\n")

	return []byte(b.String())
}
