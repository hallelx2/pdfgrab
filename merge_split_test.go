// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"reflect"
	"testing"
)

// band builds a row of glyphs at a fixed height, each `w` wide, starting
// at x with the supplied per-glyph gaps.
func glyph(text string, x0, x1 float64) Char {
	return Char{Text: text, X0: x0, X1: x1, Y0: 560, Y1: 568}
}

// TestBoundarySplitsToken separates the two cases that look identical in
// the output but are completely different on the page: a boundary that
// severed one value, and a genuine column gutter.
func TestBoundarySplitsToken(t *testing.T) {
	// Real coordinates from 3M's 2018 10-K balance sheet. The "(" ends
	// at 436.9 and "1" begins at 436.92 — 0.02pt apart, plainly one
	// token that a column edge happened to cut.
	split := []Char{
		glyph("(", 434.70, 436.90),
		glyph("1", 436.92, 440.50),
	}
	left := BBox{X0: 391.5, X1: 438.3, Y0: 559, Y1: 569}
	right := BBox{X0: 438.3, X1: 470.7, Y0: 559, Y1: 569}
	if !boundarySplitsToken(split, left, right, 3) {
		t.Error("adjacent glyphs 0.02pt apart should read as one token split by the boundary")
	}

	// A real gutter: "$" then a wide gap then the number. On the page
	// these genuinely occupy separate columns, and merging them would be
	// wrong — the currency symbol is its own cell.
	gutter := []Char{
		glyph("$", 392.0, 397.0),
		glyph("3", 450.0, 454.0),
	}
	if boundarySplitsToken(gutter, left, right, 3) {
		t.Error("a 53pt gutter must not be treated as a split token")
	}

	// Nothing on one side: no boundary to split.
	if boundarySplitsToken([]Char{glyph("(", 434.7, 436.9)}, left, right, 3) {
		t.Error("a boundary with no glyph on the right cannot split a token")
	}

	// Non-adjacent cells share no boundary at all.
	far := BBox{X0: 600, X1: 650, Y0: 559, Y1: 569}
	if boundarySplitsToken(split, left, far, 3) {
		t.Error("cells that are not neighbours have no shared boundary")
	}
}

// TestMergeSplitTokensRejoinsValues checks the row-level pass, including
// that a run of more than two fragments collapses in a single sweep and
// that cell bboxes are merged too — the bbox is what a citation
// highlight is drawn from, so a half-value box under-covers on screen.
func TestMergeSplitTokensRejoinsValues(t *testing.T) {
	chars := []Char{
		glyph("D", 100, 110), glyph("e", 110, 118), // "De" | "c" split three ways
		glyph("c", 118, 126),
	}
	cells := [][]BBox{{
		{X0: 90, X1: 118, Y0: 559, Y1: 569},
		{X0: 118, X1: 130, Y0: 559, Y1: 569},
	}}
	rows := [][]string{{"De", "c"}}

	gotRows, gotCells := mergeSplitTokens(rows, cells, chars, 3)
	want := [][]string{{"Dec"}}
	if !reflect.DeepEqual(gotRows, want) {
		t.Errorf("rows = %q, want %q", gotRows, want)
	}
	if len(gotCells[0]) != 1 {
		t.Fatalf("cells = %d, want 1 merged cell", len(gotCells[0]))
	}
	// The merged bbox must span both originals, or a highlight drawn
	// from it covers only part of the value.
	if gotCells[0][0].X0 != 90 || gotCells[0][0].X1 != 130 {
		t.Errorf("merged bbox = %+v, want X0=90 X1=130", gotCells[0][0])
	}
}

// TestMergeSplitTokensLeavesRealColumnsAlone is the safety property. The
// feature is only useful if it does not collapse genuinely distinct
// columns — a financial statement's "$" column really is a column.
func TestMergeSplitTokensLeavesRealColumnsAlone(t *testing.T) {
	chars := []Char{
		glyph("$", 92, 98),
		glyph("3", 160, 166), glyph("6", 166, 172),
	}
	cells := [][]BBox{{
		{X0: 90, X1: 120, Y0: 559, Y1: 569},
		{X0: 120, X1: 180, Y0: 559, Y1: 569},
	}}
	rows := [][]string{{"$", "36"}}

	gotRows, gotCells := mergeSplitTokens(rows, cells, chars, 3)
	if !reflect.DeepEqual(gotRows, [][]string{{"$", "36"}}) {
		t.Errorf("rows = %q, want the two columns kept separate", gotRows)
	}
	if len(gotCells[0]) != 2 {
		t.Errorf("cells = %d, want 2 (no merge)", len(gotCells[0]))
	}

	// An empty neighbour is never merged into — that would shift every
	// column left and destroy the grid.
	rows2 := [][]string{{"36", ""}}
	got2, _ := mergeSplitTokens(rows2, cells, chars, 3)
	if !reflect.DeepEqual(got2, [][]string{{"36", ""}}) {
		t.Errorf("rows = %q, want the empty cell preserved", got2)
	}
}

// TestBoundarySplitsTokenRespectsExplicitSpaces guards against welding
// real words together.
//
// The space glyph is itself a Char, sitting flush against its neighbours.
// Measuring gaps without excluding it makes every space look like an
// intra-word join — on a real 10-K that turned "(Dollars in millions,"
// plus "except per share amount)" into "millions,except". A whitespace
// glyph across the boundary is the PDF stating outright that a word ends
// there, and it outranks any geometry.
func TestBoundarySplitsTokenRespectsExplicitSpaces(t *testing.T) {
	left := BBox{X0: 90, X1: 118, Y0: 559, Y1: 569}
	right := BBox{X0: 118, X1: 180, Y0: 559, Y1: 569}

	// No space: adjacent glyphs really are one token.
	noSpace := []Char{
		glyph(",", 110, 116),
		glyph("e", 116.02, 124),
	}
	if !boundarySplitsToken(noSpace, left, right, 3) {
		t.Error("adjacent glyphs with no space should read as a split token")
	}

	// Same geometry, but with the space glyph the producer actually
	// emitted. Now it is two words and must not be merged.
	withSpace := []Char{
		glyph(",", 110, 116),
		glyph(" ", 116, 118.5),
		glyph("e", 118.52, 126),
	}
	if boundarySplitsToken(withSpace, left, right, 3) {
		t.Error("an explicit space across the boundary means two words — must not merge")
	}
}

// TestMergeSplitTokensKeepsTableRectangular is the property that makes
// the feature safe to use on a real table.
//
// A column boundary belongs to the table, not to one row. If the merge
// decision were made per-row, a header band that happens to contain the
// split would collapse while the data rows below it did not — leaving
// rows with different column counts, so the header's second date would
// sit above the first column of figures. A sheared grid is worse than
// the split it set out to fix.
func TestMergeSplitTokensKeepsTableRectangular(t *testing.T) {
	// Row 0 has the split ("Dec"+"31,"); row 1 does not — its two cells
	// are a genuine column apart.
	chars := []Char{
		// row 0, y 560-568: adjacent glyphs across the boundary
		{Text: "c", X0: 110, X1: 118, Y0: 560, Y1: 568},
		{Text: "3", X0: 118.02, X1: 126, Y0: 560, Y1: 568},
		// row 1, y 540-548: a wide gutter across the same boundary
		{Text: "A", X0: 92, X1: 100, Y0: 540, Y1: 548},
		{Text: "9", X0: 160, X1: 168, Y0: 540, Y1: 548},
	}
	cells := [][]BBox{
		{{X0: 90, X1: 118, Y0: 559, Y1: 569}, {X0: 118, X1: 180, Y0: 559, Y1: 569}},
		{{X0: 90, X1: 118, Y0: 539, Y1: 549}, {X0: 118, X1: 180, Y0: 539, Y1: 549}},
	}
	rows := [][]string{{"Dec", "31,"}, {"A", "9"}}

	gotRows, gotCells := mergeSplitTokens(rows, cells, chars, 3)

	if len(gotRows) != 2 {
		t.Fatalf("got %d rows, want 2", len(gotRows))
	}
	if len(gotRows[0]) != len(gotRows[1]) {
		t.Fatalf("rows have different column counts (%d vs %d) — the grid sheared: %q",
			len(gotRows[0]), len(gotRows[1]), gotRows)
	}
	if len(gotCells[0]) != len(gotCells[1]) {
		t.Errorf("cell rows have different lengths (%d vs %d)", len(gotCells[0]), len(gotCells[1]))
	}
	// The boundary split a token in row 0, so it goes for the whole table —
	// row 1 merges too, keeping the grid rectangular.
	want := [][]string{{"Dec31,"}, {"A 9"}}
	if !reflect.DeepEqual(gotRows, want) {
		t.Errorf("rows = %q, want %q", gotRows, want)
	}
}

// TestMergeSplitTokensIsOptIn pins the default. pdfplumber produces the
// same splits (verified against pdfplumber 0.11.9), so turning this on
// by default would silently break the parity this package promises.
func TestMergeSplitTokensIsOptIn(t *testing.T) {
	if DefaultTableSettings().MergeSplitTokens {
		t.Error("MergeSplitTokens must default to false — on by default breaks pdfplumber parity")
	}
	if DefaultTableSettings().applyDefaults().MergeSplitTokens {
		t.Error("applyDefaults must not enable MergeSplitTokens")
	}
}
