// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

// finder_text.go implements the "text" and "explicit" edge-derivation
// strategies that complement the "lines" / "lines_strict" strategies in
// finder.go.
//
// The "text" strategy is a direct port of pdfplumber's
// words_to_edges_v / words_to_edges_h (pdfplumber/table.py lines
// 101-204). The pipeline:
//
//   - vertical: cluster words by X0 (left), X1 (right), and center
//     position with tolerance=1 PDF point; pick clusters with at least
//     MinWordsVertical members; deduplicate overlapping clusters by
//     bbox-overlap; emit one vertical edge per cluster's X0, plus one
//     trailing edge at the right-most X1.
//   - horizontal: cluster words by their top Y (Y1 in user space) with
//     tolerance=1; pick clusters with at least MinWordsHorizontal
//     members; emit BOTH the top and bottom edges of each cluster so
//     the last row of the table is captured.
//
// The "explicit" strategy is a direct port of pdfplumber's handling of
// explicit_vertical_lines / explicit_horizontal_lines as standalone
// edge lists (table.py lines 623-695). The caller passes float
// coordinates; we promote each to a full-extent edge spanning the
// page's bbox.
//
// Coordinate system: pdfplumber works in image space (Y growing down,
// "top" = smaller Y). pdfgrab uses PDF user space (Y growing up). Two
// translations matter:
//
//   - pdfplumber's "top" is our Y1 (the visually-upper edge of a word's
//     bbox in user space).
//   - pdfplumber's "bottom" is our Y0.
//
// The clustering and threshold logic is the same in both coordinate
// systems; only the field names flip.

import (
	"log"
	"math"
	"sort"

	"github.com/hallelx2/pdfgrab/internal/layout"
)

// wordEdgeTolerance is the per-axis tolerance used when clustering
// words for the "text" strategy. pdfplumber hardcodes this to 1 PDF
// point in words_to_edges_v / words_to_edges_h (the third argument to
// cluster_objects); we mirror that.
const wordEdgeTolerance = 1.0

// wordsToEdgesV is the Go port of pdfplumber's words_to_edges_v
// (table.py lines 144-204). Given a slice of words and a
// MinWordsVertical threshold, infer vertical edges by clustering the
// words' X0 (left), X1 (right), and centre positions, picking
// clusters whose membership is >= threshold, dropping overlapping
// clusters (the larger one wins), and emitting one edge at each
// cluster's leftmost X plus a trailing edge at the rightmost X1.
//
// pdfplumber sorts the clusters by descending size before the overlap
// dedupe so that, when two candidate column boundaries overlap, the
// bigger one (the one supported by more words) wins.
func wordsToEdgesV(words []Word, wordThreshold int) []layout.Edge {
	if len(words) == 0 || wordThreshold <= 0 {
		return nil
	}

	// Three candidate cluster sources: words sharing left edge (X0),
	// right edge (X1), centre. Each contributes one cluster set; we
	// concatenate all three, drop those below threshold, then dedupe
	// by overlap (larger cluster wins).
	byX0 := clusterObjects(words, func(w Word) float64 { return w.X0 }, wordEdgeTolerance, false)
	byX1 := clusterObjects(words, func(w Word) float64 { return w.X1 }, wordEdgeTolerance, false)
	byCenter := clusterObjects(words, func(w Word) float64 { return (w.X0 + w.X1) / 2 }, wordEdgeTolerance, false)

	all := make([][]Word, 0, len(byX0)+len(byX1)+len(byCenter))
	all = append(all, byX0...)
	all = append(all, byX1...)
	all = append(all, byCenter...)

	// Sort by descending size so the dedupe pass keeps the largest
	// cluster when two overlap.
	sort.SliceStable(all, func(i, j int) bool {
		return len(all[i]) > len(all[j])
	})

	// Filter below-threshold clusters.
	large := make([][]Word, 0, len(all))
	for _, c := range all {
		if len(c) >= wordThreshold {
			large = append(large, c)
		}
	}
	if len(large) == 0 {
		return nil
	}

	// Convert each surviving cluster to its bbox.
	bboxes := make([]BBox, len(large))
	for i, c := range large {
		bboxes[i] = wordsBBox(c)
	}

	// Dedupe: keep clusters whose bbox doesn't overlap any already-
	// kept one. Pdfplumber uses get_bbox_overlap which is non-empty
	// only when there's positive overlap area — touching boxes don't
	// count.
	condensed := make([]BBox, 0, len(bboxes))
	for _, b := range bboxes {
		overlap := false
		for _, c := range condensed {
			if _, ok := b.Intersect(c); ok {
				overlap = true
				break
			}
		}
		if !overlap {
			condensed = append(condensed, b)
		}
	}
	if len(condensed) == 0 {
		return nil
	}

	// Sort the surviving clusters left-to-right by X0.
	sort.SliceStable(condensed, func(i, j int) bool {
		return condensed[i].X0 < condensed[j].X0
	})

	// pdfplumber emits, for each cluster, an edge at its LEFT (X0);
	// then one final trailing edge at the right edge of the right-most
	// cluster (max X1). The Y span on each edge is the union of all
	// participating clusters' Y range so that downstream merging /
	// intersection sees consistent spans.
	minY0 := math.Inf(1)
	maxY1 := math.Inf(-1)
	maxX1 := math.Inf(-1)
	for _, b := range condensed {
		if b.Y0 < minY0 {
			minY0 = b.Y0
		}
		if b.Y1 > maxY1 {
			maxY1 = b.Y1
		}
		if b.X1 > maxX1 {
			maxX1 = b.X1
		}
	}

	out := make([]layout.Edge, 0, len(condensed)+1)
	for _, b := range condensed {
		out = append(out, layout.Edge{
			X0: b.X0, X1: b.X0,
			Y0: minY0, Y1: maxY1,
			Orientation: layout.Vertical,
			Source:      layout.SourceText,
		})
	}
	out = append(out, layout.Edge{
		X0: maxX1, X1: maxX1,
		Y0: minY0, Y1: maxY1,
		Orientation: layout.Vertical,
		Source:      layout.SourceText,
	})
	return out
}

// wordsToEdgesH is the Go port of pdfplumber's words_to_edges_h
// (table.py lines 101-141). Given a slice of words and a
// MinWordsHorizontal threshold, infer horizontal edges by clustering
// the words by their visual TOP (Y1 in PDF user space, "top" in
// pdfplumber's image-space dict) and emitting one edge per cluster at
// its Y1, plus a second edge at the cluster's Y0 so the bottom of the
// last row gets captured.
//
// pdfplumber notes (table.py lines 128-130): "For each detected row,
// we also add the 'bottom' line. This will generate extra edges,
// (some will be redundant with the next row 'top' line), but this
// catches the last row of every table."
func wordsToEdgesH(words []Word, wordThreshold int) []layout.Edge {
	if len(words) == 0 || wordThreshold <= 0 {
		return nil
	}

	// Cluster by visual top (Y1 in user space). pdfplumber uses
	// "top" — which is image-space and so corresponds to user-space
	// Y1 (the upper edge of the bbox).
	byTop := clusterObjects(words, func(w Word) float64 { return w.Y1 }, wordEdgeTolerance, false)

	large := make([][]Word, 0, len(byTop))
	for _, c := range byTop {
		if len(c) >= wordThreshold {
			large = append(large, c)
		}
	}
	if len(large) == 0 {
		return nil
	}

	// Per-cluster bbox: bottom = min(Y0), top = max(Y1) — i.e. the
	// union of every word's vertical extent in the cluster.
	bboxes := make([]BBox, len(large))
	for i, c := range large {
		bboxes[i] = wordsBBox(c)
	}

	// Span the resulting edges from min(X0) to max(X1) across ALL
	// clusters — pdfplumber emits horizontal edges as page-spanning
	// rules across every detected row.
	minX0 := math.Inf(1)
	maxX1 := math.Inf(-1)
	for _, b := range bboxes {
		if b.X0 < minX0 {
			minX0 = b.X0
		}
		if b.X1 > maxX1 {
			maxX1 = b.X1
		}
	}

	out := make([]layout.Edge, 0, 2*len(bboxes))
	for _, b := range bboxes {
		// Top edge (Y1 in user space, "top" in image space).
		out = append(out, layout.Edge{
			X0: minX0, X1: maxX1,
			Y0: b.Y1, Y1: b.Y1,
			Orientation: layout.Horizontal,
			Source:      layout.SourceText,
		})
		// Bottom edge (Y0 in user space, "bottom" in image space).
		out = append(out, layout.Edge{
			X0: minX0, X1: maxX1,
			Y0: b.Y0, Y1: b.Y0,
			Orientation: layout.Horizontal,
			Source:      layout.SourceText,
		})
	}
	return out
}

// wordsBBox returns the union bbox of every word in cs. Used by the
// text-strategy to derive a cluster's spatial extent.
func wordsBBox(cs []Word) BBox {
	if len(cs) == 0 {
		return BBox{}
	}
	out := BBox{X0: cs[0].X0, Y0: cs[0].Y0, X1: cs[0].X1, Y1: cs[0].Y1}
	for _, w := range cs[1:] {
		if w.X0 < out.X0 {
			out.X0 = w.X0
		}
		if w.Y0 < out.Y0 {
			out.Y0 = w.Y0
		}
		if w.X1 > out.X1 {
			out.X1 = w.X1
		}
		if w.Y1 > out.Y1 {
			out.Y1 = w.Y1
		}
	}
	return out
}

// explicitVerticalEdges promotes a slice of float X coordinates into
// vertical edges that span the page's Y range. Each non-finite or
// zero-length entry is dropped with a warning so a misconfigured
// caller gets visible feedback rather than silent empty results.
//
// pageY0 / pageY1 are the page's vertical bounds (typically 0 and
// page.Height()) — pdfplumber's `page.bbox[1]` and `page.bbox[3]`.
func explicitVerticalEdges(xs []float64, pageY0, pageY1 float64) []layout.Edge {
	if len(xs) == 0 {
		return nil
	}
	out := make([]layout.Edge, 0, len(xs))
	for _, x := range xs {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			log.Printf("pdfgrab: explicit vertical line %v ignored (non-finite)", x)
			continue
		}
		if pageY1 <= pageY0 {
			log.Printf("pdfgrab: explicit vertical line %v ignored (page height is zero)", x)
			continue
		}
		out = append(out, layout.Edge{
			X0: x, X1: x,
			Y0: pageY0, Y1: pageY1,
			Orientation: layout.Vertical,
			Source:      layout.SourceExplicit,
		})
	}
	return out
}

// explicitHorizontalEdges promotes a slice of float Y coordinates into
// horizontal edges that span the page's X range. Same invalid-input
// behaviour as explicitVerticalEdges.
//
// pageX0 / pageX1 are the page's horizontal bounds.
func explicitHorizontalEdges(ys []float64, pageX0, pageX1 float64) []layout.Edge {
	if len(ys) == 0 {
		return nil
	}
	out := make([]layout.Edge, 0, len(ys))
	for _, y := range ys {
		if math.IsNaN(y) || math.IsInf(y, 0) {
			log.Printf("pdfgrab: explicit horizontal line %v ignored (non-finite)", y)
			continue
		}
		if pageX1 <= pageX0 {
			log.Printf("pdfgrab: explicit horizontal line %v ignored (page width is zero)", y)
			continue
		}
		out = append(out, layout.Edge{
			X0: pageX0, X1: pageX1,
			Y0: y, Y1: y,
			Orientation: layout.Horizontal,
			Source:      layout.SourceExplicit,
		})
	}
	return out
}

// validateExplicitForStrategy reports an error if either axis uses
// the "explicit" strategy but the caller supplied fewer than two
// coordinates on that axis. pdfplumber raises ValueError in this
// case (table.py lines 605-615). The check is a no-op for the
// other three strategies.
//
// Coordinates that survive the validation aren't checked for finite-
// ness here — that happens in explicit*Edges below, where invalid
// entries are logged and skipped individually.
func validateExplicitForStrategy(s TableSettings) error {
	if s.VerticalStrategy == StrategyExplicit && len(s.ExplicitVerticalLines) < 2 {
		return errExplicitNeedsTwo("vertical")
	}
	if s.HorizontalStrategy == StrategyExplicit && len(s.ExplicitHorizontalLines) < 2 {
		return errExplicitNeedsTwo("horizontal")
	}
	return nil
}
