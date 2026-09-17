// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

// Package layout owns the lower-level geometry primitives that drive
// table-finding: edges, edge-derivation from Lines/Rects/Curves, and
// edge merging (snap + join).
//
// The split between this internal package and the public pdfgrab
// package mirrors the pdfplumber split between
// pdfplumber/utils/geometry.py (edge maths) and pdfplumber/table.py
// (the TableFinder). Keeping the edge maths here lets us evolve the
// representation freely while the public surface in pdfgrab's
// table.go / finder.go stays stable.
//
// Coordinate system: PDF user space — origin at bottom-left, Y growing
// UP. An edge is a single-axis line segment:
//
//   - Horizontal edge: Y0 == Y1, X0 <= X1. Orientation "h".
//   - Vertical edge:   X0 == X1, Y0 <= Y1. Orientation "v".
//
// (Pdfplumber operates in IMAGE space, Y growing down; its "top" is
// our larger Y, its "bottom" is our smaller Y. The algorithms here
// are the same, only the coordinate sign is flipped — see comments at
// each step for the explicit mapping.)
package layout

import (
	"math"
	"sort"
)

// Orientation is the axis an edge lies along. "h" = horizontal,
// "v" = vertical. Diagonal lines never become edges; they're dropped
// at derivation time.
type Orientation string

const (
	Horizontal Orientation = "h"
	Vertical   Orientation = "v"
)

// Source tags say which kind of drawn primitive produced an edge.
// pdfplumber distinguishes between "line", "rect_edge", and
// "curve_edge" so that lines_strict mode can ignore everything that
// isn't a literal stroked line. We carry the same distinction.
type Source uint8

const (
	// SourceLine: an edge derived from a stroked Line.
	SourceLine Source = iota
	// SourceRect: an edge derived from one side of a Rect.
	SourceRect
	// SourceCurve: an edge derived from a Curve's straight segment.
	// We accept curve edges in "lines" mode but not "lines_strict".
	SourceCurve
	// SourceExplicit: an edge constructed from an
	// ExplicitVerticalLines / ExplicitHorizontalLines setting.
	SourceExplicit
	// SourceText: an edge inferred from word alignment by the "text"
	// strategy. words_to_edges_v / words_to_edges_h in pdfplumber.
	SourceText
)

// Edge is one axis-aligned line segment carrying the data the table-
// finder needs for snap + join + intersection.
//
// Invariants (constructor-enforced via newEdge / normalise):
//
//   - Orientation == "h" → Y0 == Y1, X0 <= X1.
//   - Orientation == "v" → X0 == X1, Y0 <= Y1.
//
// Length() returns the along-direction extent; Pos() returns the
// perpendicular position (the "snap axis" for merging).
type Edge struct {
	X0, Y0, X1, Y1 float64
	Orientation    Orientation
	Source         Source
	// Width is the stroke width of the originating primitive (used by
	// some callers to filter hair-thin construction lines). Zero is
	// "unknown".
	Width float64
}

// Length returns the edge's extent along its orientation axis.
//
//   - Horizontal edge → X1 - X0.
//   - Vertical edge   → Y1 - Y0.
func (e Edge) Length() float64 {
	if e.Orientation == Horizontal {
		return e.X1 - e.X0
	}
	return e.Y1 - e.Y0
}

// Pos returns the perpendicular position of the edge — the coordinate
// that's constant along its length. We use this as the snap-axis key
// for grouping edges that lie on the same infinite line.
//
//   - Horizontal edge → Y0 (which equals Y1).
//   - Vertical edge   → X0 (which equals X1).
func (e Edge) Pos() float64 {
	if e.Orientation == Horizontal {
		return e.Y0
	}
	return e.X0
}

// normalise enforces the invariants. We never construct an Edge
// directly; callers go through one of the New* / FromLine / FromRect
// helpers which always produce a normalised result.
func (e Edge) normalise() Edge {
	if e.Orientation == Horizontal {
		// Ensure Y0 == Y1 (use the average if a caller passed a near-
		// horizontal edge; the table-finding code only ever sees fully
		// horizontal edges so this branch is a guardrail).
		y := (e.Y0 + e.Y1) / 2
		e.Y0 = y
		e.Y1 = y
		if e.X1 < e.X0 {
			e.X0, e.X1 = e.X1, e.X0
		}
		return e
	}
	// Vertical.
	x := (e.X0 + e.X1) / 2
	e.X0 = x
	e.X1 = x
	if e.Y1 < e.Y0 {
		e.Y0, e.Y1 = e.Y1, e.Y0
	}
	return e
}

// LineSegment is a minimal struct describing a drawn straight-line
// segment. It exists so this package doesn't have to import the
// public pdfgrab types — keeps the dependency direction one-way
// (pdfgrab depends on layout, not the other way round).
type LineSegment struct {
	X0, Y0, X1, Y1 float64
	Width          float64
}

// RectSegment is a minimal Rect descriptor for the same reason. Only
// the bbox matters for edge derivation; we drop the Stroke/Fill flags
// at the layer above by filtering out non-stroked rects before
// calling FromRect.
type RectSegment struct {
	X0, Y0, X1, Y1 float64
	Width          float64
}

// CurveSegment is the point list of a curve path. We turn each
// horizontal or vertical pair of consecutive points into an edge —
// curves that are entirely diagonal contribute zero edges.
type CurveSegment struct {
	Points [][2]float64
	Width  float64
}

// FromLine returns one Edge for a horizontal or vertical line
// segment, or (zero, false) for diagonal lines (they aren't axis-
// aligned and so can't be table rules).
//
// Tolerance is the same near-axis-aligned slack pdfplumber's
// line_to_edge predicate uses implicitly: a "horizontal" line is one
// whose Y0 and Y1 are equal. We treat them as equal when their
// difference is below the supplied tolerance to absorb floating-
// point drift from the content-stream interpreter.
func FromLine(l LineSegment, tolerance float64) (Edge, bool) {
	dx := math.Abs(l.X1 - l.X0)
	dy := math.Abs(l.Y1 - l.Y0)
	switch {
	case dy <= tolerance && dx > tolerance:
		// Horizontal.
		e := Edge{
			X0: l.X0, X1: l.X1,
			Y0: l.Y0, Y1: l.Y1,
			Orientation: Horizontal,
			Source:      SourceLine,
			Width:       l.Width,
		}
		return e.normalise(), true
	case dx <= tolerance && dy > tolerance:
		// Vertical.
		e := Edge{
			X0: l.X0, X1: l.X1,
			Y0: l.Y0, Y1: l.Y1,
			Orientation: Vertical,
			Source:      SourceLine,
			Width:       l.Width,
		}
		return e.normalise(), true
	}
	return Edge{}, false
}

// FromRect returns the four edges of a rectangle's outline. We mirror
// pdfplumber's rect_to_edges: top, bottom, left, right — all tagged
// SourceRect. Filled-only (non-stroked) rectangles still produce
// edges because pdfplumber's edges property aggregates BOTH stroked
// and filled rects (a filled cell-background still defines a row
// boundary).
func FromRect(r RectSegment) []Edge {
	bottom := Edge{
		X0: r.X0, X1: r.X1,
		Y0: r.Y0, Y1: r.Y0,
		Orientation: Horizontal,
		Source:      SourceRect,
		Width:       r.Width,
	}
	top := Edge{
		X0: r.X0, X1: r.X1,
		Y0: r.Y1, Y1: r.Y1,
		Orientation: Horizontal,
		Source:      SourceRect,
		Width:       r.Width,
	}
	left := Edge{
		X0: r.X0, X1: r.X0,
		Y0: r.Y0, Y1: r.Y1,
		Orientation: Vertical,
		Source:      SourceRect,
		Width:       r.Width,
	}
	right := Edge{
		X0: r.X1, X1: r.X1,
		Y0: r.Y0, Y1: r.Y1,
		Orientation: Vertical,
		Source:      SourceRect,
		Width:       r.Width,
	}
	return []Edge{
		top.normalise(),
		bottom.normalise(),
		left.normalise(),
		right.normalise(),
	}
}

// FromCurve returns one edge per consecutive pair of points that lies
// on the same axis. Diagonal pairs are dropped. pdfplumber does the
// same in curve_to_edges.
func FromCurve(c CurveSegment, tolerance float64) []Edge {
	if len(c.Points) < 2 {
		return nil
	}
	out := make([]Edge, 0, len(c.Points)-1)
	for i := 1; i < len(c.Points); i++ {
		p0 := c.Points[i-1]
		p1 := c.Points[i]
		dx := math.Abs(p1[0] - p0[0])
		dy := math.Abs(p1[1] - p0[1])
		switch {
		case dy <= tolerance && dx > tolerance:
			e := Edge{
				X0: p0[0], X1: p1[0],
				Y0: p0[1], Y1: p1[1],
				Orientation: Horizontal,
				Source:      SourceCurve,
				Width:       c.Width,
			}
			out = append(out, e.normalise())
		case dx <= tolerance && dy > tolerance:
			e := Edge{
				X0: p0[0], X1: p1[0],
				Y0: p0[1], Y1: p1[1],
				Orientation: Vertical,
				Source:      SourceCurve,
				Width:       c.Width,
			}
			out = append(out, e.normalise())
		}
	}
	return out
}

// SnapEdges replaces near-collinear edges with edges sharing the
// average perpendicular position. Horizontal edges within
// snapYTolerance of each other on the Y axis get unified onto their
// mean Y; vertical edges within snapXTolerance of each other on the
// X axis get unified onto their mean X.
//
// This is the Go port of pdfplumber's table.snap_edges, which
// dispatches into utils.snap_objects per orientation.
//
// A tolerance of 0 leaves the edges unchanged for that orientation.
func SnapEdges(edges []Edge, snapXTolerance, snapYTolerance float64) []Edge {
	if len(edges) == 0 {
		return nil
	}
	// Partition.
	var vEdges, hEdges []Edge
	for _, e := range edges {
		if e.Orientation == Horizontal {
			hEdges = append(hEdges, e)
		} else {
			vEdges = append(vEdges, e)
		}
	}

	snappedV := snapAlongAxis(vEdges, snapXTolerance, true)
	snappedH := snapAlongAxis(hEdges, snapYTolerance, false)

	out := make([]Edge, 0, len(snappedV)+len(snappedH))
	out = append(out, snappedV...)
	out = append(out, snappedH...)
	return out
}

// snapAlongAxis groups edges by their perpendicular position
// (vertical=true → group by X0; vertical=false → group by Y0), forms
// clusters where consecutive sorted positions differ by <= tolerance,
// and replaces each edge's position with the cluster mean.
//
// Tolerance <= 0 returns edges unchanged.
func snapAlongAxis(edges []Edge, tolerance float64, vertical bool) []Edge {
	if len(edges) == 0 || tolerance <= 0 {
		return edges
	}
	// Indices sorted by perpendicular position.
	type item struct {
		idx int
		pos float64
	}
	items := make([]item, len(edges))
	for i, e := range edges {
		items[i] = item{idx: i, pos: e.Pos()}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].pos < items[j].pos
	})

	// Cluster: greedy single-pass over the sorted positions, opening
	// a new cluster every time the gap to the previous position
	// exceeds tolerance. Pdfplumber's cluster_objects uses the same
	// single-pass with cluster_list semantics — adjacent items within
	// tolerance form one cluster.
	clusters := make([][]int, 0)
	current := []int{items[0].idx}
	last := items[0].pos
	for _, it := range items[1:] {
		if it.pos-last <= tolerance {
			current = append(current, it.idx)
		} else {
			clusters = append(clusters, current)
			current = []int{it.idx}
		}
		last = it.pos
	}
	clusters = append(clusters, current)

	// Per-cluster mean and snap.
	out := make([]Edge, len(edges))
	copy(out, edges)
	for _, c := range clusters {
		var sum float64
		for _, i := range c {
			sum += edges[i].Pos()
		}
		mean := sum / float64(len(c))
		for _, i := range c {
			if vertical {
				out[i].X0 = mean
				out[i].X1 = mean
			} else {
				out[i].Y0 = mean
				out[i].Y1 = mean
			}
		}
	}
	return out
}

// JoinEdges merges collinear edges whose along-direction extents
// touch (within joinTolerance). Two horizontal edges with the same
// Y and overlapping or near-touching X ranges become one edge
// spanning their union; similarly for vertical edges.
//
// Edges that don't overlap and aren't within the join tolerance pass
// through unchanged.
//
// This is the Go port of pdfplumber's table.join_edge_group, called
// once per (orientation, perpendicular-position) group.
func JoinEdges(edges []Edge, joinXTolerance, joinYTolerance float64) []Edge {
	if len(edges) == 0 {
		return nil
	}
	// Group by (orientation, perpendicular position). Pdfplumber does
	// itertools.groupby over a sorted iterable; we do the same.
	type groupKey struct {
		o Orientation
		p float64
	}
	sorted := make([]Edge, len(edges))
	copy(sorted, edges)
	sort.SliceStable(sorted, func(i, j int) bool {
		gi := groupKey{o: sorted[i].Orientation, p: sorted[i].Pos()}
		gj := groupKey{o: sorted[j].Orientation, p: sorted[j].Pos()}
		if gi.o != gj.o {
			return gi.o < gj.o
		}
		if gi.p != gj.p {
			return gi.p < gj.p
		}
		// Within a group sort by along-direction start, so the join
		// scan can walk the line left-to-right or bottom-to-top.
		if sorted[i].Orientation == Horizontal {
			return sorted[i].X0 < sorted[j].X0
		}
		return sorted[i].Y0 < sorted[j].Y0
	})

	var out []Edge
	i := 0
	for i < len(sorted) {
		j := i + 1
		k := groupKey{o: sorted[i].Orientation, p: sorted[i].Pos()}
		for j < len(sorted) {
			kj := groupKey{o: sorted[j].Orientation, p: sorted[j].Pos()}
			if kj != k {
				break
			}
			j++
		}
		group := sorted[i:j]
		tol := joinXTolerance
		if k.o == Vertical {
			tol = joinYTolerance
		}
		out = append(out, joinGroup(group, tol)...)
		i = j
	}
	return out
}

// joinGroup expects all edges in `group` to share the same
// orientation AND perpendicular position. It walks along the along-
// direction sorted edges, merging adjacent edges whose gap is <=
// tolerance.
func joinGroup(group []Edge, tolerance float64) []Edge {
	if len(group) == 0 {
		return nil
	}
	joined := []Edge{group[0]}
	if group[0].Orientation == Horizontal {
		for _, e := range group[1:] {
			last := &joined[len(joined)-1]
			if e.X0 <= last.X1+tolerance {
				if e.X1 > last.X1 {
					last.X1 = e.X1
				}
			} else {
				joined = append(joined, e)
			}
		}
	} else {
		for _, e := range group[1:] {
			last := &joined[len(joined)-1]
			if e.Y0 <= last.Y1+tolerance {
				if e.Y1 > last.Y1 {
					last.Y1 = e.Y1
				}
			} else {
				joined = append(joined, e)
			}
		}
	}
	return joined
}

// MergeEdges is the snap-then-join pipeline. It's the entry point
// that table.go calls; it mirrors pdfplumber's table.merge_edges.
//
// Order:
//  1. Snap (collapse near-collinear edges onto their mean position).
//  2. Group by (orientation, position) and join within each group.
func MergeEdges(edges []Edge, snapXTol, snapYTol, joinXTol, joinYTol float64) []Edge {
	if len(edges) == 0 {
		return nil
	}
	snapped := edges
	if snapXTol > 0 || snapYTol > 0 {
		snapped = SnapEdges(edges, snapXTol, snapYTol)
	}
	return JoinEdges(snapped, joinXTol, joinYTol)
}

// FilterEdgesByLength drops edges whose along-direction length is
// below minLength. Pdfplumber calls this twice in the TableFinder
// pipeline: once with edge_min_length_prefilter on the raw edges
// (default 1 — drop hairline construction lines) and once with
// edge_min_length on the merged set (default 3 — drop short stubs
// after snap+join).
func FilterEdgesByLength(edges []Edge, minLength float64) []Edge {
	if minLength <= 0 {
		return edges
	}
	out := edges[:0:0]
	for _, e := range edges {
		if e.Length() >= minLength {
			out = append(out, e)
		}
	}
	return out
}

// FilterEdgesBySource keeps only edges produced by an allowed Source.
// Used by lines_strict mode to drop rect and curve edges.
func FilterEdgesBySource(edges []Edge, allow ...Source) []Edge {
	if len(allow) == 0 {
		return edges
	}
	allowed := make(map[Source]struct{}, len(allow))
	for _, s := range allow {
		allowed[s] = struct{}{}
	}
	out := edges[:0:0]
	for _, e := range edges {
		if _, ok := allowed[e.Source]; ok {
			out = append(out, e)
		}
	}
	return out
}

// FilterEdgesByOrientation keeps only edges with the given
// orientation. Convenience over a one-line loop, used by the
// TableFinder when separating the v / h edge lists post-merge for
// intersection detection.
func FilterEdgesByOrientation(edges []Edge, o Orientation) []Edge {
	out := edges[:0:0]
	for _, e := range edges {
		if e.Orientation == o {
			out = append(out, e)
		}
	}
	return out
}

// SortEdges returns a stable-sorted copy of edges keyed by
// (orientation, perpendicular position, along-direction start). This
// is the deterministic order downstream stages rely on for
// intersection enumeration.
func SortEdges(edges []Edge) []Edge {
	out := make([]Edge, len(edges))
	copy(out, edges)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Orientation != out[j].Orientation {
			return out[i].Orientation < out[j].Orientation
		}
		if pa, pb := out[i].Pos(), out[j].Pos(); pa != pb {
			return pa < pb
		}
		if out[i].Orientation == Horizontal {
			return out[i].X0 < out[j].X0
		}
		return out[i].Y0 < out[j].Y0
	})
	return out
}
