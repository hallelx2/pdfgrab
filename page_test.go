// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab_test

import (
	"math"
	"strings"
	"testing"

	"github.com/hallelx2/pdfgrab"
	"github.com/hallelx2/pdfgrab/testdata"
)

// TestCharsHelloWorld walks the hand-crafted "Hello, world!" page
// and asserts the glyphs come out in the right order with sensible
// positions. We don't lock in the absolute x/y values too strictly
// because the font metrics for Helvetica are fixed in the standard
// 14 and our default-width fallback is approximate — instead we
// assert:
//
//  1. Joined Text equals "Hello, world!".
//  2. Glyphs are emitted left-to-right (X0 strictly increases for
//     non-space chars).
//  3. Y baseline is roughly where the content stream put it (720).
func TestCharsHelloWorld(t *testing.T) {
	doc, err := pdfgrab.OpenBytes(testdata.Hello())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()

	p, err := doc.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}
	chars, err := p.Chars()
	if err != nil {
		t.Fatalf("Chars: %v", err)
	}

	var joined strings.Builder
	for _, c := range chars {
		joined.WriteString(c.Text)
	}
	got := joined.String()
	if got != "Hello, world!" {
		t.Fatalf("joined chars = %q, want %q", got, "Hello, world!")
	}

	// Left-to-right monotonic-X check: skip the space (its bbox can
	// look thin and X1 of the previous glyph may overlap slightly
	// with the space's X0 due to font metrics).
	prevX := -math.MaxFloat64
	for i, c := range chars {
		if c.Text == " " {
			continue
		}
		if c.X0 < prevX-0.5 {
			t.Errorf("char %d (%q): X0=%.2f < prev=%.2f (not left-to-right)", i, c.Text, c.X0, prevX)
		}
		prevX = c.X0
	}

	// Baseline check: every char should have Y0 within a few points
	// of 720 (the Td target). PDF descent moves Y0 slightly below.
	for i, c := range chars {
		if c.Y0 < 700 || c.Y0 > 730 {
			t.Errorf("char %d (%q): Y0=%.2f out of [700,730]", i, c.Text, c.Y0)
		}
		if c.FontName != "Helvetica" {
			t.Errorf("char %d (%q): FontName=%q, want Helvetica", i, c.Text, c.FontName)
		}
		if c.FontSize != 12 {
			t.Errorf("char %d (%q): FontSize=%v, want 12", i, c.Text, c.FontSize)
		}
		if !c.Upright {
			t.Errorf("char %d (%q): Upright=false, want true", i, c.Text)
		}
	}
}

// TestRectsRulings opens the hand-crafted rules.pdf and asserts the
// page has exactly the expected stroked rectangle and two lines at
// the right positions (±0.5 pt golden tolerance).
//
// The fixture draws:
//   - one rect at (100, 600) size 100×100
//   - one horizontal line from (100, 580) to (150, 580)
//   - one vertical line from (100, 500) to (100, 550)
func TestRectsRulings(t *testing.T) {
	doc, err := pdfgrab.OpenBytes(testdata.Rules())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()

	p, err := doc.Page(1)
	if err != nil {
		t.Fatalf("Page(1): %v", err)
	}
	objs, err := p.Objects()
	if err != nil {
		t.Fatalf("Objects: %v", err)
	}

	if len(objs.Rects) != 1 {
		t.Fatalf("Rects count = %d, want 1", len(objs.Rects))
	}
	r := objs.Rects[0]
	if !approxEq(r.X0, 100) || !approxEq(r.Y0, 600) ||
		!approxEq(r.X1, 200) || !approxEq(r.Y1, 700) {
		t.Errorf("Rect bbox = (%.2f,%.2f)-(%.2f,%.2f), want (100,600)-(200,700)",
			r.X0, r.Y0, r.X1, r.Y1)
	}
	if !r.Stroke {
		t.Error("Rect.Stroke = false, want true")
	}
	if r.Fill {
		t.Error("Rect.Fill = true, want false")
	}

	if len(objs.Lines) != 2 {
		t.Fatalf("Lines count = %d, want 2", len(objs.Lines))
	}
	// Sort by X0 ascending so the assertions are positional, not
	// content-stream-order dependent.
	lines := objs.Lines
	if lines[0].X0 > lines[1].X0 {
		lines[0], lines[1] = lines[1], lines[0]
	}
	// First line: (100,580)→(150,580). Second: (100,500)→(100,550).
	// They both start at X0=100, so order them by Y next.
	if lines[0].Y0 > lines[1].Y0 {
		lines[0], lines[1] = lines[1], lines[0]
	}

	// Vertical line first (lower Y), horizontal second.
	if !approxEq(lines[0].X0, 100) || !approxEq(lines[0].Y0, 500) ||
		!approxEq(lines[0].X1, 100) || !approxEq(lines[0].Y1, 550) {
		t.Errorf("vertical line = (%.2f,%.2f)-(%.2f,%.2f), want (100,500)-(100,550)",
			lines[0].X0, lines[0].Y0, lines[0].X1, lines[0].Y1)
	}
	if !approxEq(lines[1].X0, 100) || !approxEq(lines[1].Y0, 580) ||
		!approxEq(lines[1].X1, 150) || !approxEq(lines[1].Y1, 580) {
		t.Errorf("horizontal line = (%.2f,%.2f)-(%.2f,%.2f), want (100,580)-(150,580)",
			lines[1].X0, lines[1].Y0, lines[1].X1, lines[1].Y1)
	}
}

// TestObjectsAggregator confirms Objects() bundles all four primitive
// collections in a single call and matches the per-type accessors.
func TestObjectsAggregator(t *testing.T) {
	doc, err := pdfgrab.OpenBytes(testdata.Rules())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()
	p, _ := doc.Page(1)
	objs, err := p.Objects()
	if err != nil {
		t.Fatalf("Objects: %v", err)
	}
	rects, _ := p.Rects()
	lines, _ := p.Lines()
	if len(objs.Rects) != len(rects) {
		t.Errorf("Objects.Rects len=%d vs Rects() len=%d", len(objs.Rects), len(rects))
	}
	if len(objs.Lines) != len(lines) {
		t.Errorf("Objects.Lines len=%d vs Lines() len=%d", len(objs.Lines), len(lines))
	}
}

// TestRealWorldSimplePDF opens a real-world PDF (sourced from
// pdfminer.six's testdata) and asserts the parser at least makes it
// through the content stream without errors. We don't lock in the
// exact glyph count because the fixture is not under our control —
// the goal here is "does the parser handle a non-trivial-but-still-
// minimal real PDF without crashing or producing nonsense", which is
// the smoke-test floor for the v0.0.1 release.
func TestRealWorldSimplePDF(t *testing.T) {
	doc, err := pdfgrab.OpenFile("testdata/simple1.pdf")
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	if doc.NumPages() < 1 {
		t.Fatalf("NumPages = %d, want >= 1", doc.NumPages())
	}
	for n, p := range doc.Pages() {
		objs, err := p.Objects()
		if err != nil {
			t.Errorf("page %d Objects: %v", n, err)
			continue
		}
		// Per-page sanity: width/height look reasonable for a doc PDF.
		w, h := p.Width(), p.Height()
		if w <= 0 || h <= 0 {
			t.Errorf("page %d: bad dimensions w=%v h=%v", n, w, h)
		}
		// Touch the slices so the compiler doesn't strip the call.
		_ = objs.Chars
		_ = objs.Lines
		_ = objs.Rects
		_ = objs.Curves
	}
}

func approxEq(a, b float64) bool { return math.Abs(a-b) < 0.5 }
