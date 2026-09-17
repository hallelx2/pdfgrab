// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

// detect_textedge.go implements table-region detection from text
// alignment, following the approach in Anssi Nurminen's master's thesis
// ("Algorithmic Extraction of Data in Tables in PDF Documents", Tampere
// University of Technology, 2013). The method won the table-structure
// track of the ICDAR 2013 competition and is the detector behind
// camelot's "stream" flavour; this is an independent implementation of
// the published algorithm, not a translation of that code.
//
// # Why this exists
//
// Everything else in this package derives edges across the WHOLE PAGE
// and then looks for cells. That works when the page is ruled: the
// rulings themselves say where the table is. It fails in two opposite
// ways otherwise.
//
// The "lines" strategy needs INTERSECTING rulings to form a cell, so a
// booktabs-style table — horizontal rules only, no verticals — produces
// no intersections and is invisible. Measured on ICDAR 2013, 22% of
// documents yield no table at all for exactly this reason.
//
// The "text" strategy has the opposite failure. It infers column
// boundaries from word positions with no notion of where a table is, so
// it finds a grid on any page, including continuous prose. Its
// precision on the same corpus is 0.188.
//
// # The idea
//
// A table is not primarily a thing with lines around it. It is a region
// where text is VERTICALLY ALIGNED over several consecutive rows.
// Prose does not do that: a paragraph's left margin is shared by every
// line on the page, but its interior word positions wander.
//
// So: find x-coordinates where several consecutive text lines start (or
// end, or centre) at the same place, and the extent of those alignments
// is the table. Detection becomes a property of the text itself, which
// means it works on unruled tables, and it refuses prose because prose
// does not sustain the alignment.
//
// # The pipeline
//
//  1. Group words into text lines (a table row is a line, not a word).
//  2. For every line, register its left, centre and right x as a
//     candidate alignment.
//  3. A line joins an existing alignment when its x matches within
//     coordTol AND it is vertically adjacent — within edgeTol of the
//     alignment's running bottom. Adjacency is what separates a real
//     column from a coincidence of x across unrelated parts of a page.
//  4. An alignment becomes valid once it spans minLines lines.
//  5. Keep only ONE of {left, centre, right} — whichever accumulated the
//     most lines. A page is dominantly one or the other, and scoring all
//     three together is what generates noise.
//  6. The surviving alignments' extent, grown to cover overlapping
//     lines and padded by a line height, is the table region.
//
// Step 4 is the part that does the real work. It is why prose is
// rejected and why this can raise recall without destroying precision.

import (
	"math"
	"sort"
)

// TextEdgeOpts tunes region detection. The zero value is not usable;
// call DefaultTextEdgeOpts.
type TextEdgeOpts struct {
	// MinLines is how many vertically-adjacent lines must share an
	// alignment before it counts as evidence of a column.
	//
	// This is the precision knob, and the whole method turns on it.
	// Lower it and prose starts qualifying; raise it and small tables
	// disappear. Four is the published value and what camelot ships.
	MinLines int

	// EdgeTol is the vertical gap, in points, that an alignment may
	// span between one line and the next and still be considered
	// continuous.
	//
	// It is generous (50pt ≈ 4 lines of body text) on purpose: a table
	// row can be tall, and a column must survive a blank cell without
	// being cut in two.
	EdgeTol float64

	// CoordTol is how close two x-coordinates must be to count as the
	// same alignment. Sub-point, because a real column shares an exact
	// x — this is for floating-point noise, not for tolerance of
	// sloppy layout.
	CoordTol float64

	// MinTextLen ignores lines with fewer than this many non-space
	// characters. A lone digit or bullet aligns with anything and is
	// evidence of nothing.
	MinTextLen int

	// LineTol is the vertical tolerance for grouping words into a line.
	LineTol float64
}

// DefaultTextEdgeOpts returns the published parameters.
func DefaultTextEdgeOpts() TextEdgeOpts {
	return TextEdgeOpts{
		MinLines:   4,
		EdgeTol:    50,
		CoordTol:   0.5,
		MinTextLen: 2,
		LineTol:    2,
	}
}

// textAlign identifies which end of a line an alignment tracks.
type textAlign int

const (
	alignLeft textAlign = iota
	alignCenter
	alignRight
	numAligns
)

// textLine is a horizontal run of words sharing a baseline.
type textLine struct {
	X0, Y0, X1, Y1 float64
	runes          int
}

func (l textLine) coordFor(a textAlign) float64 {
	switch a {
	case alignLeft:
		return l.X0
	case alignRight:
		return l.X1
	default:
		return (l.X0 + l.X1) / 2
	}
}

// textEdge is a run of lines sharing an x-coordinate, contiguous in y.
//
// It is a vertical segment, not a point: y1 is where the alignment
// started (the topmost line) and y0 where it currently ends. Growth is
// downward because lines are visited in reading order.
type textEdge struct {
	coord  float64
	y0, y1 float64
	count  int
}

// DetectTextEdgeRegions returns the bounding boxes of regions that look
// like tables, judged purely by text alignment.
//
// It reports regions, not grids. The caller still decides how to divide
// one into cells — which is the point: this composes with every existing
// strategy instead of replacing them.
//
// Returns nil when the page shows no sustained alignment, which is the
// correct answer for prose and the reason this can be trusted to raise
// recall without inventing tables.
func DetectTextEdgeRegions(words []Word, opts TextEdgeOpts) []BBox {
	if len(words) == 0 {
		return nil
	}
	opts = withTextEdgeDefaults(opts)

	lines := wordsToTextLines(words, opts.LineTol)
	if len(lines) < opts.MinLines {
		return nil
	}

	// Reading order: top to bottom, then left to right. Edges grow
	// downward, so the traversal order is part of the algorithm rather
	// than a presentational choice.
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].Y1 != lines[j].Y1 {
			return lines[i].Y1 > lines[j].Y1
		}
		return lines[i].X0 < lines[j].X0
	})

	edges := buildTextEdges(lines, opts)

	relevant := dominantAlignment(edges, opts.MinLines)
	if len(relevant) == 0 {
		return nil
	}

	return regionsFromEdges(relevant, lines, opts)
}

func withTextEdgeDefaults(o TextEdgeOpts) TextEdgeOpts {
	d := DefaultTextEdgeOpts()
	if o.MinLines <= 0 {
		o.MinLines = d.MinLines
	}
	if o.EdgeTol <= 0 {
		o.EdgeTol = d.EdgeTol
	}
	if o.CoordTol <= 0 {
		o.CoordTol = d.CoordTol
	}
	if o.MinTextLen <= 0 {
		o.MinTextLen = d.MinTextLen
	}
	if o.LineTol <= 0 {
		o.LineTol = d.LineTol
	}
	return o
}

// wordsToTextLines groups words into horizontal lines by baseline.
//
// Alignment is a property of a row, not of a word: the left edge of a
// table column is where the first word of each row begins. Running the
// detector on raw words would register every word's x and find
// "alignments" in the interior of a paragraph.
func wordsToTextLines(words []Word, tol float64) []textLine {
	upright := make([]Word, 0, len(words))
	for _, w := range words {
		if w.Upright {
			upright = append(upright, w)
		}
	}
	if len(upright) == 0 {
		return nil
	}

	groups := clusterObjects(upright, func(w Word) float64 { return w.Y0 }, tol, false)

	lines := make([]textLine, 0, len(groups))
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		l := textLine{X0: g[0].X0, Y0: g[0].Y0, X1: g[0].X1, Y1: g[0].Y1}
		for _, w := range g {
			l.X0 = math.Min(l.X0, w.X0)
			l.Y0 = math.Min(l.Y0, w.Y0)
			l.X1 = math.Max(l.X1, w.X1)
			l.Y1 = math.Max(l.Y1, w.Y1)
			for _, r := range w.Text {
				if r != ' ' && r != '\t' {
					l.runes++
				}
			}
		}
		lines = append(lines, l)
	}
	return lines
}

// buildTextEdges accumulates, for each alignment class, the set of
// vertical runs of lines sharing an x-coordinate.
func buildTextEdges(lines []textLine, opts TextEdgeOpts) [numAligns][]*textEdge {
	var edges [numAligns][]*textEdge

	for _, l := range lines {
		if l.runes < opts.MinTextLen {
			continue
		}
		for a := textAlign(0); a < numAligns; a++ {
			c := l.coordFor(a)
			if e := findAdjacentEdge(edges[a], c, l, opts); e != nil {
				// Extend downward. y0 tracks the running bottom, which
				// is what the next line's adjacency is tested against.
				e.y0 = math.Min(e.y0, l.Y0)
				e.count++
				continue
			}
			edges[a] = append(edges[a], &textEdge{
				coord: c, y0: l.Y0, y1: l.Y1, count: 1,
			})
		}
	}
	return edges
}

// findAdjacentEdge returns the edge that l continues, if any.
//
// Two conditions, and the second is the one that matters: the
// x-coordinates must match, AND the line must be vertically adjacent to
// where the alignment currently ends. Without adjacency, text at the top
// of a page would join an alignment belonging to an unrelated table at
// the bottom, and the resulting region would span everything between.
func findAdjacentEdge(edges []*textEdge, coord float64, l textLine, opts TextEdgeOpts) *textEdge {
	for _, e := range edges {
		if math.Abs(e.coord-coord) > opts.CoordTol {
			continue
		}
		// The line sits at or above the edge's current bottom, within
		// the tolerated gap. Lines arrive top-down, so l.Y0 <= e.y0 is
		// the normal case and the gap is what we bound.
		if e.y0-l.Y0 <= opts.EdgeTol && l.Y0 <= e.y0+opts.EdgeTol {
			return e
		}
	}
	return nil
}

// dominantAlignment keeps the single alignment class carrying the most
// evidence, and returns its valid edges.
//
// Keeping all three would be a mistake, not a conservative choice. A
// left-aligned table also produces centre and right alignments wherever
// cell contents happen to be similar in width, and those spurious edges
// widen the detected region past the table's real bounds. One class,
// chosen by weight of evidence, is the algorithm.
func dominantAlignment(edges [numAligns][]*textEdge, minLines int) []*textEdge {
	best := textAlign(0)
	bestScore := -1

	for a := textAlign(0); a < numAligns; a++ {
		score := 0
		for _, e := range edges[a] {
			if e.count >= minLines {
				score += e.count
			}
		}
		if score > bestScore {
			best, bestScore = a, score
		}
	}
	if bestScore <= 0 {
		return nil
	}

	valid := make([]*textEdge, 0, len(edges[best]))
	for _, e := range edges[best] {
		if e.count >= minLines {
			valid = append(valid, e)
		}
	}
	return valid
}

// regionsFromEdges turns surviving alignments into table bounding boxes.
//
// Edges that overlap vertically belong to the same table — they are its
// columns. The region is their combined extent, then widened to include
// any text line that falls inside that vertical band, because the
// alignments mark column starts and the text continues to the right of
// the last one.
func regionsFromEdges(edges []*textEdge, lines []textLine, opts TextEdgeOpts) []BBox {
	if len(edges) == 0 {
		return nil
	}

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].y1 != edges[j].y1 {
			return edges[i].y1 > edges[j].y1
		}
		return edges[i].coord < edges[j].coord
	})

	type band struct{ x0, y0, x1, y1 float64 }
	var bands []band

	for _, e := range edges {
		merged := false
		for i := range bands {
			// Vertical overlap means same table. Columns of one table
			// span roughly the same rows by construction.
			if e.y0 <= bands[i].y1 && e.y1 >= bands[i].y0 {
				bands[i].x0 = math.Min(bands[i].x0, e.coord)
				bands[i].x1 = math.Max(bands[i].x1, e.coord)
				bands[i].y0 = math.Min(bands[i].y0, e.y0)
				bands[i].y1 = math.Max(bands[i].y1, e.y1)
				merged = true
				break
			}
		}
		if !merged {
			bands = append(bands, band{e.coord, e.y0, e.coord, e.y1})
		}
	}

	pad := averageLineHeight(lines)

	out := make([]BBox, 0, len(bands))
	for _, b := range bands {
		// Grow horizontally over every line inside the band. An
		// alignment records where a column STARTS; the row's content
		// runs past the rightmost one, and clipping there would cut the
		// last column off every table.
		x0, x1 := b.x0, b.x1
		for _, l := range lines {
			if l.Y0 <= b.y1+pad && l.Y1 >= b.y0-pad {
				x0 = math.Min(x0, l.X0)
				x1 = math.Max(x1, l.X1)
			}
		}
		out = append(out, BBox{
			X0: x0, Y0: b.y0 - pad,
			X1: x1, Y1: b.y1 + pad,
		})
	}
	return out
}

// averageLineHeight is the padding unit: regions are grown by roughly
// one line so a header or trailing row sitting just outside the detected
// alignment is not clipped away.
func averageLineHeight(lines []textLine) float64 {
	if len(lines) == 0 {
		return 0
	}
	var sum float64
	for _, l := range lines {
		sum += l.Y1 - l.Y0
	}
	return sum / float64(len(lines))
}
