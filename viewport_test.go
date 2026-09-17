// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab_test

import (
	"math"
	"testing"

	"github.com/hallelx2/pdfgrab"
)

func closeTo(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// TestViewportFlipsYAxis pins the contract a citation-highlight frontend
// depends on. PDF user space puts the origin at the bottom-left with Y
// growing up; browsers, canvas and PDF.js put it top-left with Y growing
// down. Getting this backwards produces an overlay that is vertically
// mirrored — highlights that look plausible and land on the wrong row.
func TestViewportFlipsYAxis(t *testing.T) {
	const pageH = 792.0 // US Letter

	// A box sitting at the very TOP of the page (high Y in PDF space)
	// must come back with a near-ZERO Top in viewer space.
	top := pdfgrab.BBox{X0: 100, Y0: 782, X1: 200, Y1: 792}
	r := top.Viewport(pageH, 1)
	closeTo(t, "top box Top", r.Top, 0)
	closeTo(t, "top box Left", r.Left, 100)
	closeTo(t, "top box Width", r.Width, 100)
	closeTo(t, "top box Height", r.Height, 10)

	// A box at the BOTTOM (low Y) must land at the far side.
	bottom := pdfgrab.BBox{X0: 0, Y0: 0, X1: 10, Y1: 10}
	rb := bottom.Viewport(pageH, 1)
	closeTo(t, "bottom box Top", rb.Top, 782)

	if r.Top >= rb.Top {
		t.Error("a box higher on the page must have a SMALLER Top in viewer space — Y axis is not flipped")
	}
}

// TestViewportScale checks the rendered-pixels-per-point factor, the
// other half of what a frontend needs.
func TestViewportScale(t *testing.T) {
	const pageH = 792.0
	b := pdfgrab.BBox{X0: 56.7, Y0: 559.7, X1: 537.9, Y1: 568.4}

	// 150 DPI raster: 150/72 pixels per point. These are the real
	// coordinates of the "Less: Accumulated depreciation" row on 3M's
	// 2018 10-K balance sheet, verified against the rendered page.
	const scale = 150.0 / 72.0
	r := b.Viewport(pageH, scale)
	closeTo(t, "Left", r.Left, 56.7*scale)
	closeTo(t, "Top", r.Top, (pageH-568.4)*scale)
	closeTo(t, "Width", r.Width, (537.9-56.7)*scale)
	closeTo(t, "Height", r.Height, (568.4-559.7)*scale)

	// Scale 1 is the identity on size, flip only.
	r1 := b.Viewport(pageH, 1)
	closeTo(t, "unscaled Width", r1.Width, 537.9-56.7)
}

// TestNormalizedIsResolutionIndependent covers the percentage form,
// which is what a resizable viewer should store.
func TestNormalizedIsResolutionIndependent(t *testing.T) {
	const pageW, pageH = 612.0, 792.0
	b := pdfgrab.BBox{X0: 306, Y0: 396, X1: 612, Y1: 792} // exact top-right quadrant

	n := b.Normalized(pageW, pageH)
	closeTo(t, "Left", n.Left, 0.5)
	closeTo(t, "Top", n.Top, 0.0)
	closeTo(t, "Width", n.Width, 0.5)
	closeTo(t, "Height", n.Height, 0.5)

	// Multiplying by any rendered size must agree with Viewport at the
	// matching scale — the two APIs cannot drift apart.
	for _, px := range []float64{612, 1224, 2448} {
		scale := px / pageW
		v := b.Viewport(pageH, scale)
		closeTo(t, "Left agrees", n.Left*px, v.Left)
		closeTo(t, "Width agrees", n.Width*px, v.Width)
	}

	// Degenerate pages must not emit NaN into a JSON payload.
	if got := b.Normalized(0, 0); got != (pdfgrab.ViewRect{}) {
		t.Errorf("Normalized on a zero-sized page = %+v, want zero ViewRect", got)
	}
}

// TestViewportOnRealCitation is the end-to-end check: take a real cell
// bbox from a real extraction and confirm the viewer rectangle lands
// inside the page, right way up.
func TestViewportOnRealCitation(t *testing.T) {
	doc, err := pdfgrab.OpenFile("testdata/golden/simple1.pdf")
	if err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}
	defer doc.Close()
	p, err := doc.Page(1)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	words, err := p.Words(pdfgrab.DefaultWordOpts())
	if err != nil || len(words) == 0 {
		t.Fatalf("Words: %v (n=%d)", err, len(words))
	}
	pw, ph := p.Width(), p.Height()
	for _, w := range words {
		b := pdfgrab.BBox{X0: w.X0, Y0: w.Y0, X1: w.X1, Y1: w.Y1}
		r := b.Viewport(ph, 1)
		if r.Top < 0 || r.Top > ph {
			t.Errorf("word %q: Top=%v outside page height %v", w.Text, r.Top, ph)
		}
		if r.Left < 0 || r.Left > pw {
			t.Errorf("word %q: Left=%v outside page width %v", w.Text, r.Left, pw)
		}
		if r.Width <= 0 || r.Height <= 0 {
			t.Errorf("word %q: degenerate ViewRect %+v", w.Text, r)
		}
		n := b.Normalized(pw, ph)
		if n.Left < 0 || n.Left > 1 || n.Top < 0 || n.Top > 1 {
			t.Errorf("word %q: normalized ViewRect outside 0..1: %+v", w.Text, n)
		}
	}
}
