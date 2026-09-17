// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import "testing"

// TestCharsInCellEdgesKeepsStraddlingGlyph reproduces, to the exact
// coordinate, the geometry that corrupted 3M's 2018 10-K balance sheet.
//
// The value "(16,048)" sits in the rightmost column. Its closing ")"
// spans x=536.691..539.067, so its centre is x=537.879. The column's
// right edge — which is also the table's outer edge — is x=537.871. The
// glyph missed the midpoint test by 0.008pt, roughly a nine-thousandth
// of an inch, and was dropped. "(16,048)" became "16,048": accounting
// notation for -16,048 read back as +16,048.
//
// Across that filing's five financial statements the same hair's-breadth
// geometry flipped the sign of 19% of all negative numbers, so this test
// pins real coordinates rather than round synthetic ones.
func TestCharsInCellEdgesKeepsStraddlingGlyph(t *testing.T) {
	const y0, y1 = 560.57, 567.77
	mk := func(s string, x0, x1 float64) Char {
		return Char{Text: s, X0: x0, X1: x1, Y0: y0, Y1: y1}
	}
	chars := []Char{
		mk("(", 514.500, 516.876),
		mk("1", 516.891, 520.462),
		mk("6", 520.491, 524.062),
		mk(",", 524.091, 525.876),
		mk("0", 525.891, 529.462),
		mk("4", 529.491, 533.062),
		mk("8", 533.091, 536.662),
		mk(")", 536.691, 539.067), // centre 537.879, edge is 537.871
	}
	cell := BBox{X0: 518.100, X1: 537.871, Y0: 559.0, Y1: 569.0}

	// The opening "(" (centre 515.688) falls in the column to the LEFT of
	// this one — a separate, cosmetic split. It is recoverable, because
	// the glyph still exists in an adjacent cell and joining the row
	// restores the value. The closing ")" was not: it fell off the outer
	// edge of the table and was deleted outright.
	if got := len(charsInCellEdges(chars, cell, false, false)); got != 6 {
		t.Errorf("interior cell kept %d glyphs, want 6", got)
	}

	// Outer right edge: nothing else can claim the ")", so dropping it is
	// pure data loss.
	outer := charsInCellEdges(chars, cell, false, true)
	if len(outer) != 7 {
		t.Fatalf("outer cell kept %d glyphs, want 7 (the closing paren must survive)", len(outer))
	}
	var got string
	for _, c := range outer {
		got += c.Text
	}
	if got != "16,048)" {
		t.Errorf("outer cell text = %q, want %q — losing the paren silently flips the sign", got, "16,048)")
	}
}

// TestCharsInCellEdgesDoesNotAbsorbDistantGlyphs pins the bound on the
// widening: it is the straddling glyph itself, not an open-ended reach
// past the table edge. A glyph sitting entirely beyond the boundary
// belongs to the page, not to this cell.
func TestCharsInCellEdgesDoesNotAbsorbDistantGlyphs(t *testing.T) {
	cell := BBox{X0: 100, X1: 200, Y0: 0, Y1: 10}
	chars := []Char{
		{Text: "A", X0: 150, X1: 160, Y0: 2, Y1: 8}, // inside
		{Text: "B", X0: 195, X1: 210, Y0: 2, Y1: 8}, // straddles the edge
		{Text: "C", X0: 240, X1: 250, Y0: 2, Y1: 8}, // wholly outside
	}
	got := charsInCellEdges(chars, cell, false, true)
	if len(got) != 2 {
		t.Fatalf("kept %d glyphs, want 2 (inside + straddling, never the distant one)", len(got))
	}
	for _, c := range got {
		if c.Text == "C" {
			t.Error("absorbed a glyph lying wholly outside the table edge")
		}
	}

	// Vertical containment is unaffected by the horizontal widening.
	tall := []Char{{Text: "D", X0: 195, X1: 210, Y0: 50, Y1: 60}}
	if n := len(charsInCellEdges(tall, cell, false, true)); n != 0 {
		t.Errorf("kept %d glyphs from another row, want 0", n)
	}
}

// TestCharsInCellUnchanged guards the default path: every existing
// caller gets exactly the previous midpoint-only behaviour.
func TestCharsInCellUnchanged(t *testing.T) {
	cell := BBox{X0: 100, X1: 200, Y0: 0, Y1: 10}
	chars := []Char{
		{Text: "A", X0: 150, X1: 160, Y0: 2, Y1: 8},
		{Text: "B", X0: 195, X1: 210, Y0: 2, Y1: 8}, // centre 202.5, outside
	}
	got := charsInCell(chars, cell)
	if len(got) != 1 || got[0].Text != "A" {
		t.Errorf("charsInCell = %v, want just the interior glyph", got)
	}
}
