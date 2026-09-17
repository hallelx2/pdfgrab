// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License. See LICENSE file in the project root.

package pdfgrab

// This file defines the public per-page object types. Naming mirrors
// pdfplumber's char/line/rect/curve objects, but field names follow Go
// conventions: exported, CamelCase, with self-documenting comments.
//
// Coordinate system: PDF default user space. Origin (0,0) is the BOTTOM
// LEFT of the page; the X axis grows to the right; the Y axis grows
// UPWARDS. All measurements are in PDF points (1/72 inch). For every
// bbox struct, x0 <= x1 and y0 <= y1; we normalise after applying the
// page CTM so callers never have to deal with inverted axes.
//
// We do NOT (yet) match pdfplumber's full field list — fields like
// "top", "bottom", "doctop", "object_type", and "matrix" can be derived
// from the data we expose, and we'd rather grow this surface gradually
// than dump 25 fields callers don't need. The fields that ARE here are
// the ones every layout-analysis algorithm (extract_text, find_tables,
// word grouping) actually reads.

// Char is a single positioned glyph on a page. Adjacent Chars do NOT
// share state — each carries its own font, size, and absolute bbox so
// downstream code (word grouping, table finding, text extraction) can
// reason about each glyph in isolation.
//
// Text is the Unicode string this glyph represents. For most fonts a
// glyph maps to a single code point ("A"), but Adobe ligature glyph
// names ("fi", "ffi") and ToUnicode maps with multi-character bfchar
// entries can produce multi-rune strings — we never split them.
type Char struct {
	// Text is the Unicode payload of this glyph (one or more runes).
	// Empty string means the font's encoding and ToUnicode CMap both
	// failed to map the glyph; the bbox still describes where the
	// glyph was drawn so downstream layout heuristics can use the
	// positioning even when we can't read the text.
	Text string

	// Bounding box in PDF user space, with x0 <= x1 and y0 <= y1.
	// Y0 is the descender baseline of the glyph; Y1 is the top of the
	// ascender. The bbox is the typographic cell, not the ink — it
	// matches what pdfplumber reports.
	X0, Y0, X1, Y1 float64

	// FontName is the /BaseFont (or /Name) value from the font dict,
	// e.g. "Helvetica" or "ABCDEF+TimesNewRoman-Bold". It's surfaced
	// verbatim — callers that want to detect bold weight should match
	// on substrings ("bold", "-bd").
	FontName string

	// FontSize is the size the glyph was rendered at, in PDF user space
	// units (i.e. after applying the text matrix and CTM). For a glyph
	// drawn with `12 Tf` and identity CTM this is 12.0; if the CTM
	// scales by 2x it's 24.0.
	FontSize float64

	// Upright is true when the glyph is drawn in normal reading
	// orientation (a*d > 0 and b*c <= 0 in the combined text matrix).
	// Rotated and mirrored glyphs report false. pdfplumber uses the
	// same predicate to skip vertical-stamp text during table finding.
	Upright bool

	// Advance is the horizontal advance width the text matrix moved by
	// after drawing this glyph (in user-space units). It already
	// includes character spacing, word spacing for the space glyph,
	// and the font's per-glyph width — i.e. the actual ink-to-ink
	// distance to the next glyph.
	Advance float64
}

// Width returns x1 - x0.
func (c Char) Width() float64 { return c.X1 - c.X0 }

// Height returns y1 - y0.
func (c Char) Height() float64 { return c.Y1 - c.Y0 }

// Line is a single straight-line segment emitted by an `S` (stroke)
// operator on a path that contained an `l` segment. Each `l` becomes
// one Line; a rectangle drawn with `re` does NOT decompose into four
// Lines (it becomes a single Rect) — that's how pdfplumber tells the
// two apart, and we keep the same distinction so downstream code can
// pick the right collection for table-finding.
type Line struct {
	X0, Y0, X1, Y1 float64

	// Stroke is true if the path was stroked (S, s, B, b, B*, b*).
	// Lines emitted from non-stroked paths are dropped before the
	// caller sees them — but the field is preserved for symmetry
	// with Rect/Curve so layout code can branch on it uniformly.
	Stroke bool

	// Width is the stroke line width in user-space units at the time
	// the line was emitted. For ruling lines this is typically 0.5–1.0.
	Width float64
}

// Rect is a rectangle path emitted by an `re` operator (a single PDF
// instruction that draws a closed box). pdfplumber tracks these
// separately from generic four-segment paths because table grids,
// borders, and shaded cells are nearly always drawn this way — keeping
// the distinction makes table detection much more reliable.
type Rect struct {
	X0, Y0, X1, Y1 float64

	// Stroke and Fill mirror the painting operator that closed the
	// path: S/s set Stroke; f/F/f* set Fill; B/b/B*/b* set both.
	// Either or both can be true — but not neither (paths with `n`
	// produce nothing).
	Stroke bool
	Fill   bool

	// Width is the stroke line width at the time the rectangle was
	// painted, in user-space units. Zero when Stroke is false.
	Width float64
}

// Curve is a path that contains at least one curve segment (`c`, `v`,
// or `y`) or any path that doesn't reduce to a single Rect or a series
// of straight Lines. Points are the path vertices in order, including
// intermediate control points; callers that need actual Bezier shapes
// can reconstruct them from the operator stream — for now we keep the
// simpler flattened representation that's enough for "did the page
// have decorative curves on it" detection.
type Curve struct {
	// Points are the vertices of the path in order. For curve segments,
	// only the on-path endpoints are emitted (control points are
	// dropped) — this matches pdfplumber's "pts" field, which is also
	// just the endpoints. Callers that need precise curve geometry
	// should request that extension; we don't expect tables to use
	// curves so dropping control points is fine for the initial scope.
	Points [][2]float64

	Stroke bool
	Fill   bool
	Width  float64
}

// Objects is the bundle of all primitive page objects, returned by
// Page.Objects(). It's a convenience for callers that want everything
// in one shot rather than four separate Page method calls — the per-
// type accessors (Chars/Lines/Rects/Curves) are independent and safe
// to call concurrently, but they each redo the page content-stream
// walk, so Objects() is the cheaper choice when you need it all.
type Objects struct {
	Chars  []Char
	Lines  []Line
	Rects  []Rect
	Curves []Curve
}
