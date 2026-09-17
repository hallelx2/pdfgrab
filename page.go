// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"fmt"
	"log"
	"math"
	"strings"

	"github.com/hallelx2/pdfgrab/internal/layout"
	"github.com/hallelx2/pdfgrab/internal/pdf"
)

// Page is one page of a PDF document. The interface (not a struct) is
// intentional: it lets us swap implementations later (e.g. for a
// streaming PDF parser) without breaking callers, and it makes the
// API surface easy to mock in tests.
//
// Every accessor (Chars, Lines, Rects, Curves, Objects) walks the
// page content stream from scratch. We do NOT cache between calls
// because:
//
//  1. Callers that need ALL the objects call Objects() once.
//  2. Callers that need just the chars (say, for text extraction)
//     don't pay for the path-painting machinery they aren't using.
//  3. Caching means deciding when to invalidate, which is moot
//     because a Page is immutable from the caller's perspective.
//
// Pages are 1-indexed, matching pdfplumber. Number() returns the
// 1-based index so callers can format error messages without
// re-tracking which Page they were given.
type Page interface {
	// Number returns the page number (1-based).
	Number() int

	// Width and Height return the page's mediabox dimensions in PDF
	// points (1/72 inch), already adjusted for the page's /Rotate
	// entry — so a portrait letter-sized page rotated 90 degrees
	// reports Width=792 Height=612 (landscape), matching what a
	// PDF viewer would display.
	Width() float64
	Height() float64

	// Chars walks the page and returns every positioned glyph. The
	// order is content-stream order — i.e. the order the producer
	// drew them, NOT visual reading order. Downstream layout code
	// (extract_text, find_tables) sorts the chars by position.
	Chars() ([]Char, error)

	// Lines returns every straight-line segment drawn on the page.
	// Each `l` segment in the content stream becomes one Line.
	// Rectangles drawn via `re` are NOT decomposed into four Lines;
	// they're reported through Rects() instead.
	Lines() ([]Line, error)

	// Rects returns every rectangle drawn via the `re` operator.
	// Both stroked and filled rectangles are returned; the Stroke
	// and Fill flags say which.
	Rects() ([]Rect, error)

	// Curves returns every Bezier or composite path that isn't a
	// pure line-segment chain or a single rect.
	Curves() ([]Curve, error)

	// Objects returns Chars + Lines + Rects + Curves in a single
	// walk. Use this when you need all four — it's strictly
	// cheaper than calling each accessor separately because the
	// content stream is parsed exactly once.
	Objects() (Objects, error)

	// Words extracts positioned text runs from the page. A "word"
	// is a contiguous group of chars whose horizontal gaps are
	// within WordOpts.XTolerance and whose vertical positions
	// agree within WordOpts.YTolerance. Pass DefaultWordOpts() to
	// use pdfplumber-matching defaults. See WordOpts for the full
	// configuration surface.
	//
	// Returns an empty slice (not nil) when the page contains no
	// extractable text.
	Words(opts WordOpts) ([]Word, error)

	// ExtractText returns the page's text as a single string. By
	// default words on the same line are joined with a single
	// space and lines are joined with "\n". When TextOpts.Layout is
	// true, the output preserves spatial layout (column-aligned
	// text, blank lines for vertical gaps) at the cost of more
	// whitespace. Pass DefaultTextOpts() for pdfplumber-matching
	// defaults.
	ExtractText(opts TextOpts) (string, error)

	// ExtractTextSimple is a no-frills extraction that clusters
	// chars by visual line and joins them by gap detection. Use
	// when ExtractText's word-grouping heuristics produce undesired
	// results on adversarial input.
	ExtractTextSimple(xTolerance, yTolerance float64) (string, error)

	// FindTables runs the geometry-only stage of the table-finding
	// pipeline: derive edges from the page primitives, snap+join
	// into rulers, scan for intersections, assemble cells, group
	// cells into tables. Returns one TableFinder per detected
	// table-group so callers building debugging tools can inspect
	// the intermediate stages (edges / intersections / raw cells)
	// alongside the assembled per-table CellsGrid.
	//
	// v0.3.0 supports all four pdfplumber strategies: "lines",
	// "lines_strict", "text", and "explicit". Each axis (vertical,
	// horizontal) selects its strategy independently, so mixed
	// settings like vertical="text" + horizontal="lines" work as
	// expected.
	FindTables(settings TableSettings) ([]TableFinder, error)

	// ExtractTables wraps FindTables and runs per-cell text
	// extraction on every detected table. Cells with no chars
	// produce an empty string. Leading and trailing whitespace
	// inside each cell is stripped. Returns the slice of fully
	// populated Table structs in visual top-to-bottom-left-to-right
	// order.
	ExtractTables(settings TableSettings) ([]*Table, error)
}

// page is the unexported implementation backing the Page interface.
// It wraps a *pdf.Reader (so all four accessors can lazily ReadPage)
// and the 1-based index. We separate the interface from the impl so
// that future implementations (a Page backed by a different PDF
// parser, a Page deserialised from a cached layout) can plug in.
type page struct {
	doc    *document
	number int
}

func (p *page) Number() int { return p.number }

// mediaBoxAndRotation looks up the page's mediabox and rotate value.
// We do this on demand because Width/Height callers don't necessarily
// want to pay the cost of resolving the page's content stream.
func (p *page) mediaBoxAndRotation() (mb [4]float64, rotate int, err error) {
	info, err := p.doc.reader.ReadPage(p.number)
	if err != nil {
		return mb, 0, fmt.Errorf("%w: %v", ErrInvalidPDF, err)
	}
	return info.MediaBox, info.Rotate, nil
}

func (p *page) Width() float64 {
	mb, rot, err := p.mediaBoxAndRotation()
	if err != nil {
		return 0
	}
	w := mb[2] - mb[0]
	h := mb[3] - mb[1]
	// Swap dimensions for 90/270 rotations: viewer shows the page
	// landscape so the "width" the caller cares about is the height
	// of the rotated mediabox.
	if rot == 90 || rot == 270 {
		return h
	}
	return w
}

func (p *page) Height() float64 {
	mb, rot, err := p.mediaBoxAndRotation()
	if err != nil {
		return 0
	}
	w := mb[2] - mb[0]
	h := mb[3] - mb[1]
	if rot == 90 || rot == 270 {
		return w
	}
	return h
}

// collector is the Sink implementation that gathers events from one
// interpreter run. It buffers everything; we don't stream because the
// public API returns slices. For very large pages this could be a
// memory concern — a future optimisation could expose a callback-based
// alternative, but it's not justified at this stage when typical PDFs
// have a few hundred glyphs per page.
type collector struct {
	chars  []Char
	lines  []Line
	rects  []Rect
	curves []Curve
}

func (c *collector) EmitChar(ev pdf.CharEvent) {
	c.chars = append(c.chars, Char{
		Text:     ev.Text,
		X0:       ev.X0,
		Y0:       ev.Y0,
		X1:       ev.X1,
		Y1:       ev.Y1,
		FontName: ev.FontName,
		FontSize: ev.FontSize,
		Upright:  ev.Upright,
		Advance:  ev.Advance,
	})
}

func (c *collector) EmitPath(ev pdf.PathEvent) {
	// Classify the path. If it's exactly one `re` segment, emit a
	// Rect. If every non-h segment is `l`, emit a Line per `l`. Else
	// emit a Curve summarising the path. This is the same partition
	// pdfplumber uses to populate page.lines / page.rects / page.curves.
	if isSingleRect(ev.Segments) {
		s := ev.Segments[0]
		c.rects = append(c.rects, Rect{
			X0: s.X, Y0: s.Y,
			X1: s.X + s.W, Y1: s.Y + s.H,
			Stroke: ev.Stroke, Fill: ev.Fill, Width: ev.LineWidth,
		})
		return
	}
	if onlyLines(ev.Segments) {
		var prev [2]float64
		havePrev := false
		for _, s := range ev.Segments {
			switch s.Op {
			case "m":
				prev = [2]float64{s.X, s.Y}
				havePrev = true
			case "l":
				if havePrev && ev.Stroke {
					c.lines = append(c.lines, Line{
						X0: prev[0], Y0: prev[1],
						X1: s.X, Y1: s.Y,
						Stroke: ev.Stroke, Width: ev.LineWidth,
					})
				}
				prev = [2]float64{s.X, s.Y}
				havePrev = true
			case "h":
				// closepath: connect back to subpath start. Most
				// generators only `h` their rectangles, which we
				// already caught above; ignore here.
			}
		}
		return
	}
	// Generic curve: flatten endpoints in order.
	var pts [][2]float64
	for _, s := range ev.Segments {
		switch s.Op {
		case "m", "l", "h":
			pts = append(pts, [2]float64{s.X, s.Y})
		case "c", "y":
			pts = append(pts, [2]float64{s.X3, s.Y3})
		case "v":
			pts = append(pts, [2]float64{s.X3, s.Y3})
		case "re":
			pts = append(pts,
				[2]float64{s.X, s.Y},
				[2]float64{s.X + s.W, s.Y},
				[2]float64{s.X + s.W, s.Y + s.H},
				[2]float64{s.X, s.Y + s.H},
			)
		}
	}
	c.curves = append(c.curves, Curve{
		Points: pts, Stroke: ev.Stroke, Fill: ev.Fill, Width: ev.LineWidth,
	})
}

func isSingleRect(segs []pdf.PathSeg) bool {
	if len(segs) == 0 {
		return false
	}
	if segs[0].Op != "re" {
		return false
	}
	// A bare `re` is one segment; some generators emit `re h` to
	// explicitly close. Tolerate either.
	for i := 1; i < len(segs); i++ {
		if segs[i].Op != "h" {
			return false
		}
	}
	return true
}

func onlyLines(segs []pdf.PathSeg) bool {
	for _, s := range segs {
		switch s.Op {
		case "m", "l", "h":
			// ok
		default:
			return false
		}
	}
	return true
}

func (p *page) run() (*collector, error) {
	info, err := p.doc.reader.ReadPage(p.number)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPDF, err)
	}
	col := &collector{}
	if len(info.Content) == 0 {
		return col, nil
	}
	// Initial CTM: PDF default coordinate system has origin at
	// bottom-left of the mediabox. We translate so (0,0) on the
	// content stream lines up with the mediabox lower-left corner,
	// which is what pdfplumber and PDF viewers report.
	ctm := pdf.IdentityMatrix
	if info.MediaBox != [4]float64{} {
		ctm = pdf.Matrix{1, 0, 0, 1, -info.MediaBox[0], -info.MediaBox[1]}
	}
	// Rotation: PDF 1.7 §14.8.2 — rotate values are clockwise; we
	// match what a viewer would display by pre-rotating the CTM so
	// the rotated content appears axis-aligned to the caller.
	switch info.Rotate {
	case 90:
		ctm = pdf.Mult(pdf.Matrix{0, -1, 1, 0, -info.MediaBox[1], info.MediaBox[2]}, ctm)
	case 180:
		ctm = pdf.Mult(pdf.Matrix{-1, 0, 0, -1, info.MediaBox[2], info.MediaBox[3]}, ctm)
	case 270:
		ctm = pdf.Mult(pdf.Matrix{0, 1, -1, 0, info.MediaBox[3], -info.MediaBox[0]}, ctm)
	}
	interp := pdf.NewInterpreter(ctm, col)
	interp.Fonts = info.Fonts
	interp.XObjects = info.XObjects
	if err := interp.Run(info.Content); err != nil {
		return nil, fmt.Errorf("content stream: %w", err)
	}
	return col, nil
}

func (p *page) Chars() ([]Char, error) {
	col, err := p.run()
	if err != nil {
		return nil, err
	}
	return col.chars, nil
}

func (p *page) Lines() ([]Line, error) {
	col, err := p.run()
	if err != nil {
		return nil, err
	}
	return col.lines, nil
}

func (p *page) Rects() ([]Rect, error) {
	col, err := p.run()
	if err != nil {
		return nil, err
	}
	return col.rects, nil
}

func (p *page) Curves() ([]Curve, error) {
	col, err := p.run()
	if err != nil {
		return nil, err
	}
	return col.curves, nil
}

func (p *page) Objects() (Objects, error) {
	col, err := p.run()
	if err != nil {
		return Objects{}, err
	}
	return Objects{
		Chars:  col.chars,
		Lines:  col.lines,
		Rects:  col.rects,
		Curves: col.curves,
	}, nil
}

// charsJoinedText is a tiny helper used by tests to assemble a Chars
// slice back into a string ignoring positioning. It is NOT a real
// text-extraction algorithm — Page.ExtractText (Phase 1.3.B) will
// sort by Y/X with line breaks; this is just for the smoke tests
// here in 1.3.A that need to assert "this page has the right glyphs
// in roughly the right order".
func charsJoinedText(chars []Char) string {
	var sb strings.Builder
	for _, c := range chars {
		sb.WriteString(c.Text)
	}
	return sb.String()
}

// findTableEdges derives the input-edge slice for the table-finding
// pipeline from the page's primitives. The pipeline:
//
//  1. Walk the page once via Objects(), so we pay the content-stream
//     parse cost a single time rather than once per primitive type.
//  2. Compute per-axis base edges according to the requested strategy:
//     - "lines"        → Lines + Rects + Curves (axis-aligned)
//     - "lines_strict" → Lines only
//     - "text"         → words clustered by x0/x1/centre (v) or top (h)
//     - "explicit"     → empty (caller supplies the edges)
//  3. Append the caller's ExplicitVerticalLines / ExplicitHorizontalLines
//     on top of whichever base set was chosen.
//  4. Apply the prefilter (drop edges shorter than
//     EdgeMinLengthPrefilter — pdfplumber default 1 pt).
//  5. Merge (snap onto cluster means, then join collinear edges
//     within JoinTolerance).
//  6. Apply the post-merge length filter (drop edges shorter than
//     EdgeMinLength — pdfplumber default 3 pt).
//
// Each axis uses its own strategy, so "text" vertical + "lines"
// horizontal (or any of the 16 combinations) works exactly as in
// pdfplumber.
func (p *page) findTableEdges(s TableSettings) ([]layout.Edge, error) {
	// Resolve text strategy's input once (and only when needed) — both
	// axes can ask for it, and Words is an expensive call.
	var words []Word
	needWords := s.VerticalStrategy == StrategyText || s.HorizontalStrategy == StrategyText
	if needWords {
		opts := DefaultWordOpts()
		opts.XTolerance = s.TextTolerance
		opts.YTolerance = s.TextTolerance
		opts.KeepBlankChars = s.KeepBlankChars
		w, err := p.Words(opts)
		if err != nil {
			return nil, err
		}
		words = w
	}

	// Resolve drawn-primitive edges once if either axis needs them
	// (lines or lines_strict).
	var lineLikeEdges []layout.Edge
	needPrimitives := isLineLike(s.VerticalStrategy) || isLineLike(s.HorizontalStrategy)
	if needPrimitives {
		objs, err := p.Objects()
		if err != nil {
			return nil, err
		}
		tol := 0.1 // near-axis-aligned slack for FromLine/FromCurve
		lineLikeEdges = make([]layout.Edge, 0, len(objs.Lines)+4*len(objs.Rects)+2*len(objs.Curves))
		for _, l := range objs.Lines {
			if e, ok := layout.FromLine(layout.LineSegment{
				X0: l.X0, Y0: l.Y0, X1: l.X1, Y1: l.Y1, Width: l.Width,
			}, tol); ok {
				lineLikeEdges = append(lineLikeEdges, e)
			}
		}
		for _, r := range objs.Rects {
			lineLikeEdges = append(lineLikeEdges, layout.FromRect(layout.RectSegment{
				X0: r.X0, Y0: r.Y0, X1: r.X1, Y1: r.Y1, Width: r.Width,
			})...)
		}
		for _, c := range objs.Curves {
			lineLikeEdges = append(lineLikeEdges, layout.FromCurve(layout.CurveSegment{
				Points: c.Points, Width: c.Width,
			}, tol)...)
		}
	}

	pageWidth := p.Width()
	pageHeight := p.Height()

	// Resolve StrategyAuto now that the page's drawn edges are known.
	vStrategy := resolveAuto(s.VerticalStrategy, layout.Vertical, lineLikeEdges)
	hStrategy := resolveAuto(s.HorizontalStrategy, layout.Horizontal, lineLikeEdges)

	// Auto may have turned an axis into "text" after the fact, so the
	// words it needs might not have been fetched above.
	if words == nil && (vStrategy == StrategyText || hStrategy == StrategyText) {
		opts := DefaultWordOpts()
		opts.XTolerance = s.TextTolerance
		opts.YTolerance = s.TextTolerance
		opts.KeepBlankChars = s.KeepBlankChars
		w, err := p.Words(opts)
		if err != nil {
			return nil, err
		}
		words = w
	}

	// Per-axis base edge derivation.
	vEdges := p.baseEdges(vStrategy, layout.Vertical, lineLikeEdges, words, s)
	hEdges := p.baseEdges(hStrategy, layout.Horizontal, lineLikeEdges, words, s)

	// Explicit overrides are added on top of whichever base set was
	// chosen. With StrategyExplicit the base set is empty so the
	// explicit edges are the only source; with the other strategies
	// they're additive (helpful when a column boundary isn't drawn).
	vEdges = append(vEdges, explicitVerticalEdges(s.ExplicitVerticalLines, 0, pageHeight)...)
	hEdges = append(hEdges, explicitHorizontalEdges(s.ExplicitHorizontalLines, 0, pageWidth)...)

	combined := make([]layout.Edge, 0, len(vEdges)+len(hEdges))
	combined = append(combined, vEdges...)
	combined = append(combined, hEdges...)

	// Prefilter (drop hairline construction segments). pdfplumber
	// applies edge_min_length_prefilter BEFORE merging.
	combined = layout.FilterEdgesByLength(combined, s.EdgeMinLengthPrefilter)

	// Merge: snap onto cluster means, then join collinear segments
	// that touch within JoinTolerance.
	merged := layout.MergeEdges(combined, s.SnapTolerance, s.SnapTolerance, s.JoinTolerance, s.JoinTolerance)

	// Post-merge length filter (drop short stubs).
	merged = layout.FilterEdgesByLength(merged, s.EdgeMinLength)

	return merged, nil
}

// isLineLike reports whether the strategy derives its edges from
// drawn primitives (Lines / Rects / Curves), i.e. whether
// findTableEdges needs to call Objects(). Text and explicit
// strategies don't.
//
// StrategyAuto counts: it cannot decide anything without seeing what the
// page drew, and it may well resolve to "lines".
func isLineLike(s TableStrategy) bool {
	return s == StrategyLines || s == StrategyLinesStrict || s == StrategyAuto
}

// minEdgesForAxis is how many rulings an axis needs before Auto treats it
// as genuinely ruled. Two is the floor that can bound a cell; a single
// stray rule (an underline, a header separator, a page border) is not a
// table and must not be read as evidence of one.
const minEdgesForAxis = 2

// resolveAuto turns StrategyAuto into a concrete strategy for one axis,
// using the edges the page actually drew. Every other strategy passes
// through untouched.
//
// See StrategyAuto for why the "neither axis is ruled" case deliberately
// resolves to lines rather than text.
func resolveAuto(s TableStrategy, orientation layout.Orientation, lineLikeEdges []layout.Edge) TableStrategy {
	if s != StrategyAuto {
		return s
	}
	other := layout.Horizontal
	if orientation == layout.Horizontal {
		other = layout.Vertical
	}
	if len(layout.FilterEdgesByOrientation(lineLikeEdges, orientation)) >= minEdgesForAxis {
		return StrategyLines
	}
	if len(layout.FilterEdgesByOrientation(lineLikeEdges, other)) >= minEdgesForAxis {
		// The other axis is ruled, so a table is really here — we just
		// have to infer this axis from word alignment.
		return StrategyText
	}
	return StrategyLines
}

// baseEdges returns the per-axis edges produced by the named strategy.
// lineLikeEdges is the unfiltered slice of edges derived from the
// page's drawn primitives (Lines + Rects + Curves); it's consulted
// only when the strategy is "lines" or "lines_strict". words is the
// page's extracted text runs; consulted only for the "text" strategy.
//
// "explicit" returns nil here — the caller's explicit slice is
// concatenated separately in findTableEdges.
func (p *page) baseEdges(strategy TableStrategy, orientation layout.Orientation, lineLikeEdges []layout.Edge, words []Word, s TableSettings) []layout.Edge {
	switch strategy {
	case StrategyLines:
		out := layout.FilterEdgesByOrientation(lineLikeEdges, orientation)
		return out
	case StrategyLinesStrict:
		out := layout.FilterEdgesByOrientation(lineLikeEdges, orientation)
		return layout.FilterEdgesBySource(out, layout.SourceLine)
	case StrategyText:
		if orientation == layout.Vertical {
			return wordsToEdgesV(words, s.MinWordsVertical)
		}
		return wordsToEdgesH(words, s.MinWordsHorizontal)
	case StrategyExplicit:
		// Caller-supplied edges are concatenated elsewhere; the base
		// set for the "explicit" strategy is empty.
		return nil
	default:
		return nil
	}
}

// FindTables runs the geometry-only pipeline (edges → intersections
// → cells → tables) and returns one TableFinder per detected table
// group. All four pdfplumber strategies (lines, lines_strict, text,
// explicit) are supported; the two axes can use different strategies
// independently.
//
// The returned slice is in visual top-to-bottom-left-to-right order
// (sorted by the topmost-leftmost cell of each table).
func (p *page) FindTables(settings TableSettings) ([]TableFinder, error) {
	s := settings.applyDefaults()
	if err := ensureSupportedStrategies(s); err != nil {
		return nil, err
	}
	if err := validateExplicitForStrategy(s); err != nil {
		return nil, err
	}

	edges, err := p.findTableEdges(s)
	if err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, nil
	}

	// Safety cap (defense in depth): a real table never has thousands
	// of distinct rulings on one axis. If the merged edge count blows
	// past MaxEdgesPerAxis the page is pathological (e.g. a dense vector
	// illustration misread as a grid); skip it rather than feed the
	// intersection scan an input that can only waste CPU.
	if s.MaxEdgesPerAxis > 0 {
		var nV, nH int
		for _, e := range edges {
			if e.Orientation == layout.Vertical {
				nV++
			} else {
				nH++
			}
		}
		if nV > s.MaxEdgesPerAxis || nH > s.MaxEdgesPerAxis {
			log.Printf("pdfgrab: skipping table finding on page %d: %d vertical / %d horizontal edges exceed MaxEdgesPerAxis=%d (page is not a real table)",
				p.number, nV, nH, s.MaxEdgesPerAxis)
			return nil, nil
		}
	}

	intersections := edgesToIntersections(edges, s.IntersectionTolerance, s.IntersectionTolerance)

	// Second safety cap at the intersection stage — bounds the work
	// even if some future input defeats the grid-indexed cell finder.
	if s.MaxIntersections > 0 && len(intersections) > s.MaxIntersections {
		log.Printf("pdfgrab: skipping table finding on page %d: %d intersections exceed MaxIntersections=%d",
			p.number, len(intersections), s.MaxIntersections)
		return nil, nil
	}

	cells := intersectionsToCells(intersections)
	groups := cellsToTables(cells)

	out := make([]TableFinder, 0, len(groups))
	for _, g := range groups {
		out = append(out, TableFinder{
			Edges:         edges,
			Intersections: intersections,
			Cells:         cells,
			Tables:        []TableBox{assembleTableBox(g)},
		})
	}
	return out, nil
}

// ExtractTables runs FindTables then fills each cell with the text
// of the chars whose centre lies inside the cell's bbox. Empty cells
// produce "". Leading/trailing whitespace is stripped per cell.
//
// The char-in-cell test matches pdfplumber's Table.extract:
//
//	v_mid = (char.top + char.bottom) / 2
//	h_mid = (char.x0 + char.x1) / 2
//	(h_mid >= x0) AND (h_mid < x1) AND (v_mid >= top) AND (v_mid < bottom)
//
// Translating from pdfplumber's image space to PDF user space: "top"
// becomes the larger Y, "bottom" becomes the smaller Y; the inclusive
// / exclusive convention on the right and bottom edges stays the
// same so a glyph sitting EXACTLY on a column boundary lands in the
// left column.
func (p *page) ExtractTables(settings TableSettings) ([]*Table, error) {
	s := settings.applyDefaults()
	if err := ensureSupportedStrategies(s); err != nil {
		return nil, err
	}
	if err := validateExplicitForStrategy(s); err != nil {
		return nil, err
	}

	finders, err := p.FindTables(s)
	if err != nil {
		return nil, err
	}
	if len(finders) == 0 {
		return nil, nil
	}

	chars, err := p.Chars()
	if err != nil {
		return nil, err
	}

	tables := make([]*Table, 0, len(finders))
	for _, f := range finders {
		for _, tb := range f.Tables {
			t := assembleTableText(tb, chars, s, p.number)
			tables = append(tables, t)
		}
	}
	return tables, nil
}

// assembleTableText fills the per-cell text grid for one TableBox by
// (a) filtering the page's chars by centre-in-cell-bbox, (b) running
// the existing word extractor + dense text join over the cell's
// chars, and (c) trimming whitespace.
func assembleTableText(tb TableBox, chars []Char, s TableSettings, pageNumber int) *Table {
	rows := make([][]string, tb.Rows)
	for i := range rows {
		rows[i] = make([]string, tb.Cols)
	}

	textOpts := DefaultTextOpts()
	textOpts.XTolerance = s.TextTolerance
	textOpts.YTolerance = s.TextTolerance

	wordOpts := DefaultWordOpts()
	wordOpts.XTolerance = s.TextTolerance
	wordOpts.YTolerance = s.TextTolerance
	wordOpts.KeepBlankChars = s.KeepBlankChars

	for ri, row := range tb.CellsGrid {
		// Find the outermost occupied columns in this row. Their outer
		// edges bound the table itself, not a neighbouring cell, so they
		// are allowed to keep glyphs that straddle them (see
		// charsInCellEdges).
		first, last := -1, -1
		for ci := range row {
			if !row[ci].IsZero() {
				if first < 0 {
					first = ci
				}
				last = ci
			}
		}
		for ci, cell := range row {
			if cell.IsZero() {
				rows[ri][ci] = ""
				continue
			}
			cellChars := charsInCellEdges(chars, cell, ci == first, ci == last)
			if len(cellChars) == 0 {
				rows[ri][ci] = ""
				continue
			}
			text := extractTextFromChars(cellChars, textOpts)
			rows[ri][ci] = strings.TrimSpace(text)
		}
	}

	cells := tb.CellsGrid
	if s.MergeSplitTokens {
		rows, cells = mergeSplitTokens(rows, cells, chars, s.TextTolerance)
	}

	return &Table{
		Rows:      rows,
		BBox:      tb.BBox,
		Page:      pageNumber,
		CellsBBox: cells,
	}
}

// mergeSplitTokens rejoins adjacent cells whose shared boundary cuts
// through a single token. See TableSettings.MergeSplitTokens for why
// this is opt-in.
//
// A boundary is judged to split a token when the rightmost glyph of the
// left cell and the leftmost glyph of the right cell sit closer together
// than tol — the same threshold word grouping uses, so this only ever
// rejoins glyphs that Words() would have put in one word. Real column
// gutters are far wider than an intra-word gap, which is what keeps this
// from collapsing genuinely distinct columns.
//
// Both the text and the cell bbox are merged. The bbox matters as much
// as the text: it is what a citation highlight is drawn from, and a
// half-value box under-covers the number on screen.
func mergeSplitTokens(rows [][]string, cells [][]BBox, chars []Char, tol float64) ([][]string, [][]BBox) {
	if tol <= 0 {
		tol = 3
	}
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols < 2 {
		return rows, cells
	}

	// A column boundary is a property of the TABLE, not of one row. Decide
	// once, over every row, whether boundary ci|ci+1 cuts a token — then
	// apply that decision uniformly.
	//
	// Doing this per-row instead would merge the header band (where the
	// split occurs) while leaving the data rows alone, so rows would end
	// up with different column counts and the grid would shear: the
	// header's second date would sit above the first column of figures.
	// A ragged table is a worse outcome than the split it set out to fix.
	drop := make([]bool, cols)
	for ci := 0; ci+1 < cols; ci++ {
		for ri := range cells {
			if ci+1 >= len(cells[ri]) {
				continue
			}
			l, r := cells[ri][ci], cells[ri][ci+1]
			if l.IsZero() || r.IsZero() {
				continue
			}
			if rows[ri][ci] == "" || rows[ri][ci+1] == "" {
				continue
			}
			if boundarySplitsToken(chars, l, r, tol) {
				drop[ci] = true
				break
			}
		}
	}
	anyDrop := false
	for _, d := range drop {
		if d {
			anyDrop = true
			break
		}
	}
	if !anyDrop {
		return rows, cells
	}

	outRows := make([][]string, len(rows))
	outCells := make([][]BBox, len(cells))
	for ri := range rows {
		var rowText []string
		var rowCells []BBox
		for ci := 0; ci < cols; ci++ {
			text := ""
			if ci < len(rows[ri]) {
				text = rows[ri][ci]
			}
			cell := BBox{}
			if ri < len(cells) && ci < len(cells[ri]) {
				cell = cells[ri][ci]
			}
			// drop[ci-1] means the boundary to this cell's LEFT is gone,
			// so it belongs to the cell before it.
			if ci > 0 && drop[ci-1] && len(rowText) > 0 {
				n := len(rowText) - 1
				// Whether to merge is a table-wide decision, so the grid
				// stays rectangular. The SEPARATOR is per-row, because the
				// same boundary can cut a token on one row and fall
				// between two real words on another.
				//
				// Joining blind produced "millions,except" from
				// "(Dollars in millions," + "except per share amount)" —
				// the boundary was dropped for a split several rows above,
				// and this row paid for it.
				sep := " "
				prevCell := rowCells[n]
				if text == "" || rowText[n] == "" {
					sep = ""
				} else if !cell.IsZero() && !prevCell.IsZero() &&
					boundarySplitsToken(chars, prevCell, cell, tol) {
					sep = "" // this row really is one token cut in half
				}
				rowText[n] += sep + text
				if !cell.IsZero() {
					if rowCells[n].IsZero() {
						rowCells[n] = cell
					} else {
						rowCells[n] = rowCells[n].Union(cell)
					}
				}
				continue
			}
			rowText = append(rowText, text)
			rowCells = append(rowCells, cell)
		}
		outRows[ri] = rowText
		outCells[ri] = rowCells
	}
	return outRows, outCells
}

// boundarySplitsToken reports whether the glyphs either side of the
// boundary between left and right are close enough to be one word.
func boundarySplitsToken(chars []Char, left, right BBox, tol float64) bool {
	// The two cells must actually be horizontal neighbours on the same
	// band; a vertical stack shares no boundary to split.
	if math.Abs(left.X1-right.X0) > 0.5 {
		return false
	}
	// Nearest ink either side of the boundary, ignoring whitespace: a
	// space glyph is flush against its neighbours, so counting it as ink
	// would make every space look like a zero-gap intra-word join.
	var lastLeft, firstRight *Char
	var spaces []*Char
	for i := range chars {
		c := &chars[i]
		hMid := (c.X0 + c.X1) / 2
		vMid := (c.Y0 + c.Y1) / 2
		if vMid < left.Y0 || vMid >= left.Y1 {
			continue
		}
		if strings.TrimSpace(c.Text) == "" {
			spaces = append(spaces, c)
			continue
		}
		if hMid >= left.X0 && hMid < left.X1 {
			if lastLeft == nil || c.X1 > lastLeft.X1 {
				lastLeft = c
			}
		}
		if hMid >= right.X0 && hMid < right.X1 {
			if firstRight == nil || c.X0 < firstRight.X0 {
				firstRight = c
			}
		}
	}
	if lastLeft == nil || firstRight == nil {
		return false
	}

	// A space sitting IN the gap settles it: the PDF is stating outright
	// that a word ends here, so whatever the geometry says, this is not
	// one token cut in half. Only the gap is checked — a space elsewhere
	// in either cell says nothing about this boundary, and treating it as
	// if it did stops every merge from happening at all.
	for _, sp := range spaces {
		if sp.X1 > lastLeft.X1 && sp.X0 < firstRight.X0 {
			return false
		}
	}

	gap := firstRight.X0 - lastLeft.X1
	return gap >= 0 && gap < tol
}

// charsInCell returns the chars whose centre point lies inside the
// supplied bbox. Mirrors pdfplumber's char_in_bbox predicate, which
// is the right thing for cell extraction: a glyph that straddles two
// cells gets assigned to the cell containing its visual centre, not
// to both.
//
// Inclusive on x0 / y0 (bottom-left), exclusive on x1 / y1 (top-
// right). The exclusivity makes the boundary deterministic — a glyph
// sitting at exactly the column boundary goes into the left column.
func charsInCell(chars []Char, cell BBox) []Char {
	return charsInCellEdges(chars, cell, false, false)
}

// charsInCellEdges is charsInCell with the option to treat the left
// and/or right edge as the OUTER boundary of the table rather than a
// boundary shared with a neighbouring cell.
//
// Midpoint assignment is the correct rule for an interior boundary: it
// decides which of two candidate cells owns a glyph that straddles them,
// and every glyph still lands somewhere. At the table's outer edge there
// is no competing cell, so the same rule silently deletes the glyph
// instead of placing it.
//
// That is not hypothetical. On 3M's 2018 10-K balance sheet the value
// "(16,048)" ends with a ")" whose centre sits at x=537.879 while the
// last column ends at x=537.871 — a shortfall of 0.008pt, roughly a
// nine-thousandth of an inch. The ")" was dropped, turning the
// accounting notation for -16,048 into a plain 16,048. Across that
// filing's five financial statements it corrupted 19% of all negative
// numbers, silently flipping their sign.
//
// So on an outer edge we also keep glyphs that merely OVERLAP it. The
// widening is bounded by the glyph itself, so it cannot pull in
// unrelated page content — only a glyph genuinely crossing the table's
// own boundary.
func charsInCellEdges(chars []Char, cell BBox, outerLeft, outerRight bool) []Char {
	out := make([]Char, 0, len(chars))
	for _, c := range chars {
		hMid := (c.X0 + c.X1) / 2
		vMid := (c.Y0 + c.Y1) / 2
		if vMid < cell.Y0 || vMid >= cell.Y1 {
			continue
		}
		in := hMid >= cell.X0 && hMid < cell.X1
		if !in && outerRight && hMid >= cell.X1 && c.X0 < cell.X1 {
			in = true // straddles the table's right edge
		}
		if !in && outerLeft && hMid < cell.X0 && c.X1 > cell.X0 {
			in = true // straddles the table's left edge
		}
		if in {
			out = append(out, c)
		}
	}
	return out
}
