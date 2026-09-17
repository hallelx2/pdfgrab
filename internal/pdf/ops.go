// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdf

// This file defines the operator dispatch surface for the PDF content
// stream interpreter. Each PDF content operator (Tj, TJ, cm, q, re, …)
// has a handler function defined in content.go; this file owns the
// operator table itself plus the small evaluator that converts a
// stack of typed operands into typed handler arguments.
//
// We split the dispatch table out of content.go so that adding a new
// operator is a one-line change in the table and a one-function change
// in content.go — no need to navigate around a huge switch statement.
// pdfminer's PDFContentEmitter uses Python's getattr() magic to do the
// same; we get there with a static map.

// opHandler is the signature of every operator handler. The handler
// receives the interpreter (for state and emit callbacks) and the
// list of operands the parser collected since the previous operator.
// Returning an error stops interpretation of the current content
// stream; the page can still surface whatever objects were emitted
// before the failure.
type opHandler func(it *Interpreter, args []Operand) error

// operatorTable maps every PDF content-stream operator token to its
// handler. Operators we don't currently care about (color, graphics-
// state name resolution that doesn't affect text positioning, clipping
// path, etc.) are still in the table — they just consume their
// operands and return nil. This keeps the parser's operand stack
// balanced no matter what the document throws at us.
//
// The list is exhaustive per PDF 1.7 §A.2 except for inline images
// (`BI ... ID ... EI`) which are skipped specially by the lexer
// because their operand grammar is irregular.
//
// Built lazily by getOperatorTable() to break the initialisation cycle
// (opDo → Run → operatorTable).
var operatorTable map[string]opHandler

func getOperatorTable() map[string]opHandler {
	if operatorTable != nil {
		return operatorTable
	}
	operatorTable = buildOperatorTable()
	return operatorTable
}

func buildOperatorTable() map[string]opHandler {
	return map[string]opHandler{
		// --- Graphics state ----
		"q":  opSaveState,
		"Q":  opRestoreState,
		"cm": opConcatMatrix,
		"w":  opLineWidth,
		"J":  opNoop, // line cap
		"j":  opNoop, // line join
		"M":  opNoop, // miter limit
		"d":  opNoop, // line dash
		"ri": opNoop, // rendering intent
		"i":  opNoop, // flatness
		"gs": opNoop, // gstate dict (name)

		// --- Path construction ----
		"m":  opMove,
		"l":  opLine,
		"c":  opCurve,
		"v":  opCurveV,
		"y":  opCurveY,
		"h":  opClosePath,
		"re": opRectangle,

		// --- Path painting ----
		"S":  opStroke,
		"s":  opCloseStroke,
		"f":  opFill,
		"F":  opFill, // obsolete alias
		"f*": opFillEvenOdd,
		"B":  opFillStroke,
		"B*": opFillStrokeEvenOdd,
		"b":  opCloseFillStroke,
		"b*": opCloseFillStrokeEvenOdd,
		"n":  opEndPath,

		// --- Clipping ----
		"W":  opNoop,
		"W*": opNoop,

		// --- Text state ----
		"Tc": opTc,
		"Tw": opTw,
		"Tz": opTz,
		"TL": opTL,
		"Tf": opTf,
		"Tr": opTr,
		"Ts": opTrise,

		// --- Text objects & positioning ----
		"BT": opBeginText,
		"ET": opEndText,
		"Td": opTd,
		"TD": opTD,
		"Tm": opTm,
		"T*": opTStar,

		// --- Text showing ----
		"Tj": opTj,
		"TJ": opTJ,
		"'":  opQuote,       // move to next line and show.
		"\"": opDoubleQuote, // set spacing, move, show.

		// --- Colour (we don't track but pop the operands) ----
		"CS": opNoop, "cs": opNoop,
		"SC": opNoop, "sc": opNoop,
		"SCN": opNoop, "scn": opNoop,
		"G": opNoop, "g": opNoop,
		"RG": opNoop, "rg": opNoop,
		"K": opNoop, "k": opNoop,
		"sh": opNoop,

		// --- XObject invocation ----
		"Do": opDo,

		// --- Marked content (passthrough) ----
		"BMC": opNoop, "BDC": opNoop, "EMC": opNoop,
		"MP": opNoop, "DP": opNoop,

		// --- Compatibility ----
		"BX": opNoop, "EX": opNoop,
	}
}

// opNoop is the do-nothing handler. The parser already consumed the
// operands off the stack before dispatching, so we just return.
func opNoop(it *Interpreter, args []Operand) error { return nil }
