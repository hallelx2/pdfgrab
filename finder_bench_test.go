// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

// finder_bench_test.go pins the performance characteristics of the
// cell-finding pipeline. The dense-grid cases are a regression guard
// against the O(n^2)/O(n^3) blow-up that used to hang the parser for
// minutes on finely-ruled financial-document pages: a fine ruling
// grid with hundreds of vertical + horizontal rulings yields tens of
// thousands of intersections, and the pre-v0.3.1 suffix-rescan cell
// finder was quadratic in that count.
//
// BenchmarkIntersectionsToCellsDenseGrid measures the hot path on a
// 200x200 lattice (40,000 intersections). TestDenseGridTerminates
// Quickly turns the same input into a hard wall-clock assertion so
// CI fails loudly if anyone reintroduces the quadratic behaviour.

import (
	"testing"
	"time"

	"github.com/hallelx2/pdfgrab/internal/layout"
)

// denseGridEdges builds the edge set for an n x n cell lattice:
// (n+1) horizontal rulings crossed by (n+1) vertical rulings, every
// ruling spanning the full extent so every crossing is a real
// intersection. That yields (n+1)^2 intersections and n^2 cells — the
// worst realistic case a fine-ruled table produces.
func denseGridEdges(n int) []layout.Edge {
	const step = 10.0
	span := float64(n) * step
	edges := make([]layout.Edge, 0, 2*(n+1))
	for i := 0; i <= n; i++ {
		pos := float64(i) * step
		// Horizontal ruling at Y = pos spanning the full width.
		edges = append(edges, makeH(0, span, pos))
		// Vertical ruling at X = pos spanning the full height.
		edges = append(edges, makeV(pos, 0, span))
	}
	return edges
}

// BenchmarkIntersectionsToCellsDenseGrid benchmarks the cell finder on
// a 200x200 lattice (40,401 intersections, 40,000 cells). With the
// grid-indexed implementation this completes in well under a second;
// the old suffix-rescan implementation took tens of seconds to
// minutes.
func BenchmarkIntersectionsToCellsDenseGrid(b *testing.B) {
	edges := denseGridEdges(200)
	ints := edgesToIntersections(edges, 0.1, 0.1)
	if len(ints) != 201*201 {
		b.Fatalf("setup: got %d intersections, want %d", len(ints), 201*201)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cells := intersectionsToCells(ints)
		if len(cells) != 200*200 {
			b.Fatalf("got %d cells, want %d", len(cells), 200*200)
		}
	}
}

// BenchmarkEdgesToIntersectionsDenseGrid benchmarks the intersection
// scan on the same dense lattice. The sweep implementation only tests
// edge pairs whose spans overlap, so a full grid (where they all
// overlap) is its worst case yet still finishes promptly.
func BenchmarkEdgesToIntersectionsDenseGrid(b *testing.B) {
	edges := denseGridEdges(200)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ints := edgesToIntersections(edges, 0.1, 0.1)
		if len(ints) != 201*201 {
			b.Fatalf("got %d intersections, want %d", len(ints), 201*201)
		}
	}
}

// TestDenseGridTerminatesQuickly is the hard regression guard for the
// O(n^2)/O(n^3) cell-finding blow-up. On a 200x200 lattice (40,401
// intersections, 40,000 cells) the grid-indexed intersectionsToCells
// finishes in a few hundred milliseconds; the pre-v0.3.1 suffix-rescan
// implementation took ~78 seconds on this exact input (and the
// production symptom was a 20+ minute hang on real financial pages with
// comparable intersection counts). The < 2s assertion therefore sits
// far below any quadratic regression while leaving generous headroom
// for a loaded CI machine.
//
// The intersection list is built as untimed setup so the assertion
// isolates the cell finder — the stage that actually exhibited the
// cubic behaviour. The intersection sweep has its own benchmark.
func TestDenseGridTerminatesQuickly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dense-grid timing test in -short mode")
	}
	const n = 200
	edges := denseGridEdges(n)
	ints := edgesToIntersections(edges, 0.1, 0.1)
	if got, want := len(ints), (n+1)*(n+1); got != want {
		t.Fatalf("intersections: got %d, want %d", got, want)
	}

	start := time.Now()
	cells := intersectionsToCells(ints)
	elapsed := time.Since(start)

	if got, want := len(cells), n*n; got != want {
		t.Fatalf("cells: got %d, want %d", got, want)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("intersectionsToCells on a dense %dx%d grid (%d intersections) took %v, want < 2s (quadratic regression?)",
			n, n, len(ints), elapsed)
	}
	t.Logf("intersectionsToCells: dense %dx%d grid, %d intersections -> %d cells in %v", n, n, len(ints), len(cells), elapsed)
}

// TestDenseGridPipelineCompletes is a looser end-to-end guard over the
// full edges -> intersections -> cells path. It uses a generous bound
// (the old cell finder alone blew well past this) so it can't flake on
// a contended machine, while still catching a catastrophic regression
// in either stage.
func TestDenseGridPipelineCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dense-grid pipeline timing test in -short mode")
	}
	const n = 200
	edges := denseGridEdges(n)

	start := time.Now()
	ints := edgesToIntersections(edges, 0.1, 0.1)
	cells := intersectionsToCells(ints)
	elapsed := time.Since(start)

	if got, want := len(ints), (n+1)*(n+1); got != want {
		t.Fatalf("intersections: got %d, want %d", got, want)
	}
	if got, want := len(cells), n*n; got != want {
		t.Fatalf("cells: got %d, want %d", got, want)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("full dense %dx%d pipeline took %v, want < 10s", n, n, elapsed)
	}
	t.Logf("full pipeline: dense %dx%d grid -> %d intersections, %d cells in %v", n, n, len(ints), len(cells), elapsed)
}
