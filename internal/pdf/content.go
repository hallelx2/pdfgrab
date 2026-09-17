// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdf

import (
	"errors"
	"strconv"
	"strings"
)

// Interpreter walks a PDF content stream, maintaining the graphics
// and text state, and emits typed events (glyphs and path paints)
// to a Sink callback supplied by the caller. One Interpreter is used
// per page; resetting requires constructing a new one.
//
// The design follows pdfminer.six's PDFContentEmitter / PDFTextDevice
// split, with two changes:
//
//  1. The "device" is just a Sink interface that receives Char and
//     paint-path events. No subclassing, no method override mess —
//     we don't need pdfminer's layered architecture because we're
//     not implementing a separate visual renderer.
//  2. The interpreter is purely synchronous and single-threaded. PDF
//     content streams don't have any constructs that benefit from
//     concurrency, and a single-threaded loop is much easier to
//     reason about when porting glyph-position math from Python.
type Interpreter struct {
	// state is the active graphics state. Mutated in place by every
	// operator. The q/Q stack pushes copies of this struct.
	state GraphicsState

	// stack is the q/Q stack of graphics-state snapshots. A `q`
	// pushes the current state; `Q` pops and replaces.
	stack []GraphicsState

	// Fonts is the page's font map (/Font subtree of resources),
	// indexed by the resource name used in Tf operands. Set by the
	// caller before Run.
	Fonts map[string]*Font

	// XObjects is the page's XObject map (/XObject subtree). Used
	// only for `Do`-invoked Form XObjects whose content streams are
	// inlined into the page; image XObjects are recognised and
	// dropped.
	XObjects map[string]XObject

	// Sink receives emitted events. The caller installs whatever
	// implementation it likes — pdfgrab's Page uses a struct that
	// accumulates Chars and paints into separate slices.
	Sink Sink

	// curPath holds the in-flight path under construction. Each
	// path-painting operator (S, f, B, n, …) drains this slice.
	curPath []PathSeg

	// pathStart is the start point of the current subpath, recorded
	// for `h` (close subpath). The PDF spec only requires us to track
	// this for the most recent `m` — multi-subpath paths reset it on
	// every new `m`.
	pathStart [2]float64
}

// Sink receives high-level events from the content interpreter. It is
// implemented by the page-builder in the parent package; per pdfgrab's
// package boundary, this is the contract between the parser and the
// public-API layer.
type Sink interface {
	// EmitChar is called once per glyph drawn by Tj/TJ/'/" operators.
	// All coordinates are already in user space (post text matrix and
	// CTM), with y0 <= y1 and x0 <= x1. Text is the glyph's Unicode
	// (may be empty).
	EmitChar(ev CharEvent)

	// EmitPath is called once per path-painting operator. The path
	// has already been classified into a stroke / fill flag pair and
	// a slice of segments; the sink converts segments into Lines,
	// Rects, or Curves at its discretion.
	EmitPath(ev PathEvent)
}

// CharEvent is the per-glyph data emitted by EmitChar.
type CharEvent struct {
	Text           string
	X0, Y0, X1, Y1 float64
	FontName       string
	FontSize       float64
	Upright        bool
	Advance        float64
}

// PathEvent is the data emitted by EmitPath when a path-painting op
// (S/s/f/F/f*/B/B*/b/b*/n) drains the current path. The Segments
// slice owns its memory after the call returns — the interpreter
// will not modify it.
type PathEvent struct {
	Segments  []PathSeg
	Stroke    bool
	Fill      bool
	EvenOdd   bool
	LineWidth float64
}

// PathSeg is one segment of a path. Op is "m", "l", "c", "h", or "re".
type PathSeg struct {
	Op string

	// X, Y are populated for m, l, and re's first corner.
	X, Y float64

	// For curves (c, v, y) we record up to three control/endpoint pairs.
	// pdfplumber's flattened representation only keeps the endpoint
	// (X3, Y3); we keep all of them so callers that want true Bezier
	// reconstruction can do it.
	X1, Y1, X2, Y2, X3, Y3 float64

	// For re: width and height (already absolute, in user space).
	W, H float64
}

// XObject is a Form/Image XObject reachable from the page. When the
// interpreter encounters `name Do`, it looks the name up here; if the
// subtype is "Form", the contained content stream is interpreted
// recursively under the current CTM (which is multiplied by the
// XObject's /Matrix).
type XObject struct {
	Subtype string // "Form" or "Image"

	// For Form XObjects:
	Content []byte
	BBox    [4]float64
	Matrix  Matrix

	// Resources from the XObject's /Resources dict, if any. Form
	// XObjects can carry their own resource scope; if absent, the
	// PDF spec says we inherit the enclosing page's resources.
	Fonts    map[string]*Font
	XObjects map[string]XObject
}

// Operand is one parsed value from the content-stream operand stack.
// The interpreter handlers cast it to the type they expect (e.g. Tf
// pops a name then a number). We use a single sum type rather than
// individual Push methods because the operand grammar is uniform —
// every operator just pops N typed values — and Operand keeps the
// handler signatures tidy.
type Operand struct {
	Kind   OperandKind
	Number float64
	Name   string    // for name and keyword operands
	String []byte    // for literal/hex strings
	Array  []Operand // for [ ... ] arrays (TJ uses this)
}

type OperandKind int

const (
	OpNumber OperandKind = iota
	OpName
	OpString
	OpArray
)

// NewInterpreter returns a fresh interpreter ready to walk a content
// stream. The initial CTM is the device transform supplied by the
// caller (typically identity composed with the page rotation and
// mediabox-origin translation).
func NewInterpreter(initialCTM Matrix, sink Sink) *Interpreter {
	gs := newDefaultGraphicsState()
	gs.CTM = initialCTM
	return &Interpreter{
		state: gs,
		Sink:  sink,
	}
}

// Run lexes and dispatches a single content stream. Multiple content
// streams on the same page are run sequentially against the same
// interpreter instance — this matches how the PDF spec defines them:
// the array `/Contents [a b c]` is semantically `a ++ b ++ c`, with
// q/Q stack state preserved across the splits.
func (it *Interpreter) Run(stream []byte) error {
	lex := newContentLexer(stream)
	var args []Operand
	for {
		tok, ok := lex.next()
		if !ok {
			break
		}
		switch tok.kind {
		case ctokKeyword:
			h, found := getOperatorTable()[tok.text]
			if !found {
				// Unknown operator: drop its operands and continue.
				// PDFs in the wild occasionally carry private
				// extensions; ignoring them is safer than erroring.
				args = args[:0]
				continue
			}
			if err := h(it, args); err != nil {
				return err
			}
			args = args[:0]
		case ctokNumber:
			n, _ := strconv.ParseFloat(tok.text, 64)
			args = append(args, Operand{Kind: OpNumber, Number: n})
		case ctokName:
			args = append(args, Operand{Kind: OpName, Name: tok.text})
		case ctokString:
			args = append(args, Operand{Kind: OpString, String: tok.raw})
		case ctokArrayBegin:
			arr, err := lex.readArray()
			if err != nil {
				return err
			}
			args = append(args, Operand{Kind: OpArray, Array: arr})
		}
	}
	return nil
}

// --- Operator handlers ------------------------------------------------------

func opSaveState(it *Interpreter, _ []Operand) error {
	it.stack = append(it.stack, it.state)
	return nil
}

func opRestoreState(it *Interpreter, _ []Operand) error {
	if n := len(it.stack); n > 0 {
		it.state = it.stack[n-1]
		it.stack = it.stack[:n-1]
	}
	return nil
}

func opConcatMatrix(it *Interpreter, args []Operand) error {
	if len(args) < 6 {
		return nil
	}
	m := Matrix{
		args[0].Number, args[1].Number,
		args[2].Number, args[3].Number,
		args[4].Number, args[5].Number,
	}
	it.state.CTM = Mult(m, it.state.CTM)
	return nil
}

func opLineWidth(it *Interpreter, args []Operand) error {
	if len(args) < 1 {
		return nil
	}
	// PDF spec: line width is in user space units before the CTM
	// scaling. We don't scale here — Sink callers want the raw
	// declared width because that's what they cross-check against
	// table-grid ruling thickness.
	it.state.LineWidth = args[0].Number
	return nil
}

// --- Path construction ----

func opMove(it *Interpreter, args []Operand) error {
	if len(args) < 2 {
		return nil
	}
	x, y := args[0].Number, args[1].Number
	it.pathStart = [2]float64{x, y}
	it.curPath = append(it.curPath, PathSeg{Op: "m", X: x, Y: y})
	return nil
}

func opLine(it *Interpreter, args []Operand) error {
	if len(args) < 2 {
		return nil
	}
	it.curPath = append(it.curPath, PathSeg{Op: "l", X: args[0].Number, Y: args[1].Number})
	return nil
}

func opCurve(it *Interpreter, args []Operand) error {
	if len(args) < 6 {
		return nil
	}
	it.curPath = append(it.curPath, PathSeg{
		Op: "c",
		X1: args[0].Number, Y1: args[1].Number,
		X2: args[2].Number, Y2: args[3].Number,
		X3: args[4].Number, Y3: args[5].Number,
	})
	return nil
}

func opCurveV(it *Interpreter, args []Operand) error {
	if len(args) < 4 {
		return nil
	}
	it.curPath = append(it.curPath, PathSeg{
		Op: "v",
		X2: args[0].Number, Y2: args[1].Number,
		X3: args[2].Number, Y3: args[3].Number,
	})
	return nil
}

func opCurveY(it *Interpreter, args []Operand) error {
	if len(args) < 4 {
		return nil
	}
	it.curPath = append(it.curPath, PathSeg{
		Op: "y",
		X1: args[0].Number, Y1: args[1].Number,
		X3: args[2].Number, Y3: args[3].Number,
	})
	return nil
}

func opClosePath(it *Interpreter, _ []Operand) error {
	it.curPath = append(it.curPath, PathSeg{Op: "h", X: it.pathStart[0], Y: it.pathStart[1]})
	return nil
}

func opRectangle(it *Interpreter, args []Operand) error {
	if len(args) < 4 {
		return nil
	}
	x, y, w, h := args[0].Number, args[1].Number, args[2].Number, args[3].Number
	it.curPath = append(it.curPath, PathSeg{Op: "re", X: x, Y: y, W: w, H: h})
	return nil
}

// --- Path painting ----

func opStroke(it *Interpreter, _ []Operand) error { return it.paintPath(true, false, false) }
func opCloseStroke(it *Interpreter, _ []Operand) error {
	_ = opClosePath(it, nil)
	return it.paintPath(true, false, false)
}
func opFill(it *Interpreter, _ []Operand) error        { return it.paintPath(false, true, false) }
func opFillEvenOdd(it *Interpreter, _ []Operand) error { return it.paintPath(false, true, true) }
func opFillStroke(it *Interpreter, _ []Operand) error  { return it.paintPath(true, true, false) }
func opFillStrokeEvenOdd(it *Interpreter, _ []Operand) error {
	return it.paintPath(true, true, true)
}
func opCloseFillStroke(it *Interpreter, _ []Operand) error {
	_ = opClosePath(it, nil)
	return it.paintPath(true, true, false)
}
func opCloseFillStrokeEvenOdd(it *Interpreter, _ []Operand) error {
	_ = opClosePath(it, nil)
	return it.paintPath(true, true, true)
}

func opEndPath(it *Interpreter, _ []Operand) error {
	it.curPath = it.curPath[:0]
	return nil
}

// paintPath transforms the in-flight path through the CTM, hands it to
// the sink, and clears it. Because path segments are stored in their
// pre-CTM (user-defined) coordinates, this is the moment we apply the
// transform — and it has to happen here, not at construction time,
// because `q ... cm ... Q` lets the CTM change mid-path. (In practice
// real generators rarely do that, but the spec allows it.)
func (it *Interpreter) paintPath(stroke, fill, evenOdd bool) error {
	if len(it.curPath) == 0 {
		return nil
	}
	if it.Sink == nil {
		it.curPath = it.curPath[:0]
		return nil
	}
	out := make([]PathSeg, len(it.curPath))
	for i, s := range it.curPath {
		out[i] = s
		switch s.Op {
		case "m", "l", "h":
			out[i].X, out[i].Y = ApplyPoint(it.state.CTM, s.X, s.Y)
		case "re":
			// Rectangle: transform two opposite corners then re-derive
			// width/height after the CTM has been applied. If the CTM
			// includes rotation the "rectangle" no longer is one in
			// world coords — we approximate by an axis-aligned bbox,
			// which is the same approximation pdfplumber makes.
			x0, y0 := ApplyPoint(it.state.CTM, s.X, s.Y)
			x1, y1 := ApplyPoint(it.state.CTM, s.X+s.W, s.Y+s.H)
			if x0 > x1 {
				x0, x1 = x1, x0
			}
			if y0 > y1 {
				y0, y1 = y1, y0
			}
			out[i].X, out[i].Y = x0, y0
			out[i].W, out[i].H = x1-x0, y1-y0
		case "c":
			out[i].X1, out[i].Y1 = ApplyPoint(it.state.CTM, s.X1, s.Y1)
			out[i].X2, out[i].Y2 = ApplyPoint(it.state.CTM, s.X2, s.Y2)
			out[i].X3, out[i].Y3 = ApplyPoint(it.state.CTM, s.X3, s.Y3)
		case "v":
			out[i].X2, out[i].Y2 = ApplyPoint(it.state.CTM, s.X2, s.Y2)
			out[i].X3, out[i].Y3 = ApplyPoint(it.state.CTM, s.X3, s.Y3)
		case "y":
			out[i].X1, out[i].Y1 = ApplyPoint(it.state.CTM, s.X1, s.Y1)
			out[i].X3, out[i].Y3 = ApplyPoint(it.state.CTM, s.X3, s.Y3)
		}
	}
	it.Sink.EmitPath(PathEvent{
		Segments: out, Stroke: stroke, Fill: fill, EvenOdd: evenOdd,
		LineWidth: it.state.LineWidth,
	})
	it.curPath = it.curPath[:0]
	return nil
}

// --- Text state ----

func opTc(it *Interpreter, args []Operand) error {
	if len(args) > 0 {
		it.state.Text.CharSpace = args[0].Number
	}
	return nil
}
func opTw(it *Interpreter, args []Operand) error {
	if len(args) > 0 {
		it.state.Text.WordSpace = args[0].Number
	}
	return nil
}
func opTz(it *Interpreter, args []Operand) error {
	if len(args) > 0 {
		// PDF Tz operand is a percentage; we store as a multiplier so
		// it can plug into width math without a /100 everywhere.
		it.state.Text.Scale = args[0].Number / 100.0
	}
	return nil
}
func opTL(it *Interpreter, args []Operand) error {
	if len(args) > 0 {
		// PDF TL convention: leading is the POSITIVE value, but T*
		// moves DOWN, so we store the negative so the math is just
		// "Translate(textMatrix, 0, leading)".
		it.state.Text.Leading = -args[0].Number
	}
	return nil
}
func opTf(it *Interpreter, args []Operand) error {
	if len(args) < 2 {
		return nil
	}
	name := args[0].Name
	size := args[1].Number
	it.state.Text.Font = it.Fonts[name]
	it.state.Text.FontSize = size
	return nil
}
func opTr(it *Interpreter, args []Operand) error {
	if len(args) > 0 {
		it.state.Text.Render = int(args[0].Number)
	}
	return nil
}
func opTrise(it *Interpreter, args []Operand) error {
	if len(args) > 0 {
		it.state.Text.Rise = args[0].Number
	}
	return nil
}

// --- Text objects ----

func opBeginText(it *Interpreter, _ []Operand) error {
	it.state.Text.resetForBT()
	return nil
}
func opEndText(it *Interpreter, _ []Operand) error {
	// ET is otherwise inert — there's nothing to flush because every
	// glyph is emitted as it's drawn.
	return nil
}

func opTd(it *Interpreter, args []Operand) error {
	if len(args) < 2 {
		return nil
	}
	tx, ty := args[0].Number, args[1].Number
	// Translate the line matrix; the text matrix is reset to the
	// translated line matrix. This is the PDF spec's literal wording
	// for Td (§9.4.2): "Move to the start of the next line, offset
	// from the start of the current line by (tx, ty)."
	it.state.Text.LineMatrix = Translate(it.state.Text.LineMatrix, tx, ty)
	it.state.Text.Matrix = it.state.Text.LineMatrix
	return nil
}

func opTD(it *Interpreter, args []Operand) error {
	if len(args) < 2 {
		return nil
	}
	tx, ty := args[0].Number, args[1].Number
	// TD = "set leading to -ty, then perform Td". This is the only
	// op that mutates leading as a side effect, which is why PDFs
	// emitted by Word call it after every "carriage return" in
	// running prose.
	it.state.Text.Leading = ty
	it.state.Text.LineMatrix = Translate(it.state.Text.LineMatrix, tx, ty)
	it.state.Text.Matrix = it.state.Text.LineMatrix
	return nil
}

func opTm(it *Interpreter, args []Operand) error {
	if len(args) < 6 {
		return nil
	}
	m := Matrix{
		args[0].Number, args[1].Number,
		args[2].Number, args[3].Number,
		args[4].Number, args[5].Number,
	}
	it.state.Text.Matrix = m
	it.state.Text.LineMatrix = m
	return nil
}

func opTStar(it *Interpreter, _ []Operand) error {
	// T* = "move to next line at current leading". Same as Td(0, leading).
	it.state.Text.LineMatrix = Translate(it.state.Text.LineMatrix, 0, it.state.Text.Leading)
	it.state.Text.Matrix = it.state.Text.LineMatrix
	return nil
}

// --- Text showing ----

func opTj(it *Interpreter, args []Operand) error {
	if len(args) < 1 || args[0].Kind != OpString {
		return nil
	}
	it.showString(args[0].String)
	return nil
}

// opTJ handles TJ — an array of strings interleaved with number
// kerning operands. The PDF spec (§9.4.3) is precise: each number is
// subtracted from the horizontal text-space position, scaled by
// (-fontSize/1000) so positive numbers move LEFTWARDS (tighter
// kerning).
func opTJ(it *Interpreter, args []Operand) error {
	if len(args) < 1 || args[0].Kind != OpArray {
		return nil
	}
	for _, elem := range args[0].Array {
		switch elem.Kind {
		case OpString:
			it.showString(elem.String)
		case OpNumber:
			// Negative number == widen gap; positive == tighten.
			// dxscale follows pdfminer's formula.
			adj := -elem.Number * 0.001 * it.state.Text.FontSize * it.state.Text.Scale
			it.state.Text.Matrix = Translate(it.state.Text.Matrix, adj, 0)
		}
	}
	return nil
}

func opQuote(it *Interpreter, args []Operand) error {
	// ' = move to next line, then show.
	_ = opTStar(it, nil)
	return opTj(it, args)
}

func opDoubleQuote(it *Interpreter, args []Operand) error {
	// " = aw ac string '. The first two operands set Tw/Tc.
	if len(args) < 3 {
		return nil
	}
	it.state.Text.WordSpace = args[0].Number
	it.state.Text.CharSpace = args[1].Number
	_ = opTStar(it, nil)
	return opTj(it, []Operand{args[2]})
}

// --- XObject invocation ----

func opDo(it *Interpreter, args []Operand) error {
	if len(args) < 1 || args[0].Kind != OpName {
		return nil
	}
	xo, ok := it.XObjects[args[0].Name]
	if !ok || xo.Subtype != "Form" {
		return nil
	}
	// Form XObjects nest: their content stream runs under (XObject.Matrix * CTM),
	// the q/Q stack is saved/restored around the call, and the XObject's
	// own /Resources (if any) shadow the page's for the duration. We
	// build a fresh sub-interpreter rather than mutating the current one
	// so that q/Q misuse inside the XObject can't leak out.
	subCTM := Mult(xo.Matrix, it.state.CTM)
	sub := NewInterpreter(subCTM, it.Sink)
	if xo.Fonts != nil {
		sub.Fonts = xo.Fonts
	} else {
		sub.Fonts = it.Fonts
	}
	if xo.XObjects != nil {
		sub.XObjects = xo.XObjects
	} else {
		sub.XObjects = it.XObjects
	}
	return sub.Run(xo.Content)
}

// --- The text-emission core ---

// showString is the heart of glyph emission: it decodes a single
// string operand into CIDs via the current font, computes each glyph's
// bbox via the combined text matrix × CTM, emits a CharEvent, and
// advances the text matrix by the glyph's width plus character spacing.
//
// This function is a direct port of pdfminer's
// PDFTextDevice.render_string_horizontal (we don't implement vertical
// writing — see ErrUnsupported handling in the parent package).
func (it *Interpreter) showString(s []byte) {
	t := &it.state.Text
	font := t.Font
	if font == nil {
		return
	}
	if it.Sink == nil {
		return
	}

	fontSize := t.FontSize
	scale := t.Scale
	charSpace := t.CharSpace * scale
	wordSpace := t.WordSpace * scale
	rise := t.Rise

	// composite fonts ignore wordspace per spec (Tw applies to single-
	// byte CIDs whose byte value is 32; multi-byte CIDs never satisfy
	// that test).
	if !font.IsSimple {
		wordSpace = 0
	}

	// dxscale converts font-design-unit widths to user-space advances.
	dxScale := 0.001 * fontSize * scale

	cids := font.Decode(s)
	for _, cid := range cids {
		// Combined transform: text → user space.
		combined := Mult(t.Matrix, it.state.CTM)
		// Glyph bbox in font design units, then user space.
		//
		// Descent is stored in /1000ths of an em, so it has to be scaled
		// by BOTH 0.001 and the font size to reach text space -- the same
		// two factors dxScale applies to the advance width. pdfminer.six
		// does this as get_descent() * fontsize, where get_descent() is
		// already descent/1000. Omitting fontSize made the descender
		// contribution a fixed fraction of a point instead of a fraction
		// of the glyph, which at 12pt is off by 12x.
		descent := font.Descent * 0.001 * fontSize
		adv := font.CharWidth(cid) * dxScale
		bbox := [4]float64{0, descent + rise, adv, descent + rise + fontSize}
		x0, y0, x1, y1 := ApplyRect(combined,
			bbox[0], bbox[1], bbox[2], bbox[3])
		if x0 > x1 {
			x0, x1 = x1, x0
		}
		if y0 > y1 {
			y0, y1 = y1, y0
		}

		// Upright = a*d*scale > 0 AND b*c <= 0 (pdfminer convention).
		a, b, c, d := combined[0], combined[1], combined[2], combined[3]
		upright := a*d*scale > 0 && b*c <= 0

		text := font.DecodeUnicode(cid)

		it.Sink.EmitChar(CharEvent{
			Text:     text,
			X0:       x0,
			Y0:       y0,
			X1:       x1,
			Y1:       y1,
			FontName: font.BaseFont,
			FontSize: fontSize,
			Upright:  upright,
			Advance:  adv,
		})

		// Advance the text matrix. For horizontal writing, the
		// advance is in text-space X. Character spacing is added
		// AFTER the glyph and before the next one (PDF spec §9.4.4).
		advance := adv + charSpace
		if cid == 32 && wordSpace != 0 {
			advance += wordSpace
		}
		t.Matrix = Translate(t.Matrix, advance/scaleOrOne(scale), 0)
	}
}

// scaleOrOne avoids division by zero if a malformed PDF declares
// /Tz 0. The PDF spec doesn't allow zero scale, but real PDFs do
// weird things and the alternative is a NaN that breaks the rest of
// the page.
func scaleOrOne(s float64) float64 {
	if s == 0 {
		return 1.0
	}
	return s
}

// --- Content-stream lexer -----------------------------------------------------
//
// PDF content streams use the same PostScript-like grammar as the rest
// of the format: whitespace-separated tokens, with `<...>` for hex
// strings, `(...)` for literal strings (with `\\` escapes and balanced
// parens), `/name` for names, `[...]` for arrays, and bare-word
// keywords for everything else. We implement just enough of it to feed
// the interpreter; the lexer is a recursive-descent reader that the
// interpreter calls via next() / readArray().

type ctokKind int

const (
	ctokInvalid ctokKind = iota
	ctokNumber
	ctokName
	ctokString
	ctokKeyword
	ctokArrayBegin
	ctokArrayEnd
	ctokDictBegin
	ctokDictEnd
)

type ctok struct {
	kind ctokKind
	text string
	raw  []byte
}

type contentLexer struct {
	data []byte
	i    int
}

func newContentLexer(data []byte) *contentLexer {
	return &contentLexer{data: data}
}

func (l *contentLexer) next() (ctok, bool) {
	for l.i < len(l.data) {
		c := l.data[l.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			l.i++
		case c == '%':
			for l.i < len(l.data) && l.data[l.i] != '\n' && l.data[l.i] != '\r' {
				l.i++
			}
		case c == '<':
			if l.i+1 < len(l.data) && l.data[l.i+1] == '<' {
				l.i += 2
				return ctok{kind: ctokDictBegin}, true
			}
			// Hex string.
			j := l.i + 1
			for j < len(l.data) && l.data[j] != '>' {
				j++
			}
			raw := hexDecode(l.data[l.i+1 : j])
			if j < len(l.data) {
				l.i = j + 1
			} else {
				l.i = j
			}
			return ctok{kind: ctokString, raw: raw}, true
		case c == '>':
			if l.i+1 < len(l.data) && l.data[l.i+1] == '>' {
				l.i += 2
				return ctok{kind: ctokDictEnd}, true
			}
			l.i++
		case c == '(':
			raw, n := readLiteralString(l.data[l.i:])
			l.i += n
			return ctok{kind: ctokString, raw: raw}, true
		case c == '[':
			l.i++
			return ctok{kind: ctokArrayBegin}, true
		case c == ']':
			l.i++
			return ctok{kind: ctokArrayEnd}, true
		case c == '/':
			j := l.i + 1
			for j < len(l.data) && !isContentDelim(l.data[j]) {
				j++
			}
			name := string(l.data[l.i+1 : j])
			l.i = j
			return ctok{kind: ctokName, text: name}, true
		case isDigit(c) || c == '-' || c == '+' || c == '.':
			j := l.i + 1
			for j < len(l.data) && !isContentDelim(l.data[j]) {
				j++
			}
			text := string(l.data[l.i:j])
			l.i = j
			if _, err := strconv.ParseFloat(text, 64); err == nil {
				return ctok{kind: ctokNumber, text: text}, true
			}
			// PDF allows operator names with `*` and `'` and `"` —
			// they're all printable non-delimiter chars, so they fall
			// through here. Treat as keyword.
			return ctok{kind: ctokKeyword, text: text}, true
		default:
			j := l.i
			for j < len(l.data) && !isContentDelim(l.data[j]) {
				j++
			}
			if j > l.i {
				text := string(l.data[l.i:j])
				l.i = j
				return ctok{kind: ctokKeyword, text: text}, true
			}
			l.i++
		}
	}
	return ctok{}, false
}

// readArray collects operands until the matching `]`. Used by TJ.
// Nested arrays are NOT recursively assembled — the TJ array is flat
// in practice — but we'd handle nesting fine if it appeared.
func (l *contentLexer) readArray() ([]Operand, error) {
	var out []Operand
	for {
		tok, ok := l.next()
		if !ok {
			return nil, errors.New("content: unterminated array")
		}
		switch tok.kind {
		case ctokArrayEnd:
			return out, nil
		case ctokNumber:
			n, _ := strconv.ParseFloat(tok.text, 64)
			out = append(out, Operand{Kind: OpNumber, Number: n})
		case ctokName:
			out = append(out, Operand{Kind: OpName, Name: tok.text})
		case ctokString:
			out = append(out, Operand{Kind: OpString, String: tok.raw})
		case ctokArrayBegin:
			sub, err := l.readArray()
			if err != nil {
				return nil, err
			}
			out = append(out, Operand{Kind: OpArray, Array: sub})
		}
	}
}

func isContentDelim(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' ||
		c == '<' || c == '>' || c == '[' || c == ']' ||
		c == '(' || c == ')' || c == '/' || c == '%' || c == '{' || c == '}'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// readLiteralString parses a (foo) literal string, returning the raw
// bytes (after backslash escape processing) and the number of input
// bytes consumed (including the closing paren).
//
// PDF literal-string escapes (PDF 1.7 §7.3.4.2):
//
//	\n \r \t \b \f → control chars.
//	\\ \( \)        → literal char.
//	\ddd            → octal byte (1–3 octal digits).
//	\<newline>      → line continuation, dropped.
func readLiteralString(data []byte) ([]byte, int) {
	if len(data) == 0 || data[0] != '(' {
		return nil, 0
	}
	var sb strings.Builder
	depth := 1
	i := 1
	for i < len(data) && depth > 0 {
		c := data[i]
		switch c {
		case '\\':
			if i+1 >= len(data) {
				i++
				continue
			}
			next := data[i+1]
			switch {
			case next == 'n':
				sb.WriteByte('\n')
				i += 2
			case next == 'r':
				sb.WriteByte('\r')
				i += 2
			case next == 't':
				sb.WriteByte('\t')
				i += 2
			case next == 'b':
				sb.WriteByte('\b')
				i += 2
			case next == 'f':
				sb.WriteByte('\f')
				i += 2
			case next == '(' || next == ')' || next == '\\':
				sb.WriteByte(next)
				i += 2
			case next == '\r':
				if i+2 < len(data) && data[i+2] == '\n' {
					i += 3
				} else {
					i += 2
				}
			case next == '\n':
				i += 2
			case next >= '0' && next <= '7':
				// 1–3 octal digits.
				v := int(next - '0')
				j := i + 2
				for k := 0; k < 2 && j < len(data) && data[j] >= '0' && data[j] <= '7'; k++ {
					v = v*8 + int(data[j]-'0')
					j++
				}
				sb.WriteByte(byte(v))
				i = j
			default:
				sb.WriteByte(next)
				i += 2
			}
		case '(':
			depth++
			sb.WriteByte(c)
			i++
		case ')':
			depth--
			if depth > 0 {
				sb.WriteByte(c)
			}
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return []byte(sb.String()), i
}
