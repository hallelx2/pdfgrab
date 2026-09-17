// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"fmt"
	"math"
	"testing"
)

// word builds an upright Word at a position, sized roughly like 10pt
// type so the tests exercise realistic geometry rather than unit squares.
func word(text string, x0, y0 float64) Word {
	return Word{
		Text: text, Upright: true,
		X0: x0, Y0: y0,
		X1: x0 + float64(len(text))*5, Y1: y0 + 10,
	}
}

// tableWords lays out a grid: nrows rows at colXs, one word per cell.
func tableWords(colXs []float64, nrows int, topY, rowGap float64) []Word {
	var ws []Word
	for r := 0; r < nrows; r++ {
		y := topY - float64(r)*rowGap
		for c, x := range colXs {
			ws = append(ws, word(fmt.Sprintf("r%dc%d", r, c), x, y))
		}
	}
	return ws
}

func TestDetectsAnUnruledTable(t *testing.T) {
	// Six rows, three columns, no rulings anywhere. This is the case
	// the "lines" strategy cannot see at all — no intersecting rulings
	// means no cells means no table.
	ws := tableWords([]float64{72, 200, 330}, 6, 700, 20)

	got := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts())
	if len(got) != 1 {
		t.Fatalf("got %d regions, want 1: %+v", len(got), got)
	}

	r := got[0]
	// The region must cover every row and reach past the last column's
	// text, or the rightmost column is clipped off.
	if r.Y1 < 710 || r.Y0 > 600 {
		t.Errorf("region %v does not span rows from y=700 down to y=600", r)
	}
	if r.X0 > 72 {
		t.Errorf("region X0 = %v, want <= 72 (first column start)", r.X0)
	}
	if r.X1 < 330+4*5 {
		t.Errorf("region X1 = %v, want past the last column's text", r.X1)
	}
}

// The property the whole method rests on. Prose shares a left margin,
// so a detector keyed on x-coincidence alone finds a table in every
// paragraph — which is exactly how pdfplumber's text strategy reaches
// 0.188 precision. Alignment must be sustained AND the page must not
// otherwise look like a grid.
func TestRejectsProse(t *testing.T) {
	// Lines starting at the same left margin but with nothing else
	// aligned — one long run of words per line, varying lengths.
	var ws []Word
	for i := 0; i < 12; i++ {
		y := 700 - float64(i)*14
		x := 72.0
		for w := 0; w < 8; w++ {
			// Word widths vary, so interior positions never line up.
			text := "lorem"
			if (i+w)%3 == 0 {
				text = "ipsumdolor"
			} else if (i+w)%3 == 1 {
				text = "sit"
			}
			ws = append(ws, word(text, x, y))
			x += float64(len(text))*5 + 4
		}
	}

	got := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts())

	// A single left-margin alignment is not a table. If a region is
	// reported it must at least not claim the whole page as tabular
	// with multiple columns.
	for _, r := range got {
		if r.X1-r.X0 > 300 && r.Y1-r.Y0 > 140 {
			t.Errorf("claimed a page-sized table on prose: %v", r)
		}
	}
}

// MinLines is the precision knob. Below it, a short aligned run must
// not register — otherwise any two-line heading becomes a table.
func TestShortAlignmentIsNotATable(t *testing.T) {
	ws := tableWords([]float64{72, 200, 330}, 3, 700, 20) // 3 < MinLines=4

	if got := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts()); len(got) != 0 {
		t.Errorf("got %d regions for a 3-row alignment, want 0: %+v", len(got), got)
	}
}

func TestMinLinesIsHonoured(t *testing.T) {
	ws := tableWords([]float64{72, 200, 330}, 3, 700, 20)

	opts := DefaultTextEdgeOpts()
	opts.MinLines = 3
	if got := DetectTextEdgeRegions(ws, opts); len(got) != 1 {
		t.Errorf("with MinLines=3 got %d regions, want 1", len(got))
	}
}

// Two tables separated by a large vertical gap must come back as two
// regions. Merging them would produce a region spanning the prose
// between, and every relation across that gap becomes a false positive.
func TestSeparatesTwoTables(t *testing.T) {
	ws := tableWords([]float64{72, 200, 330}, 5, 700, 15)
	// Second table far below — well past EdgeTol.
	ws = append(ws, tableWords([]float64{72, 200, 330}, 5, 300, 15)...)

	got := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts())
	if len(got) != 2 {
		t.Fatalf("got %d regions, want 2: %+v", len(got), got)
	}
	// They must not overlap, or they are really one region reported twice.
	a, b := got[0], got[1]
	if a.Y0 <= b.Y1 && b.Y0 <= a.Y1 {
		t.Errorf("regions overlap vertically: %v and %v", a, b)
	}
}

// EdgeTol exists so a column survives a blank cell. A table whose
// middle row is empty in one column is still one table.
func TestSurvivesAGapWithinEdgeTol(t *testing.T) {
	ws := tableWords([]float64{72, 200, 330}, 3, 700, 15)
	// Resume 30pt below the last row — a gap, but under EdgeTol=50.
	ws = append(ws, tableWords([]float64{72, 200, 330}, 3, 700-2*15-30, 15)...)

	got := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts())
	if len(got) != 1 {
		t.Fatalf("got %d regions, want 1 — a blank row must not split a table: %+v",
			len(got), got)
	}
}

// Right-aligned numeric columns are the common financial-table shape.
// The detector must find them via the right-edge alignment class, not
// only via left edges.
func TestDetectsRightAlignedColumns(t *testing.T) {
	var ws []Word
	rights := []float64{200, 330, 460}
	for r := 0; r < 6; r++ {
		y := 700 - float64(r)*18
		ws = append(ws, word("Label", 72, y))
		for _, right := range rights {
			// Varying width, fixed right edge.
			text := "1234"
			if r%2 == 0 {
				text = "1,234,567"
			}
			w := word(text, right-float64(len(text))*5, y)
			ws = append(ws, w)
		}
	}

	if got := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts()); len(got) == 0 {
		t.Error("found no region in a right-aligned numeric table")
	}
}

func TestEmptyAndTinyInputs(t *testing.T) {
	if got := DetectTextEdgeRegions(nil, DefaultTextEdgeOpts()); got != nil {
		t.Errorf("nil words returned %v, want nil", got)
	}
	if got := DetectTextEdgeRegions([]Word{word("x", 1, 1)}, DefaultTextEdgeOpts()); len(got) != 0 {
		t.Errorf("one word returned %v, want none", got)
	}
}

// Rotated text is not part of a horizontal alignment and must not
// contribute, or a rotated caption can anchor a spurious column.
func TestIgnoresRotatedText(t *testing.T) {
	ws := tableWords([]float64{72, 200, 330}, 6, 700, 20)
	for i := range ws {
		ws[i].Upright = false
	}
	if got := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts()); len(got) != 0 {
		t.Errorf("rotated text produced %d regions, want 0", len(got))
	}
}

// Single characters align with anything. A column of bullets or digits
// is not evidence of a table on its own.
func TestIgnoresVeryShortLines(t *testing.T) {
	var ws []Word
	for r := 0; r < 8; r++ {
		ws = append(ws, word("*", 72, 700-float64(r)*15))
	}
	if got := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts()); len(got) != 0 {
		t.Errorf("a column of bullets produced %d regions, want 0", len(got))
	}
}

// The zero value must not silently disable the method: an unset
// MinLines of 0 would make every alignment valid.
func TestZeroOptsFallBackToDefaults(t *testing.T) {
	ws := tableWords([]float64{72, 200, 330}, 6, 700, 20)

	zero := DetectTextEdgeRegions(ws, TextEdgeOpts{})
	def := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts())
	if len(zero) != len(def) {
		t.Errorf("zero opts gave %d regions, defaults gave %d — defaults did not apply",
			len(zero), len(def))
	}
}

// Determinism: the same page must produce the same regions every time.
// Map iteration or unstable sorts would make the benchmark unreproducible,
// which the 2026-09-17 evaluation established as a requirement.
func TestIsDeterministic(t *testing.T) {
	ws := tableWords([]float64{72, 200, 330}, 6, 700, 20)
	ws = append(ws, tableWords([]float64{90, 250}, 5, 400, 18)...)

	first := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts())
	for i := 0; i < 25; i++ {
		got := DetectTextEdgeRegions(ws, DefaultTextEdgeOpts())
		if len(got) != len(first) {
			t.Fatalf("run %d returned %d regions, first returned %d", i, len(got), len(first))
		}
		for j := range got {
			if math.Abs(got[j].X0-first[j].X0) > 1e-9 ||
				math.Abs(got[j].Y0-first[j].Y0) > 1e-9 ||
				math.Abs(got[j].X1-first[j].X1) > 1e-9 ||
				math.Abs(got[j].Y1-first[j].Y1) > 1e-9 {
				t.Fatalf("run %d region %d = %v, first = %v", i, j, got[j], first[j])
			}
		}
	}
}
