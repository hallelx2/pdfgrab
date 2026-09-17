// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

// Package pdf is the internal content-stream interpreter for pdfgrab.
//
// It is intentionally NOT a public package: the data model here is the
// raw output of walking a PDF content stream (glyph events, path
// segments, graphics-state pushes), and it would be premature to lock
// that shape behind a stable API while the layout-analysis layers on
// top are still being built out.
//
// The interpreter mirrors the structure of pdfminer.six's PDFContentEmitter
// (in pdfinterp.py) — same operator dispatch table, same graphics/text
// state stacks, same coordinate-transform math — but rewritten in
// idiomatic Go: explicit state structs instead of dynamic attributes,
// a switch-on-operator dispatch instead of getattr, and value types
// for matrices instead of tuples.
//
// What this package does NOT do:
//   - Compose glyphs into words or lines. That is layout analysis and
//     lives one layer up (Page.Words, in a later sub-phase).
//   - Solve table detection. Also layout analysis.
//   - Handle PDF encryption. The reader assumes a decrypted document;
//     callers that need decryption should pre-process with pdfcpu.
//   - Render Type 3 fonts. Type 3 fonts are themselves content streams;
//     they are extremely rare in tabular documents and we explicitly
//     punt — the glyph's bbox is still emitted, just with empty Text.
package pdf

// Matrix is a 3x3 affine transform stored as the six PDF parameters
// [a b c d e f], representing the matrix
//
//	[ a b 0 ]
//	[ c d 0 ]
//	[ e f 1 ]
//
// Multiplication and point application follow the PDF spec (PDF 1.7,
// section 8.3.3). We use a fixed-size value type rather than a pointer
// or slice — the matrix is six floats and copying it is cheaper than
// reaching through a heap-allocated wrapper, and we save the GC
// bookkeeping in a hot path that runs once per glyph.
type Matrix [6]float64

// IdentityMatrix is the affine identity (no transform). Used as the
// starting CTM and text matrix until an op overrides them.
var IdentityMatrix = Matrix{1, 0, 0, 1, 0, 0}

// Mult is matrix concatenation, following pdfminer's mult_matrix
// convention exactly: `Mult(m2, m1)` returns the matrix M such that
// applying M to a point produces the same result as applying m2
// first and then applying m1 to the intermediate result.
//
// This convention matters because PDF generators emit operators in
// the order they want them composed: a `cm M ... TM ...` sequence
// produces glyph positions via `Translate(textMatrix, ...)` applied
// through `Mult(textMatrix, ctm)` — i.e. text matrix first, then
// CTM — which is exactly the order they're written in the stream.
func Mult(m2, m1 Matrix) Matrix {
	a1, b1, c1, d1, e1, f1 := m2[0], m2[1], m2[2], m2[3], m2[4], m2[5]
	a0, b0, c0, d0, e0, f0 := m1[0], m1[1], m1[2], m1[3], m1[4], m1[5]
	return Matrix{
		a0*a1 + c0*b1,
		b0*a1 + d0*b1,
		a0*c1 + c0*d1,
		b0*c1 + d0*d1,
		a0*e1 + c0*f1 + e0,
		b0*e1 + d0*f1 + f0,
	}
}

// Translate returns the matrix m post-translated by (tx, ty) in its
// own local coordinate system. Equivalent to Mult({1,0,0,1,tx,ty}, m).
// Used to position each glyph as the text matrix advances.
func Translate(m Matrix, tx, ty float64) Matrix {
	a, b, c, d, e, f := m[0], m[1], m[2], m[3], m[4], m[5]
	return Matrix{a, b, c, d, tx*a + ty*c + e, tx*b + ty*d + f}
}

// ApplyPoint maps (x, y) through m. PDF user space points → device
// space, or text space → user space, depending on which matrix m is.
func ApplyPoint(m Matrix, x, y float64) (float64, float64) {
	a, b, c, d, e, f := m[0], m[1], m[2], m[3], m[4], m[5]
	return a*x + c*y + e, b*x + d*y + f
}

// ApplyRect maps a rectangle through m. The result is a NEW rectangle
// that tightly encloses the (possibly rotated) image of the input — it
// is NOT a rotated rectangle. After the transform we normalise so
// x0 <= x1 and y0 <= y1; pdfplumber relies on this invariant.
func ApplyRect(m Matrix, x0, y0, x1, y1 float64) (float64, float64, float64, float64) {
	// Four corner points of the input rectangle.
	corners := [4][2]float64{
		{x0, y0},
		{x1, y0},
		{x1, y1},
		{x0, y1},
	}
	minX, minY := 0.0, 0.0
	maxX, maxY := 0.0, 0.0
	for i, p := range corners {
		tx, ty := ApplyPoint(m, p[0], p[1])
		if i == 0 {
			minX, minY = tx, ty
			maxX, maxY = tx, ty
			continue
		}
		if tx < minX {
			minX = tx
		}
		if tx > maxX {
			maxX = tx
		}
		if ty < minY {
			minY = ty
		}
		if ty > maxY {
			maxY = ty
		}
	}
	return minX, minY, maxX, maxY
}

// GraphicsState is the snapshot pushed by `q` and popped by `Q`.
//
// We track only the fields the public API actually exposes: the CTM,
// stroke fill (so paths can record whether they were filled or stroked),
// and line width (so emitted Lines / Rects carry their drawing width).
// Color, clipping path, dash pattern etc. are parsed by the operator
// dispatcher so the stack stays balanced, but we don't retain them —
// adding those fields later is a non-breaking change.
type GraphicsState struct {
	CTM       Matrix  // Current transformation matrix.
	LineWidth float64 // From `w`; user-space units.

	// Text state is part of the graphics state per the PDF spec, so it
	// participates in the same q/Q stack. We keep it inline rather than
	// as a separate field-of-fields because the q/Q hot path copies the
	// whole struct; one flat struct is one mempcpy.
	Text TextState
}

// TextState is the subset of PDF text-state parameters we need to
// position glyphs and resolve their font. It is mutated only by text-
// state operators (Tc Tw Tz TL Tf Tm Td TD T* Trise) and consulted by
// every text-showing op (Tj TJ ' ").
//
// The text matrix and text line matrix are part of the text OBJECT
// (BT/ET), not the text state — but we keep them on this struct anyway
// because operator dispatch is more uniform that way, and BT/ET just
// zero them out as if they were any other text-state field.
type TextState struct {
	Font      *Font   // Selected by Tf; nil before any Tf is seen.
	FontSize  float64 // From Tf (second operand).
	CharSpace float64 // Tc; added to every glyph advance.
	WordSpace float64 // Tw; added after every space (cid 32) in simple fonts.
	Scale     float64 // Tz / 100; horizontal text scaling factor (1.0 = normal).
	Leading   float64 // TL; negated so it represents the y-delta on T*.
	Rise      float64 // Trise; vertical offset for super/subscript.
	Render    int     // Tr; 0 = fill (default), 3 = invisible.

	// Text matrix: maps text-space coordinates to user space. Set by Tm,
	// translated by Td/TD/T*, mutated by every glyph emission. BT zeros
	// this back to identity.
	Matrix Matrix

	// Text line matrix: snapshotted at the start of each line. Td/TD/T*
	// reset Matrix from LineMatrix; glyph emission only mutates Matrix.
	LineMatrix Matrix
}

// reset re-initialises a TextState to its post-BT defaults. The PDF
// spec is unambiguous about what these are (PDF 1.7 §9.4.1): every
// text-state value is preserved across BT/ET except for Matrix and
// LineMatrix, which are reset to the identity at every BT.
func (t *TextState) resetForBT() {
	t.Matrix = IdentityMatrix
	t.LineMatrix = IdentityMatrix
}

// newDefaultGraphicsState returns the state that exists at the start
// of a content stream, before any operator has run. The CTM is the
// device transform that the caller (page renderer) configures — this
// constructor returns identity; the caller is expected to overwrite
// CTM with the page's coordinate transform before stepping the
// interpreter.
func newDefaultGraphicsState() GraphicsState {
	return GraphicsState{
		CTM:       IdentityMatrix,
		LineWidth: 1.0,
		Text: TextState{
			Scale:      1.0,
			Matrix:     IdentityMatrix,
			LineMatrix: IdentityMatrix,
		},
	}
}
