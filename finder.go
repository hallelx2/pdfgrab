// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

// finder.go is the Go port of pdfplumber/table.py's TableFinder. The
// algorithm runs in four stages, each implemented as a pure function
// below so it can be unit-tested without spinning up a Page:
//
//  1. getEdges        — derive Lines/Rects/Curves from page primitives,
//                       apply prefilter, merge (snap + join), apply the
//                       post-merge min-length filter. Vertical edges
//                       from the "vertical_strategy" go in alongside the
//                       horizontal edges from the "horizontal_strategy".
//                       Implemented as Page.findTableEdges in page.go.
//  2. edgesToIntersections — pair every vertical edge with every
//                            horizontal edge and record the (x, y)
//                            intersection points whose perpendicular
//                            distance is within intersectionTolerance.
//  3. intersectionsToCells — for each intersection, walk down and right
//                            looking for the smallest closed rectangle
//                            whose four corners are all intersections
//                            joined by edges. Each found rectangle is
//                            one cell.
//  4. cellsToTables   — group cells that share at least one corner into
//                       the same table. Tables sorted top-to-bottom-
//                       then-left-to-right.
//
// Coordinate system note: pdfplumber operates in image space (Y growing
// DOWN). pdfgrab uses PDF user space (Y growing UP). The intersection
// algorithm is invariant under that flip — "below" in image space is
// "below" in user space if we substitute "lower Y" — but the wording in
// pdfplumber's source talks about "directly below and directly right".
// We keep the same algorithm, sorted by (Y descending, X ascending) so
// "below" means smaller Y (visually lower) and "right" means larger X.
// pdfplumber's points list is sorted ascending in image-space Y (so the
// FIRST point is the visually-topmost), which maps in user space to
// DESCENDING Y. The intersection logic uses point equality on (x, y)
// so the sort order only affects iteration order and the resulting
// cell list is the same.

import (
	"fmt"
	"sort"

	"github.com/hallelx2/pdfgrab/internal/layout"
)

// Intersection records one crossing point: an (x, y) tuple plus the
// vertical and horizontal edges that meet there. We need the edge sets
// (not just the count) because the cell-finder asks "does the same
// edge connect points p1 and p2?" — checking that two points lie on a
// shared edge is how the algorithm distinguishes "two intersections on
// the same ruler" from "two intersections on parallel rulers that
// happen to align".
//
// Field naming follows pdfplumber's intersections dict-of-dicts shape:
// the X/Y are the keys, V/H are the value lists. We keep them as slice
// fields so the struct is value-comparable on (X, Y) alone.
type Intersection struct {
	X, Y float64
	V    []layout.Edge // vertical edges passing through (X, Y)
	H    []layout.Edge // horizontal edges passing through (X, Y)
}

// TableBox is one detected table, expressed as a bbox plus a 2-D grid
// of cell bboxes. Rows are visually top-to-bottom; columns are left-to-
// right. CellsGrid[i][j] gives the bbox of the cell at row i, column j;
// missing cells (rectangular gaps in the grid) are reported as the
// zero BBox, NOT removed — callers can detect "this cell was missing"
// by checking IsZero on the entry.
//
// This is the geometry-only intermediate between FindTables and
// ExtractTables: FindTables returns one of these per detected table;
// ExtractTables then runs text-extraction per cell and wraps the
// result in a Table.
type TableBox struct {
	// BBox is the union of every cell's bbox.
	BBox BBox

	// Rows is the row count.
	Rows int

	// Cols is the column count.
	Cols int

	// CellsGrid is the per-cell bbox aligned to Rows × Cols. The
	// entry at [i][j] is the bbox of the cell at visual row i (0 is
	// topmost) and column j (0 is leftmost). Empty cells are the zero
	// BBox.
	CellsGrid [][]BBox
}

// Cells returns the cell bboxes flattened into reading order
// (left-to-right, top-to-bottom). Zero-bbox entries (holes in the
// grid) are skipped. Convenience helper for callers that want a single
// iterable.
func (t TableBox) Cells() []BBox {
	out := make([]BBox, 0, t.Rows*t.Cols)
	for _, row := range t.CellsGrid {
		for _, c := range row {
			if !c.IsZero() {
				out = append(out, c)
			}
		}
	}
	return out
}

// TableFinder is the geometry-only result of running the cells-from-
// edges pipeline on a page. It exposes the intermediate stages
// (edges, intersections, raw cells) alongside the assembled TableBox
// list so callers building debugging tools or custom text-extraction
// can see exactly what the pipeline produced.
//
// Pdfplumber bundles the page reference inside its TableFinder and
// exposes Table objects with an .extract() method; we keep the
// finder a pure value (no Page pointer) and let callers either grab
// the assembled Tables from Page.ExtractTables or compose their own
// text-fill loop using the public Cells and CellsGrid.
type TableFinder struct {
	// Edges is the merged, length-filtered edge list used as the
	// input to the intersection scan. Useful for debugging "why
	// didn't this rule get picked up" issues.
	Edges []layout.Edge

	// Intersections is the full set of edge crossings, keyed by
	// (X, Y). The order is deterministic — sorted by Y descending,
	// then X ascending — so callers can rely on iteration order.
	Intersections []Intersection

	// Cells is the raw list of detected cell bboxes BEFORE grouping
	// into tables. Each is a single rectangle whose four corners are
	// intersections joined by shared edges.
	Cells []BBox

	// Tables is the final list of detected tables. Each carries a
	// bbox plus a CellsGrid aligned to row/column order. Tables are
	// sorted top-to-bottom-then-left-to-right by their topmost cell.
	Tables []TableBox
}

// edgesToIntersections is the Go port of pdfplumber's
// edges_to_intersections. Given a slice of merged edges, return the
// list of crossing points where a vertical edge meets a horizontal
// edge within the supplied perpendicular tolerance.
//
// The algorithm:
//   - Split edges by orientation.
//   - Sort vertical edges by (X, smallest Y); sort horizontal edges
//     by (Y descending so top-to-bottom in user space, X).
//   - For each (v, h) pair, test whether the vertical edge's Y span
//     covers h.Y (within yTol) AND h's X span covers v.X (within xTol).
//     If yes, register (v.X, h.Y) as an intersection.
//
// We deduplicate intersection points on (X, Y) equality — if multiple
// edge pairs land on the same exact point, the V and H slices
// accumulate all participating edges. This matches pdfplumber's
// behaviour: its dict-keyed-on-vertex collapses repeats.
//
// xTol and yTol default to 0 if zero; the caller (TableSettings)
// already substitutes the pdfplumber default (3) before reaching this
// function.
func edgesToIntersections(edges []layout.Edge, xTol, yTol float64) []Intersection {
	if len(edges) == 0 {
		return nil
	}

	vEdges := layout.FilterEdgesByOrientation(edges, layout.Vertical)
	hEdges := layout.FilterEdgesByOrientation(edges, layout.Horizontal)

	// Sort: pdfplumber sorts v by (x0, top) and h by (top, x0). In
	// PDF user space "top" means LARGER Y. We sort v by (X0 asc,
	// Y0 asc) and h by (Y0 asc, X0 asc). Sorting h by Y0 ascending is
	// what lets the sweep below window in on the band of horizontal
	// edges a given vertical edge can possibly cross, via binary
	// search, instead of testing every pair.
	sort.SliceStable(vEdges, func(i, j int) bool {
		if vEdges[i].X0 != vEdges[j].X0 {
			return vEdges[i].X0 < vEdges[j].X0
		}
		return vEdges[i].Y0 < vEdges[j].Y0
	})
	sort.SliceStable(hEdges, func(i, j int) bool {
		if hEdges[i].Y0 != hEdges[j].Y0 {
			return hEdges[i].Y0 < hEdges[j].Y0
		}
		return hEdges[i].X0 < hEdges[j].X0
	})

	// hYs is the ascending Y0 of every horizontal edge, parallel to
	// hEdges, so sort.Search can locate the band [v.Y0-yTol, v.Y1+yTol]
	// without recomputing.
	hYs := make([]float64, len(hEdges))
	for i, h := range hEdges {
		hYs[i] = h.Y0
	}

	// Key on (X, Y) using two floats — float64 comparisons are
	// exact-equality after snap_edges has unified positions onto
	// cluster means, so a struct key works without epsilon games.
	type key struct {
		x, y float64
	}
	indexByKey := make(map[key]int)
	var out []Intersection

	// Sweep: for each vertical edge, only the horizontal edges whose Y
	// lies in [v.Y0 - yTol, v.Y1 + yTol] can possibly cross it. Because
	// hEdges is sorted by Y0 ascending, that window is a contiguous
	// slice located by binary search — so a dense page no longer pays
	// the full V x H cross product. Within the window we still apply
	// pdfplumber's exact predicate (the Y band test is already
	// satisfied by construction; the X-cover test is checked per edge).
	for _, v := range vEdges {
		lo := sort.SearchFloat64s(hYs, v.Y0-yTol)
		// Upper bound: first index whose Y0 exceeds v.Y1 + yTol.
		hi := sort.Search(len(hYs), func(i int) bool {
			return hYs[i] > v.Y1+yTol
		})
		for hIdx := lo; hIdx < hi; hIdx++ {
			h := hEdges[hIdx]
			// The [lo, hi) window already enforces pdfplumber's two Y
			// conditions exactly:
			//
			//   v.Y0 <= h.Y0 + yTol  AND  v.Y1 >= h.Y0 - yTol
			//
			// (lo is the first h with h.Y0 >= v.Y0 - yTol; hi is the
			// first h with h.Y0 > v.Y1 + yTol.) Only the X-cover test
			// remains — h must span v.X0 with xTol slack on each side:
			if v.X0 < h.X0-xTol {
				continue
			}
			if v.X0 > h.X1+xTol {
				continue
			}
			k := key{x: v.X0, y: h.Y0}
			if idx, ok := indexByKey[k]; ok {
				out[idx].V = append(out[idx].V, v)
				out[idx].H = append(out[idx].H, h)
			} else {
				indexByKey[k] = len(out)
				out = append(out, Intersection{
					X: v.X0,
					Y: h.Y0,
					V: []layout.Edge{v},
					H: []layout.Edge{h},
				})
			}
		}
	}

	// Deterministic order: pdfplumber sorts the keys ascending in
	// (image-space) top, then x0 — which in user space is Y
	// DESCENDING (visually top first), then X ascending.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Y != out[j].Y {
			return out[i].Y > out[j].Y
		}
		return out[i].X < out[j].X
	})
	return out
}

// edgeSharedKey is the equality test used to decide whether two
// intersection lists share an edge. We hash edges by their full
// geometric tuple so that two intersections that lie on the SAME
// merged edge (post snap+join, so identical X/Y) share the key.
type edgeSharedKey struct {
	x0, y0, x1, y1 float64
	o              layout.Orientation
}

func edgeKey(e layout.Edge) edgeSharedKey {
	return edgeSharedKey{x0: e.X0, y0: e.Y0, x1: e.X1, y1: e.Y1, o: e.Orientation}
}

// dedupEdgeKeys turns an edge slice into a deduplicated slice of
// edgeSharedKeys. The cell finder calls edge_connects as a set
// intersection; pre-deduping each intersection's edge list once means
// the per-pair test (sharesKey) is a scan over a handful of unique
// rulers rather than a re-derivation. Real intersections lie on only
// one or two distinct rulers per axis, so these slices are tiny and a
// linear dedup beats a map allocation.
func dedupEdgeKeys(edges []layout.Edge) []edgeSharedKey {
	if len(edges) == 0 {
		return nil
	}
	out := make([]edgeSharedKey, 0, len(edges))
	for _, e := range edges {
		k := edgeKey(e)
		dup := false
		for _, existing := range out {
			if existing == k {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, k)
		}
	}
	return out
}

// sharesKey reports whether two deduplicated edge-key slices have any
// element in common. Both slices hold at most a few entries (the
// rulers passing through one intersection on one axis), so the nested
// scan is effectively constant-time and allocation-free.
func sharesKey(a, b []edgeSharedKey) bool {
	for _, ka := range a {
		for _, kb := range b {
			if ka == kb {
				return true
			}
		}
	}
	return false
}

// edgeConnects reports whether p1 and p2 share an edge — i.e. lie on
// the same merged ruler. This is the predicate pdfplumber uses to
// distinguish "two points on the same line" from "two points on
// parallel lines that happen to align".
//
// p1 and p2 must share an axis (same X for vertical-shared, same Y
// for horizontal-shared); the function returns false otherwise.
//
// This is the convenience entry point used by tests and any caller
// that has only the two Intersection values. The cell finder uses the
// pre-computed edge-key form (sharesKey on gridNode slices) on its hot
// path instead, so it never re-walks the raw edge slices per pair.
func edgeConnects(p1, p2 Intersection) bool {
	if p1.X == p2.X && sharesKey(dedupEdgeKeys(p1.V), dedupEdgeKeys(p2.V)) {
		return true
	}
	if p1.Y == p2.Y && sharesKey(dedupEdgeKeys(p1.H), dedupEdgeKeys(p2.H)) {
		return true
	}
	return false
}

// intersectionsToCells is the Go port of pdfplumber's
// intersections_to_cells. Given the intersection list, return the
// smallest closed rectangle anchored at each intersection — that's
// one cell.
//
// pdfplumber's algorithm, per anchor point pt:
//   - `below` = all points sharing pt's X with a lower Y (visually
//     below pt), nearest first.
//   - `right` = all points sharing pt's Y with a larger X, nearest
//     first.
//   - For the nearest `below` point that shares a vertical edge with
//     pt, scan `right` (nearest first) for a point that shares a
//     horizontal edge with pt AND whose diagonal partner
//     (right.X, below.Y) is an intersection that closes the rectangle
//     (shares the bottom edge with `below` and the right edge with
//     `right`). The first such rectangle is the cell; emit it and stop.
//   - At most one cell is emitted per anchor point.
//
// The reference re-derives `below`/`right` by scanning the entire
// remaining suffix for EVERY point — O(n) work per point, O(n^2)
// overall, and O(n^3) once the inner below x right search is counted.
// On a finely-ruled financial page (thousands of intersections) that
// is the multi-minute hang this package was built to avoid.
//
// We exploit the fact that intersections lie on a lattice: a set of
// unique X positions crossed by a set of unique Y positions. Indexing
// the points into that grid makes `below`/`right` O(1)-to-locate
// (they are simply the present cells in pt's column / row beyond pt)
// and the corner an O(1) lookup, so the whole pass is O(number of
// candidate cells) with small constants. The cell-SELECTION order is
// kept byte-for-byte identical to the reference (nearest-first
// outward walk, below-outer / right-inner, first-close-wins), so the
// emitted cell set is unchanged — only the cost of finding it drops.
func intersectionsToCells(intersections []Intersection) []BBox {
	if len(intersections) == 0 {
		return nil
	}

	g := newIntersectionGrid(intersections)

	// Visit anchor points in the same order pdfplumber does — by
	// sorted (X asc, Y asc-in-image-space) point key. The emitted cell
	// SET is independent of visit order (one cell per anchor, fully
	// determined by the anchor's own column/row), but we keep the
	// append order matching the reference so any order-sensitive
	// downstream consumer sees the same sequence. pdfplumber iterates
	// `sorted(intersections.keys())`; that is X ascending then
	// image-space top ascending, i.e. X ascending then user-space Y
	// DESCENDING. We reproduce it by walking columns left-to-right and,
	// within a column, rows top-to-bottom (Y descending).
	cells := make([]BBox, 0, len(intersections))
	for xi := 0; xi < len(g.xs); xi++ {
		col := g.colRows[xi] // present row indices, ascending (Y ascending)
		// Y descending → iterate the column's present rows from the
		// top (largest Y = largest row index) down.
		for ri := len(col) - 1; ri >= 0; ri-- {
			yi := col[ri]
			if c, ok := g.smallestCellAt(xi, yi); ok {
				cells = append(cells, c)
			}
		}
	}
	return cells
}

// intersectionGrid is the lattice index over a set of intersections.
// It holds the sorted unique X / Y coordinates, a presence index from
// (column, row) to the intersection's cached edge-key slices, and the
// per-column / per-row lists of present indices that drive the
// nearest-first outward walk.
type intersectionGrid struct {
	xs []float64 // sorted unique X coordinates
	ys []float64 // sorted unique Y coordinates (ascending)

	// nodes holds one gridNode per intersection, in the order the
	// intersections were indexed. Lookups go through dense/sparse below.
	nodes []gridNode

	// Presence index from packed (xi,yi) to nodes-index+1 (0 == absent).
	// Two representations, picked by buildup based on lattice density:
	//   - dense: a flat slice of length len(xs)*len(ys), used when that
	//     product stays within a sane multiple of the intersection count
	//     (the normal case — a real table is a near-full lattice).
	//   - sparse: a map keyed on the packed position, used when the
	//     bounding lattice would be far larger than the point count
	//     (scattered points that aren't a real grid), so we never
	//     allocate a giant matrix.
	dense  []int32
	sparse map[int64]int32
	stride int // == len(ys); used to pack (xi,yi) -> xi*stride + yi

	// colRows[xi] = sorted-ascending list of row indices present in
	// column xi. rowCols[yi] = sorted-ascending list of column indices
	// present in row yi. These let smallestCellAt walk only the points
	// that actually exist, nearest-first.
	colRows [][]int
	rowCols [][]int
}

// gridNode caches the deduplicated vertical / horizontal edge keys of
// one intersection so edge_connects checks are tiny slice scans, not
// map allocations.
type gridNode struct {
	v []edgeSharedKey
	h []edgeSharedKey
}

// denseGridLimit bounds the dense presence array: we only allocate the
// flat len(xs)*len(ys) matrix when it stays at or below this many
// entries. Beyond it (a sparse scatter that isn't a real grid) we fall
// back to the map so memory stays O(intersections). 4 million int32 is
// 16 MB — comfortably covers any legitimate ruled page (the dense
// 200x200 benchmark grid is only 40,401 entries) while capping the
// worst case.
const denseGridLimit = 4 << 20

// newIntersectionGrid snaps the intersections onto their lattice and
// builds the lookup structures. Coordinate equality uses the same
// exact-float comparison the rest of the finder relies on: snap_edges
// has already unified positions onto cluster means upstream, so equal
// coordinates are byte-equal here.
func newIntersectionGrid(intersections []Intersection) *intersectionGrid {
	xSet := make(map[float64]struct{}, len(intersections))
	ySet := make(map[float64]struct{}, len(intersections))
	for _, p := range intersections {
		xSet[p.X] = struct{}{}
		ySet[p.Y] = struct{}{}
	}
	xs := make([]float64, 0, len(xSet))
	for x := range xSet {
		xs = append(xs, x)
	}
	ys := make([]float64, 0, len(ySet))
	for y := range ySet {
		ys = append(ys, y)
	}
	sort.Float64s(xs)
	sort.Float64s(ys)

	xIndex := make(map[float64]int, len(xs))
	for i, x := range xs {
		xIndex[x] = i
	}
	yIndex := make(map[float64]int, len(ys))
	for i, y := range ys {
		yIndex[y] = i
	}

	g := &intersectionGrid{
		xs:      xs,
		ys:      ys,
		nodes:   make([]gridNode, 0, len(intersections)),
		stride:  len(ys),
		colRows: make([][]int, len(xs)),
		rowCols: make([][]int, len(ys)),
	}

	// Choose the presence representation. int64 math avoids overflow on
	// the product for pathological lattices before we decide.
	cells := int64(len(xs)) * int64(len(ys))
	if cells <= denseGridLimit {
		g.dense = make([]int32, cells)
	} else {
		g.sparse = make(map[int64]int32, len(intersections))
	}

	for _, p := range intersections {
		xi := xIndex[p.X]
		yi := yIndex[p.Y]
		g.nodes = append(g.nodes, gridNode{
			v: dedupEdgeKeys(p.V),
			h: dedupEdgeKeys(p.H),
		})
		ref := int32(len(g.nodes)) // node index + 1
		packed := int64(xi)*int64(g.stride) + int64(yi)
		if g.dense != nil {
			g.dense[packed] = ref
		} else {
			g.sparse[packed] = ref
		}
		g.colRows[xi] = append(g.colRows[xi], yi)
		g.rowCols[yi] = append(g.rowCols[yi], xi)
	}
	// Each column's rows and each row's columns sorted ascending so the
	// outward walk can go nearest-first.
	for xi := range g.colRows {
		sort.Ints(g.colRows[xi])
	}
	for yi := range g.rowCols {
		sort.Ints(g.rowCols[yi])
	}
	return g
}

// node returns the cached edge slices at lattice position (xi, yi), or
// nil if no intersection sits there.
func (g *intersectionGrid) node(xi, yi int) *gridNode {
	packed := int64(xi)*int64(g.stride) + int64(yi)
	var ref int32
	if g.dense != nil {
		ref = g.dense[packed]
	} else {
		ref = g.sparse[packed]
	}
	if ref == 0 {
		return nil
	}
	return &g.nodes[ref-1]
}

// smallestCellAt reproduces pdfplumber's find_smallest_cell for the
// anchor at lattice position (xi, yi): the nearest connected point
// below, paired with the nearest connected point to the right whose
// diagonal partner closes the rectangle. Returns the cell bbox and
// true, or false if the anchor opens no cell.
//
// The walk order is identical to the reference: `below` candidates are
// tried from nearest to farthest (outer loop); for each, `right`
// candidates are tried nearest to farthest (inner loop); the first
// quadruple that forms a closed, edge-connected rectangle wins. The
// only difference from the reference is HOW the candidate lists are
// obtained — here they are the present nodes in the anchor's column /
// row, located in O(1) via the grid index instead of by rescanning
// the whole intersection suffix.
func (g *intersectionGrid) smallestCellAt(xi, yi int) (BBox, bool) {
	anchor := g.node(xi, yi)
	if anchor == nil {
		return BBox{}, false
	}

	// `right` candidates are the nodes in this row to the right of the
	// anchor (larger X). rowCols holds column indices ascending, so the
	// suffix after the anchor's own position is already nearest-first.
	// We locate the anchor's slot once and reuse the suffix for every
	// `below` candidate rather than rescanning from zero.
	rowCols := g.rowCols[yi]
	rStart := upperBoundInt(rowCols, xi) // first index whose column > xi
	if rStart == len(rowCols) {
		return BBox{}, false // nothing to the right → no cell can close
	}
	rightCols := rowCols[rStart:]

	// `below` = nodes in this column with a smaller Y (visually below),
	// nearest first. colRows holds row indices ascending, so the rows
	// strictly below the anchor are the prefix before the anchor's own
	// slot. Locate that slot in O(log n) and walk the prefix downward
	// (nearest-below first) — we never touch the rows above the anchor.
	//
	// The loops then mirror pdfplumber exactly: nearest connecting
	// `below` (outer), then nearest connecting `right` whose diagonal
	// partner closes the rectangle (inner), first match wins. The inner
	// scan breaks out the moment a cell closes, so on a regular grid the
	// adjacent neighbours settle it in O(1) — only genuinely irregular
	// lattices walk further, exactly as the reference does.
	colRows := g.colRows[xi]
	bStart := sort.SearchInts(colRows, yi) // anchor's own index (yi is present)
	for bIdx := bStart - 1; bIdx >= 0; bIdx-- {
		byi := colRows[bIdx]
		bp := g.node(xi, byi)
		// pt and bp share column (same X) → need a shared vertical edge.
		if !sharesKey(anchor.v, bp.v) {
			continue
		}

		for _, rxi := range rightCols {
			rp := g.node(rxi, yi)
			// pt and rp share row (same Y) → need a shared horizontal edge.
			if !sharesKey(anchor.h, rp.h) {
				continue
			}
			// Diagonal partner: bottom-right corner at (rxi, byi).
			corner := g.node(rxi, byi)
			if corner == nil {
				continue
			}
			// corner & rp share column → vertical edge; corner & bp share
			// row → horizontal edge.
			if !sharesKey(corner.v, rp.v) {
				continue
			}
			if !sharesKey(corner.h, bp.h) {
				continue
			}

			// Cell bbox: x0 = anchor X, x1 = rp X, y0 = bp Y, y1 = anchor Y.
			return NewBBox(g.xs[xi], g.ys[byi], g.xs[rxi], g.ys[yi]), true
		}
	}
	return BBox{}, false
}

// upperBoundInt returns the index of the first element of the
// ascending-sorted slice s that is strictly greater than v, or len(s)
// if every element is <= v. Used to find the start of the "to the
// right of the anchor" column suffix in O(log n).
func upperBoundInt(s []int, v int) int {
	return sort.Search(len(s), func(i int) bool { return s[i] > v })
}

// cellsToTables is the Go port of pdfplumber's cells_to_tables. Given
// the raw cells from intersectionsToCells, group cells that share at
// least one corner into the same table. Standalone cells (those that
// don't touch any other cell on a corner) are dropped — a "table" of
// one cell is almost always a decorative box, not real tabular data.
//
// The implementation:
//   - Initialise a current table with the first remaining cell.
//   - In a pass over the remaining cells, append every cell that shares
//     at least one corner with the current table; remove appended
//     cells from the remaining list.
//   - Repeat until no more cells get appended in a pass; close the
//     current table and start a new one with the next remaining cell.
//   - Drop tables with fewer than 2 cells.
//
// Returns each table as a 1-D slice of its constituent cells; the
// caller (assembleTableBox) then projects the cells into the row /
// column grid.
func cellsToTables(cells []BBox) [][]BBox {
	if len(cells) == 0 {
		return nil
	}
	remaining := make([]BBox, len(cells))
	copy(remaining, cells)

	type corner struct {
		x, y float64
	}
	bboxCorners := func(b BBox) [4]corner {
		return [4]corner{
			{b.X0, b.Y1}, // top-left
			{b.X0, b.Y0}, // bottom-left
			{b.X1, b.Y1}, // top-right
			{b.X1, b.Y0}, // bottom-right
		}
	}

	var tables [][]BBox
	currentCells := make([]BBox, 0)
	currentCorners := make(map[corner]struct{})

	for len(remaining) > 0 {
		initialCount := len(currentCells)
		// One pass over remaining; collect newly assigned indices to
		// remove after the pass so we don't disturb the slice during
		// iteration.
		assigned := make([]int, 0, len(remaining))
		for i, cell := range remaining {
			cc := bboxCorners(cell)
			if len(currentCells) == 0 {
				for _, c := range cc {
					currentCorners[c] = struct{}{}
				}
				currentCells = append(currentCells, cell)
				assigned = append(assigned, i)
				continue
			}
			cornerCount := 0
			for _, c := range cc {
				if _, ok := currentCorners[c]; ok {
					cornerCount++
				}
			}
			if cornerCount > 0 {
				for _, c := range cc {
					currentCorners[c] = struct{}{}
				}
				currentCells = append(currentCells, cell)
				assigned = append(assigned, i)
			}
		}
		// Apply removals in reverse so indices stay valid.
		sort.Sort(sort.Reverse(sort.IntSlice(assigned)))
		for _, idx := range assigned {
			remaining = append(remaining[:idx], remaining[idx+1:]...)
		}

		if len(currentCells) == initialCount {
			// Nothing was added this pass — close the table and start
			// a fresh one with the next remaining cell.
			tables = append(tables, currentCells)
			currentCells = make([]BBox, 0)
			currentCorners = make(map[corner]struct{})
		}
	}
	if len(currentCells) > 0 {
		tables = append(tables, currentCells)
	}

	// Sort tables visually top-to-bottom, then left-to-right. We key
	// each table on its topmost-leftmost cell — pdfplumber uses
	// min((top, x0)) (image space); in user space that's the cell
	// with the LARGEST Y1 (visual top), tie-broken by smallest X0.
	sort.SliceStable(tables, func(i, j int) bool {
		ti := pickTopLeft(tables[i])
		tj := pickTopLeft(tables[j])
		if ti.Y1 != tj.Y1 {
			return ti.Y1 > tj.Y1
		}
		return ti.X0 < tj.X0
	})

	// Drop standalone-cell tables (pdfplumber: `len(t) > 1`).
	filtered := tables[:0]
	for _, t := range tables {
		if len(t) > 1 {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// pickTopLeft returns the cell that's visually topmost (largest Y1)
// and leftmost on ties (smallest X0).
func pickTopLeft(cells []BBox) BBox {
	best := cells[0]
	for _, c := range cells[1:] {
		if c.Y1 > best.Y1 || (c.Y1 == best.Y1 && c.X0 < best.X0) {
			best = c
		}
	}
	return best
}

// assembleTableBox projects a flat list of cells into a 2-D
// row/column grid. The algorithm collects the unique X0 values
// (column lefts) and Y1 values (row tops) across all cells, sorts
// them, and indexes each cell by (row from Y1, column from X0).
//
// Holes in the grid (a cell that should be at row i, col j but
// wasn't detected) remain as zero BBox entries — the caller can
// detect them with IsZero. We don't try to "fill" them by inferring
// boundaries; pdfplumber doesn't either.
func assembleTableBox(cells []BBox) TableBox {
	if len(cells) == 0 {
		return TableBox{}
	}

	// Collect unique row/column anchor positions. Row 0 is visually
	// topmost — largest Y1; column 0 is leftmost — smallest X0.
	xs := make(map[float64]struct{})
	ys := make(map[float64]struct{})
	for _, c := range cells {
		xs[c.X0] = struct{}{}
		ys[c.Y1] = struct{}{}
	}
	xList := make([]float64, 0, len(xs))
	for x := range xs {
		xList = append(xList, x)
	}
	yList := make([]float64, 0, len(ys))
	for y := range ys {
		yList = append(yList, y)
	}
	sort.Float64s(xList)
	sort.Slice(yList, func(i, j int) bool { return yList[i] > yList[j] })

	xIndex := make(map[float64]int, len(xList))
	for i, x := range xList {
		xIndex[x] = i
	}
	yIndex := make(map[float64]int, len(yList))
	for i, y := range yList {
		yIndex[y] = i
	}

	rows := len(yList)
	cols := len(xList)
	grid := make([][]BBox, rows)
	for i := range grid {
		grid[i] = make([]BBox, cols)
	}

	// Union bbox.
	bbox := cells[0]
	for _, c := range cells {
		ri, ok1 := yIndex[c.Y1]
		ci, ok2 := xIndex[c.X0]
		if ok1 && ok2 {
			grid[ri][ci] = c
		}
		bbox = bbox.Union(c)
	}

	return TableBox{
		BBox:      bbox,
		Rows:      rows,
		Cols:      cols,
		CellsGrid: grid,
	}
}

// runTableFinder is the geometry-only pipeline: given the page's
// edges (already merged + length-filtered by Page.findTableEdges) and
// the intersection / settings tolerances, build a TableFinder.
//
// This is the seam between page.go (which knows how to enumerate the
// page primitives) and the algorithms in this file. Splitting it out
// keeps the algorithm tests in table_test.go fast: they construct
// edges in-memory and call this function directly without ever
// opening a PDF.
func runTableFinder(edges []layout.Edge, xTol, yTol float64) TableFinder {
	intersections := edgesToIntersections(edges, xTol, yTol)
	cells := intersectionsToCells(intersections)
	groups := cellsToTables(cells)
	tables := make([]TableBox, 0, len(groups))
	for _, g := range groups {
		tables = append(tables, assembleTableBox(g))
	}
	return TableFinder{
		Edges:         edges,
		Intersections: intersections,
		Cells:         cells,
		Tables:        tables,
	}
}

// ensureSupportedStrategies validates that both axes' strategies are
// one of the four pdfplumber-defined values. As of v0.3.0 all four
// strategies (lines, lines_strict, text, explicit) are implemented;
// the function now exists only to reject unknown strategy strings.
func ensureSupportedStrategies(s TableSettings) error {
	for _, pair := range []struct {
		axis     string
		strategy TableStrategy
	}{
		{"vertical", s.VerticalStrategy},
		{"horizontal", s.HorizontalStrategy},
	} {
		switch pair.strategy {
		case StrategyLines, StrategyLinesStrict, StrategyText, StrategyExplicit, StrategyAuto:
			// ok
		default:
			return fmt.Errorf("%w: unknown %s_strategy %q", ErrUnsupported, pair.axis, pair.strategy)
		}
	}
	return nil
}

// errExplicitNeedsTwo is the error returned when the caller selects
// the "explicit" strategy on an axis but supplies fewer than two
// coordinates. pdfplumber raises ValueError with the same message.
func errExplicitNeedsTwo(axis string) error {
	return fmt.Errorf("pdfgrab: %s_strategy=%q requires at least two coordinates in Explicit%sLines",
		axis, StrategyExplicit, axisFieldName(axis))
}

// axisFieldName returns the field-name suffix for the axis ("Vertical"
// or "Horizontal") so error messages reference the actual struct field
// the caller would need to populate.
func axisFieldName(axis string) string {
	if axis == "vertical" {
		return "Vertical"
	}
	return "Horizontal"
}
