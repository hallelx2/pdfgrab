// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

// This file defines the public types of the table-finding pipeline:
// TableSettings (with pdfplumber-matching defaults), Table (the
// extracted result), and TableFinder (the intermediate object that
// exposes edges, intersections, and cell bboxes without running text
// extraction).
//
// Algorithm and field names are direct ports of pdfplumber's
// TableSettings / TableFinder / Table from pdfplumber/table.py. The
// public surface differs in two ways:
//
//   - Field names follow Go conventions (CamelCase, exported) rather
//     than pdfplumber's snake_case dict keys.
//   - Coordinates are PDF user space (origin at bottom-left, Y growing
//     up). pdfplumber emits image-space coordinates ("top" / "bottom"
//     with Y growing down); we use Y0/Y1 throughout. The intersection
//     geometry is invariant under that flip; only the comments
//     describing "below" / "right" change their sign.

// TableStrategy is the enum of edge-derivation strategies. Each axis
// (vertical, horizontal) picks one independently. All four pdfplumber
// strategies are implemented as of v0.3.0.
type TableStrategy string

const (
	// StrategyLines derives edges from drawn Lines, Rects (all four
	// sides), and Curves whose segments lie on an axis. Snap and join
	// tolerances are at their defaults — looser than lines_strict so
	// hand-drawn or jittery rules still merge.
	StrategyLines TableStrategy = "lines"

	// StrategyLinesStrict derives edges ONLY from drawn Lines.
	// Rectangle outlines and curve segments are ignored, even if they
	// look like a table grid. Use this when your PDF draws cell
	// backgrounds as filled rects that you do NOT want treated as row
	// boundaries.
	StrategyLinesStrict TableStrategy = "lines_strict"

	// StrategyText infers edges from word alignment. Vertical edges
	// come from clusters of words sharing X0 / X1 / centre positions;
	// horizontal edges from clusters sharing visual top. Best for
	// borderless tables — bank statements, narrative tables in 10-K
	// filings, scanned-then-OCR'd content — where the columns and
	// rows are conveyed by whitespace alignment rather than rules.
	// Tunable via MinWordsVertical (default 3) and
	// MinWordsHorizontal (default 1).
	StrategyText TableStrategy = "text"

	// StrategyExplicit uses caller-supplied coordinates from
	// ExplicitVerticalLines / ExplicitHorizontalLines as the only
	// source of edges on that axis. Useful when the table boundaries
	// are known from an external source (layout analysis, manual
	// annotation) and you want to bypass edge detection entirely.
	// The "explicit" strategy on an axis requires at least two
	// coordinates on that axis; fewer than two produces an error.
	StrategyExplicit TableStrategy = "explicit"

	// StrategyAuto picks "lines" or "text" for its axis by looking at
	// what the page actually drew. It exists for the very common table
	// that is ruled on ONE axis only.
	//
	// A table with horizontal rules and no vertical ones — booktabs
	// style, and the house style of most government and academic
	// publishing — yields no ruling intersections at all, so "lines"
	// finds nothing on either axis. On the ICDAR 2013 competition set
	// that accounted for every document where pdfgrab detected no
	// table whatsoever: us-017 has 218 horizontal rules and 0 vertical,
	// us-018 has 226 and 0, us-025 has 225 and 0.
	//
	// The rule, per axis:
	//
	//   - this axis has usable rulings           -> "lines"
	//   - it does not, but the OTHER axis does   -> "text"
	//   - neither axis has rulings               -> "lines" (find nothing)
	//
	// That last case is the important one. Falling back to "text" when
	// the page has no rulings at all is what makes a naive lines->text
	// fallback score WORSE than "lines" alone: on the same benchmark it
	// drops precision from 0.865 to 0.223, because a prose page has
	// word alignment too and the text strategy will happily report a
	// table for it. Rulings on the other axis are the evidence that a
	// table is really there; without that evidence Auto declines to
	// guess.
	StrategyAuto TableStrategy = "auto"
)

// TableSettings controls table finding. Construct via
// DefaultTableSettings() and override the fields you need — the zero
// value is NOT usable because the tolerances default to zero and the
// strategies are empty strings.
//
// Field naming and defaults are 1:1 with pdfplumber's TableSettings
// dataclass (see pdfplumber/table.py:486-555). Where pdfplumber
// supports independent x/y tolerances via *_x_tolerance / *_y_tolerance
// fallbacks, we expose the shared field directly; explicit per-axis
// overrides can be added later if a real-world need surfaces.
type TableSettings struct {
	// VerticalStrategy picks the source of vertical edges.
	// Default: StrategyLines.
	VerticalStrategy TableStrategy

	// HorizontalStrategy picks the source of horizontal edges.
	// Default: StrategyLines.
	HorizontalStrategy TableStrategy

	// SnapTolerance is the perpendicular-axis tolerance for clustering
	// near-collinear edges before joining (PDF points). Default: 3.
	SnapTolerance float64

	// JoinTolerance is the along-direction gap that still gets merged
	// during the join pass (PDF points). Default: 3.
	JoinTolerance float64

	// EdgeMinLength drops merged edges shorter than this (PDF points).
	// Default: 3.
	EdgeMinLength float64

	// EdgeMinLengthPrefilter drops raw edges before merging
	// (PDF points). Default: 1 — kills hairline construction
	// segments that snap+join shouldn't pull together.
	EdgeMinLengthPrefilter float64

	// IntersectionTolerance is the slack used when testing whether a
	// vertical edge crosses a horizontal edge — accounts for tiny
	// gaps between the end of a stroked line and the start of the
	// next (PDF points). Default: 3.
	IntersectionTolerance float64

	// TextTolerance is forwarded to the per-cell text-extraction call
	// inside ExtractTables. It overrides both x_tolerance and
	// y_tolerance of the underlying WordExtractor. Default: 3.
	TextTolerance float64

	// MinWordsVertical / MinWordsHorizontal control the "text"
	// strategy thresholds. A candidate column-boundary cluster must
	// contain at least MinWordsVertical words sharing X0 / X1 /
	// centre alignment to be promoted to a vertical edge; row
	// boundaries need MinWordsHorizontal words sharing a top edge.
	// pdfplumber defaults (3 / 1) mirror those in pdfplumber's
	// table.py:11-12. These fields are ignored when the corresponding
	// strategy is anything other than "text".
	MinWordsVertical   int
	MinWordsHorizontal int

	// KeepBlankChars is forwarded to the per-cell WordExtractor.
	// Default: false (matches pdfplumber's text_keep_blank_chars).
	KeepBlankChars bool

	// ExplicitVerticalLines / ExplicitHorizontalLines hold caller-
	// supplied edge positions. With StrategyLines, StrategyLinesStrict,
	// or StrategyText they are ADDED to the derived edges; with
	// StrategyExplicit they ARE the only source of edges on that axis.
	// Useful when a column or row boundary is invisible in the PDF but
	// known from an external source.
	//
	// Values are X coordinates for vertical lines, Y coordinates for
	// horizontal lines, both in PDF user-space points. Non-finite
	// values (NaN, Inf) are dropped with a log warning. When
	// StrategyExplicit is selected on an axis, at least two
	// coordinates must be supplied on that axis — fewer than two
	// returns an error.
	ExplicitVerticalLines   []float64
	ExplicitHorizontalLines []float64

	// MaxEdgesPerAxis is a defense-in-depth cap on the number of merged
	// rulings the table finder will accept on a single axis. If a page
	// yields more than this many vertical OR horizontal edges after
	// merging, table finding is skipped for that page (FindTables /
	// ExtractTables return no tables) and a warning is logged. A real
	// table never has this many distinct rulings on one axis; exceeding
	// it means the page is pathological (e.g. a dense vector drawing
	// misread as a grid), and processing it would only burn CPU. The
	// grid-indexed cell finder is fast enough that this cap should
	// essentially never trigger on legitimate input.
	//
	// Default: 1000. A negative value disables the cap; zero is treated
	// as "unset" and filled with the default (matching every other
	// field in this struct).
	MaxEdgesPerAxis int

	// MaxIntersections is the same defense-in-depth idea one stage
	// later: if the edge crossings exceed this count, table finding is
	// skipped for the page with a logged warning. This bounds the work
	// even in the unlikely event that a future input defeats the grid
	// optimization in the cell finder.
	//
	// Default: 50000. A negative value disables the cap; zero is treated
	// as "unset" and filled with the default.
	MaxIntersections int

	// MergeSplitTokens merges two adjacent cells when the column
	// boundary between them falls INSIDE a single token — that is, when
	// the last glyph of the left cell and the first glyph of the right
	// cell are close enough to belong to the same word.
	//
	// The "text" strategy derives column boundaries by clustering word
	// edges, so a narrow band that happens to align down the page
	// becomes a column even if it cuts a value in half. On a financial
	// statement that produces cells like
	//
	//	| Less: Accumulated depreciation | ( | 16,135) |
	//	|                                | December 3 | 1, |
	//
	// where the document reads "(16,135)" and "December 31,". No text is
	// lost, but a consumer treating a cell as one value gets two
	// fragments, and Table.CellsBBox covers only part of the value —
	// which matters when the bbox drives a citation highlight.
	//
	// OFF by default, deliberately. pdfplumber produces the same splits
	// (verified against pdfplumber 0.11.9 on a real 10-K: it yields
	// '(', '16,135)' for that row, and splits the label into
	// 'Less: Accumula', 'ted depreciation' as well), so enabling this by
	// default would silently break the parity this package promises.
	// Turn it on when clean values matter more than byte-compatibility —
	// feeding a table to an LLM, for instance.
	//
	// Merging is bounded by TextTolerance, the same threshold word
	// grouping uses, so it only ever rejoins glyphs that word grouping
	// would have placed in one word.
	MergeSplitTokens bool
}

// DefaultTableSettings returns settings with the pdfplumber default
// values pre-populated. The intended pattern is:
//
//	settings := pdfgrab.DefaultTableSettings()
//	settings.VerticalStrategy = pdfgrab.StrategyLinesStrict
//	tables, err := page.ExtractTables(settings)
//
// pdfplumber's defaults (table.py lines 9-12, 486-503):
//
//	DEFAULT_SNAP_TOLERANCE         = 3
//	DEFAULT_JOIN_TOLERANCE         = 3
//	DEFAULT_MIN_WORDS_VERTICAL     = 3
//	DEFAULT_MIN_WORDS_HORIZONTAL   = 1
//	edge_min_length                = 3
//	edge_min_length_prefilter      = 1
//	intersection_tolerance         = 3
//	vertical_strategy              = "lines"
//	horizontal_strategy            = "lines"
//	text_x_tolerance/y_tolerance   = 3
func DefaultTableSettings() TableSettings {
	return TableSettings{
		VerticalStrategy:       StrategyLines,
		HorizontalStrategy:     StrategyLines,
		SnapTolerance:          3,
		JoinTolerance:          3,
		EdgeMinLength:          3,
		EdgeMinLengthPrefilter: 1,
		IntersectionTolerance:  3,
		TextTolerance:          3,
		MinWordsVertical:       3,
		MinWordsHorizontal:     1,
		MaxEdgesPerAxis:        1000,
		MaxIntersections:       50000,
	}
}

// applyDefaults fills in zero-valued fields with pdfplumber-matching
// defaults. Callers who construct a TableSettings literal and only set
// the fields they care about get the same defaults as if they'd used
// DefaultTableSettings().
func (s TableSettings) applyDefaults() TableSettings {
	if s.VerticalStrategy == "" {
		s.VerticalStrategy = StrategyLines
	}
	if s.HorizontalStrategy == "" {
		s.HorizontalStrategy = StrategyLines
	}
	if s.SnapTolerance == 0 {
		s.SnapTolerance = 3
	}
	if s.JoinTolerance == 0 {
		s.JoinTolerance = 3
	}
	if s.EdgeMinLength == 0 {
		s.EdgeMinLength = 3
	}
	if s.EdgeMinLengthPrefilter == 0 {
		s.EdgeMinLengthPrefilter = 1
	}
	if s.IntersectionTolerance == 0 {
		s.IntersectionTolerance = 3
	}
	if s.TextTolerance == 0 {
		s.TextTolerance = 3
	}
	if s.MinWordsVertical == 0 {
		s.MinWordsVertical = 3
	}
	if s.MinWordsHorizontal == 0 {
		s.MinWordsHorizontal = 1
	}
	if s.MaxEdgesPerAxis == 0 {
		s.MaxEdgesPerAxis = 1000
	}
	if s.MaxIntersections == 0 {
		s.MaxIntersections = 50000
	}
	return s
}

// Table is the extracted result for one detected table. It carries
// the assembled cell texts plus the geometry needed for downstream
// consumers (re-rendering, click-through to source positions).
type Table struct {
	// Rows is the table's text content as a 2-D slice. Row 0 is the
	// VISUALLY TOP row of the table; column 0 is the leftmost. Empty
	// cells appear as "". Missing cells (when a row has fewer columns
	// than the table's column count, because the underlying cell
	// detection found a hole) are also "" — we promote missing to
	// empty so callers don't have to nil-check every entry.
	Rows [][]string

	// BBox is the union of every cell's bbox, in PDF user-space
	// coordinates (origin bottom-left, Y growing up).
	BBox BBox

	// Page is the 1-based page number the table was found on, copied
	// from the originating Page so callers can carry results across
	// page boundaries without holding Page references.
	Page int

	// CellsBBox is the per-cell bbox aligned to Rows: CellsBBox[i][j]
	// is the bbox of Rows[i][j]. Useful for re-rendering with
	// highlight overlays, or for re-cropping the page to extract the
	// cell's contents in a richer format than plain text.
	CellsBBox [][]BBox
}

// Cells returns the cell bboxes flattened into reading order
// (left-to-right, top-to-bottom). Provided as a convenience for
// callers that want a single iterable rather than a nested slice.
func (t Table) Cells() []BBox {
	out := make([]BBox, 0)
	for _, row := range t.CellsBBox {
		out = append(out, row...)
	}
	return out
}
