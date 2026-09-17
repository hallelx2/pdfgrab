// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdfgrab

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// This file is the Go port of pdfplumber/utils/text.py — the word and
// text-extraction layer that turns a slice of positioned Chars into
// reading-order Words and a string of text. Three exported entry
// points hang off Page (defined below):
//
//   page.Words(opts)        →  []Word
//   page.ExtractText(opts)  →  string
//   page.ExtractTextSimple(...)  →  convenience wrapper (no layout)
//
// The algorithm is faithful to pdfplumber's WordExtractor: cluster
// chars into lines by Y, then walk each line left-to-right merging
// adjacent chars whose horizontal gap is within XTolerance. The
// coordinate-system inversion (pdfplumber's image space has Y growing
// down; we use PDF user space with Y growing up) is handled here by
// clustering on Y1 (visual top of the glyph) and using Y1 / X0 as
// sort keys.
//
// Defaults match pdfplumber:
//   XTolerance = 3, YTolerance = 3
//   Direction  = "ltr", HorizontalLTR = true, VerticalTTB = true
//   Expand     = true (ligature expansion on by default)

// Word is one extracted text run. It bundles the assembled string and
// the bbox of the constituent chars, plus enough metadata for callers
// who want to filter/restyle on font properties (font name + size of
// the first char) or know which direction the run reads.
//
// Field names map onto pdfplumber's word dict the way the rest of the
// package maps onto its char dict: X0/Y0/X1/Y1 instead of "x0"/"top"/
// "x1"/"bottom". Y0 is the descender (lower edge of the lowest glyph
// in the run); Y1 is the ascender (upper edge of the tallest glyph).
type Word struct {
	// Text is the concatenated Unicode payload of the run. Ligature
	// glyphs are expanded into their constituent characters when
	// WordOpts.Expand is true (the default), so "ﬁle" appears as
	// "file" in the output.
	Text string

	// Bounding box of the run in PDF user space (origin at bottom-
	// left, Y growing up). The bbox is the union of every char's bbox
	// in this run.
	X0, Y0, X1, Y1 float64

	// Upright is true if every char in the run was drawn in normal
	// reading orientation. We don't merge upright and rotated chars
	// into the same Word — they end up in different runs.
	Upright bool

	// Direction is one of "ltr", "rtl", "ttb", "btt". Most words on
	// most pages are "ltr"; rotated stamps may be "ttb"; Arabic/Hebrew
	// content is "rtl". The value is the direction the chars were
	// READ, not the direction they were drawn.
	Direction string

	// FontName / FontSize are copied from the first char in the run.
	// pdfplumber does the same — if a word straddles a font change,
	// only the leading font is reported, but in practice such words
	// are rare because changing font emits a new BT/ET pair which
	// breaks the run boundary at the content-stream level.
	FontName string
	FontSize float64

	// Chars is the slice of Char objects this word was assembled from.
	// Populated only when WordOpts.KeepChars is true (it costs O(n)
	// memory per word so we default to off). Useful for callers that
	// want to map word substrings back to glyph positions (highlight,
	// search) or to filter further by per-char attributes.
	Chars []Char
}

// Width returns x1 - x0.
func (w Word) Width() float64 { return w.X1 - w.X0 }

// Height returns y1 - y0.
func (w Word) Height() float64 { return w.Y1 - w.Y0 }

// WordOpts configures Page.Words. The zero value is NOT useful — call
// DefaultWordOpts() to get a populated struct with pdfplumber-compatible
// defaults, then override the fields you care about.
//
// Naming matches pdfplumber's WordExtractor kwargs where possible
// (XTolerance → x_tolerance, KeepBlankChars → keep_blank_chars, etc.)
// to make porting examples between the two libraries straightforward.
type WordOpts struct {
	// XTolerance is the maximum horizontal gap (in PDF points) between
	// adjacent chars that still get merged into the same word.
	// Default: 3.
	XTolerance float64

	// XToleranceRatio, when > 0, makes the horizontal word-gap tolerance
	// SIZE-RELATIVE instead of a fixed number of points: the effective
	// tolerance for a pair of glyphs becomes XToleranceRatio × the
	// previous glyph's font size. This generalizes word splitting across
	// documents and across font sizes WITHIN a document — the gap that
	// separates words scales with type size, so a single absolute value
	// (e.g. 3pt) over-merges small body text while a small value
	// over-splits large headings. Mirrors pdfplumber's x_tolerance_ratio.
	// When 0 (the WordExtractor default), the fixed XTolerance is used.
	XToleranceRatio float64

	// YTolerance is the maximum vertical jitter between chars that
	// still get clustered onto the same line. Default: 3.
	YTolerance float64

	// KeepBlankChars: when false (the default), space chars in the
	// content stream are dropped before word grouping — the word
	// boundary is inferred from the gap, not from the explicit space.
	// Set to true to preserve them (e.g. for diff-style line
	// reconstruction).
	KeepBlankChars bool

	// UseExplicitSpaces: when true, whitespace glyphs in the content
	// stream are treated as authoritative word boundaries (the PDF is
	// telling us exactly where words break) instead of being dropped and
	// re-inferred from inter-glyph gaps. This is strictly more robust than
	// pure gap heuristics for the many PDFs that DO emit real space glyphs
	// — the size-relative gap check then only has to carry the PDFs that
	// space via positioning. The spaces are used as separators only; they
	// are never emitted into the word text. Off in the raw WordExtractor
	// (pdfplumber parity); on in DefaultTextOpts.
	UseExplicitSpaces bool

	// UseTextFlow: when true, chars are processed in content-stream
	// order rather than re-sorted by position. This is faster and
	// often matches reading order in well-formed PDFs, but breaks for
	// PDFs that draw glyphs in random order (e.g. some scanner OCR
	// output).
	UseTextFlow bool

	// HorizontalLTR: when true (the default), upright text is read
	// left-to-right; when false, right-to-left. Setting this to false
	// is shorthand for Direction="rtl" but only for upright text.
	HorizontalLTR bool

	// VerticalTTB: when true (the default), rotated text is read top-
	// to-bottom; when false, bottom-to-top.
	VerticalTTB bool

	// ExtraAttrs is a list of Char field names that must match
	// EXACTLY for two chars to be merged into the same word. The
	// supported names are: "fontname", "size". Useful when a single
	// physical line has two runs that should be kept separate (e.g. a
	// bold caption followed by regular body text).
	ExtraAttrs []string

	// SplitAtPunctuation: when true, every ASCII punctuation char
	// (string.punctuation in Python) terminates the current word and
	// becomes its own one-char word. Default: false.
	SplitAtPunctuation bool

	// Expand: when true (the default), ligature glyphs (ﬁ, ﬂ, …) are
	// expanded into their constituent ASCII chars during text
	// assembly. The Char's text payload is preserved unchanged; only
	// the Word.Text string is expanded.
	Expand bool

	// KeepChars: when true, Word.Chars is populated with the source
	// chars. Off by default to save memory.
	KeepChars bool
}

// DefaultWordOpts returns a WordOpts populated with pdfplumber-matching
// defaults. Use this and override the fields you care about:
//
//	opts := pdfgrab.DefaultWordOpts()
//	opts.XTolerance = 1.5
//	words, _ := page.Words(opts)
func DefaultWordOpts() WordOpts {
	return WordOpts{
		XTolerance:    3,
		YTolerance:    3,
		HorizontalLTR: true,
		VerticalTTB:   true,
		Expand:        true,
		// On by default because pdfplumber does the same, and leaving it
		// off silently over-merges small type. pdfplumber's WordExtractor
		// ends a word AT a whitespace glyph, before any gap test runs;
		// only when there is no space glyph does it fall back to the gap.
		//
		// Without this, 8pt text merged into one run: the space is
		// 278/1000 x 8 = 2.22pt wide, under the 3pt XTolerance, so the
		// gap check saw no boundary even though the PDF had emitted an
		// explicit space. Body type in real documents is routinely 8-9pt,
		// so this was not an edge case.
		UseExplicitSpaces: true,
	}
}

// TextOpts configures Page.ExtractText. Like WordOpts the zero value
// is not useful; call DefaultTextOpts() for sensible defaults.
type TextOpts struct {
	XTolerance float64

	// XToleranceRatio makes the word-gap tolerance size-relative
	// (XToleranceRatio × glyph size) instead of a fixed number of points.
	// See WordOpts.XToleranceRatio. DefaultTextOpts enables it so text
	// extraction is robust across mixed-size documents out of the box.
	XToleranceRatio float64

	// UseExplicitSpaces honours the PDF's own whitespace glyphs as word
	// boundaries. See WordOpts.UseExplicitSpaces. Enabled by DefaultTextOpts.
	UseExplicitSpaces bool

	YTolerance float64

	// Layout: when true, the output preserves the page's spatial
	// layout — words at the same x-position appear in the same column
	// across lines, and lines that are far apart are separated by
	// extra newlines. When false (the default), output is dense:
	// words on the same line are joined with single spaces, lines
	// with "\n".
	Layout bool

	// LayoutWidthChars: when Layout=true, the total width of each
	// emitted line in characters. If 0, defaults to round(page.Width /
	// XDensity).
	LayoutWidthChars int

	// LayoutHeightChars: when Layout=true, the total number of
	// emitted lines (extra blank lines at the bottom pad to this
	// height). If 0, defaults to round(page.Height / YDensity).
	LayoutHeightChars int

	// XDensity / YDensity: PDF points per character / per line when
	// computing layout grid dimensions. Default values match
	// pdfplumber (XDensity=7.25, YDensity=13) — roughly the metrics
	// of 10pt Helvetica.
	XDensity float64
	YDensity float64

	// UseTextFlow / HorizontalLTR / VerticalTTB / ExtraAttrs are
	// passed through to the underlying WordExtractor.
	UseTextFlow   bool
	HorizontalLTR bool
	VerticalTTB   bool
	ExtraAttrs    []string

	// Expand passes through to WordOpts.Expand.
	Expand bool
}

// DefaultTextOpts returns pdfplumber-matching defaults.
func DefaultTextOpts() TextOpts {
	return TextOpts{
		XTolerance: 3,
		// Size-relative word gap: 0.15 × glyph size. At 10pt body that's
		// 1.5pt (splits tight typography correctly); at 24pt headings that
		// scales to 3.6pt (won't over-split). Generalizes across documents.
		XToleranceRatio:   0.15,
		YTolerance:        3,
		XDensity:          7.25,
		YDensity:          13,
		HorizontalLTR:     true,
		VerticalTTB:       true,
		Expand:            true,
		UseExplicitSpaces: true,
	}
}

// ligatures is the ligature-expansion table from pdfplumber.utils.text.
// Keys are the single Unicode code points produced by glyph names like
// "fi" / "ffi"; values are the expanded ASCII strings. The lookup is
// done on the char's text string, so ligatures encoded via ToUnicode
// CMaps that already expand them (e.g. some Adobe Reader-produced PDFs)
// pass through unchanged.
var ligatures = map[string]string{
	"ﬀ": "ff",
	"ﬁ": "fi",
	"ﬂ": "fl",
	"ﬃ": "ffi",
	"ﬄ": "ffl",
	"ﬅ": "st",
	"ﬆ": "st",
}

// expandLigatures returns the expansion of s if it's a ligature glyph,
// else s unchanged.
func expandLigatures(s string) string {
	if exp, ok := ligatures[s]; ok {
		return exp
	}
	return s
}

// asciiPunctuation is the set of ASCII characters string.punctuation
// (Python) covers: !"#$%&'()*+,-./:;<=>?@[\]^_`{|}~. SplitAtPunctuation
// terminates a word at every one of these.
const asciiPunctuation = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// isPunctRune reports whether r is in asciiPunctuation.
func isPunctRune(r rune) bool {
	if r > 127 {
		return false
	}
	return strings.ContainsRune(asciiPunctuation, r)
}

// extractWordsFromChars is the core word-grouping algorithm. It is
// pulled out of Page.Words so it can be exercised by unit tests on
// hand-crafted Char slices without spinning up a Page.
//
// Steps (port of WordExtractor.iter_extract_tuples):
//  1. If !KeepBlankChars, drop chars whose text is whitespace.
//  2. Filter out chars with empty text (PDF glyphs that failed to
//     resolve via encoding / ToUnicode). Their bbox can still inform
//     layout but they shouldn't participate in text assembly.
//  3. Group chars by (upright, *extra_attrs) — exact-match grouping.
//  4. For each group, either keep content-stream order (UseTextFlow)
//     or cluster into LINES by Y position then sort each line by X.
//  5. Within each line, walk left-to-right (or right-to-left for rtl)
//     and split into words whenever the gap exceeds XTolerance or the
//     char begins a new line within the cluster's tolerance band.
//  6. Apply ligature expansion (if Expand) when concatenating text.
func extractWordsFromChars(chars []Char, opts WordOpts) []Word {
	if len(chars) == 0 {
		return nil
	}

	// Step 1/2: filter blanks (unless KeepBlankChars) and empties.
	filtered := chars
	if !opts.KeepBlankChars {
		out := make([]Char, 0, len(chars))
		for _, c := range chars {
			if c.Text == "" {
				continue
			}
			// Retain whitespace glyphs when UseExplicitSpaces is set so
			// mergeLineIntoWords can honour them as word boundaries; drop
			// them otherwise (pure gap-based grouping).
			if isAllSpace(c.Text) && !opts.UseExplicitSpaces {
				continue
			}
			out = append(out, c)
		}
		filtered = out
	} else {
		out := make([]Char, 0, len(chars))
		for _, c := range chars {
			if c.Text == "" {
				continue
			}
			out = append(out, c)
		}
		filtered = out
	}
	if len(filtered) == 0 {
		return nil
	}

	// Step 3: group by (upright, *extra_attrs). The group key is a
	// string so it can be a map key. We keep the SAME ORDER as input
	// — within a contiguous run of equal-key chars, all chars go in
	// one group. A second equal-key run later in the slice starts a
	// new group (matching itertools.groupby semantics).
	keyOf := func(c Char) string {
		buf := make([]byte, 0, 8+len(c.FontName))
		if c.Upright {
			buf = append(buf, 'U')
		} else {
			buf = append(buf, 'u')
		}
		for _, attr := range opts.ExtraAttrs {
			buf = append(buf, '\x00')
			switch attr {
			case "fontname":
				buf = append(buf, c.FontName...)
			case "size":
				bits := math.Float64bits(c.FontSize)
				for i := 7; i >= 0; i-- {
					buf = append(buf, byte(bits>>(i*8)))
				}
			}
		}
		return string(buf)
	}
	groups := groupObjectsByAttr(filtered, keyOf)

	var words []Word
	for _, group := range groups {
		upright := group[0].Upright

		// Step 4: cluster into lines (or honour use_text_flow).
		var lines [][]Char
		var charDir string
		if opts.UseTextFlow {
			charDir = directionFor(upright, opts.HorizontalLTR, opts.VerticalTTB)
			lines = [][]Char{group}
		} else {
			lineDir := "ttb"
			if !upright {
				// For rotated text, pdfplumber flips line / char dir:
				// line_dir_rotated defaults to char_dir, char_dir_rotated
				// defaults to line_dir. So a rotated cluster's line_dir
				// becomes "ltr" (left-to-right) and char_dir becomes
				// "ttb" (top-to-bottom). The clustering then groups by
				// x rather than y.
				lineDir = "ltr"
			}
			charDir = directionFor(upright, opts.HorizontalLTR, opts.VerticalTTB)

			var keyForCluster func(c Char) float64
			var tol float64
			switch lineDir {
			case "ttb":
				// Cluster by Y1 (visual top in PDF user space).
				keyForCluster = func(c Char) float64 { return -c.Y1 }
				tol = opts.YTolerance
			case "ltr":
				keyForCluster = func(c Char) float64 { return c.X0 }
				tol = opts.XTolerance
			}
			lines = clusterObjects(group, keyForCluster, tol, false)

			// Sort within each line by char_dir.
			for i := range lines {
				sortCharsByDir(lines[i], charDir)
			}
		}

		// Step 5: walk each line and split into words.
		for _, line := range lines {
			words = append(words, mergeLineIntoWords(line, charDir, opts)...)
		}
	}

	return words
}

// directionFor picks the char direction for a glyph based on its
// upright flag and the HorizontalLTR / VerticalTTB toggles.
//
//	upright=true,  ltr=true   →  ltr
//	upright=true,  ltr=false  →  rtl
//	upright=false, ttb=true   →  ttb
//	upright=false, ttb=false  →  btt
func directionFor(upright, horizontalLTR, verticalTTB bool) string {
	if upright {
		if horizontalLTR {
			return "ltr"
		}
		return "rtl"
	}
	if verticalTTB {
		return "ttb"
	}
	return "btt"
}

// sortCharsByDir sorts chars in-place by the requested reading direction.
// For "ltr" we sort ascending by X0; for "rtl" descending by X1; for "ttb"
// ascending by Y1 (visual top first in PDF coords); for "btt" ascending by Y0.
//
// Note that PDF user space has Y growing UP, but pdfplumber's image space
// has Y growing DOWN. pdfplumber's "ttb" sort key is `(top, bottom)`
// where "top" is the smaller y in image space (visually higher). For us,
// visually higher means LARGER Y1, so "ttb" sorts by -Y1 ascending = Y1
// descending. We flip the sign in the comparison so the call sites read
// naturally.
func sortCharsByDir(chars []Char, dir string) {
	switch dir {
	case "ltr":
		sort.SliceStable(chars, func(i, j int) bool { return chars[i].X0 < chars[j].X0 })
	case "rtl":
		sort.SliceStable(chars, func(i, j int) bool { return chars[i].X1 > chars[j].X1 })
	case "ttb":
		// Visually top-most first = largest Y1 first in PDF space.
		sort.SliceStable(chars, func(i, j int) bool { return chars[i].Y1 > chars[j].Y1 })
	case "btt":
		// Visually bottom-most first = smallest Y0 first.
		sort.SliceStable(chars, func(i, j int) bool { return chars[i].Y0 < chars[j].Y0 })
	}
}

// mergeLineIntoWords walks a sorted line of chars and emits words. A
// new word starts whenever the gap to the previous char exceeds
// XTolerance (for ltr/rtl) / YTolerance (for ttb/btt), the perpendicular
// distance exceeds the cross-tolerance, or a blank/punctuation char
// triggers a split.
func mergeLineIntoWords(line []Char, dir string, opts WordOpts) []Word {
	if len(line) == 0 {
		return nil
	}

	var words []Word
	var current []Char

	flush := func() {
		if len(current) > 0 {
			words = append(words, buildWord(current, dir, opts))
			current = nil
		}
	}

	for _, c := range line {
		text := c.Text
		// Whitespace breaks the word. A space glyph only reaches this
		// point when KeepBlankChars or UseExplicitSpaces kept it; either
		// way it is an authoritative boundary. It is used as a separator
		// only — never appended to the word text.
		if isAllSpace(text) && (opts.KeepBlankChars || opts.UseExplicitSpaces) {
			flush()
			continue
		}

		// Punctuation: split before AND after, so the punctuation char
		// becomes its own one-char word.
		if opts.SplitAtPunctuation && len(text) == 1 && isPunctRune(rune(text[0])) {
			flush()
			current = []Char{c}
			flush()
			continue
		}

		if len(current) == 0 {
			current = []Char{c}
			continue
		}

		if charBeginsNewWord(current[len(current)-1], c, dir, opts) {
			flush()
			current = []Char{c}
		} else {
			current = append(current, c)
		}
	}
	flush()

	return words
}

// charBeginsNewWord is the Go port of WordExtractor.char_begins_new_word.
// Returns true if curr is far enough from prev to start a new word.
//
// pdfplumber's check has two parts:
//   - INTRALINE: gap between previous char's TRAILING edge and current
//     char's LEADING edge exceeds XTolerance. (Or the current char
//     overlaps backwards — cx < ax.)
//   - INTERLINE: chars within the same cluster but on visually
//     different lines (|cy - ay| > YTolerance).
//
// We map pdfplumber's image-space y to our user-space Y1 (visual top).
// In pdfplumber "top" decreases as you go down the page; in PDF user
// space Y1 decreases as you go down the page too (Y grows up, so the
// "top" Y1 of a lower char is smaller). So the |cy - ay| > y check
// uses Y1 directly.
func charBeginsNewWord(prev, curr Char, dir string, opts WordOpts) bool {
	var ax, bx, cx float64 // intraline (along reading direction)
	var ay, cy float64     // interline (perpendicular)
	var xTol, yTol float64

	switch dir {
	case "ltr":
		ax = prev.X0
		bx = prev.X1
		cx = curr.X0
		ay = prev.Y1 // visual top
		cy = curr.Y1
		xTol = opts.XTolerance
		yTol = opts.YTolerance
	case "rtl":
		ax = -prev.X1
		bx = -prev.X0
		cx = -curr.X1
		ay = prev.Y1
		cy = curr.Y1
		xTol = opts.XTolerance
		yTol = opts.YTolerance
	case "ttb":
		// Reading top-to-bottom: along-direction is Y (descending),
		// perpendicular is X. Intraline gap measured from prev's
		// BOTTOM (smaller Y1) to curr's TOP (larger Y1 of curr is
		// AHEAD of prev in image space → we invert by negating).
		ax = -prev.Y1
		bx = -prev.Y0
		cx = -curr.Y1
		ay = prev.X0
		cy = curr.X0
		xTol = opts.YTolerance
		yTol = opts.XTolerance
	case "btt":
		ax = prev.Y0
		bx = prev.Y1
		cx = curr.Y0
		ay = prev.X0
		cy = curr.X0
		xTol = opts.YTolerance
		yTol = opts.XTolerance
	default:
		// Unknown direction — default to ltr behaviour.
		ax = prev.X0
		bx = prev.X1
		cx = curr.X0
		ay = prev.Y1
		cy = curr.Y1
		xTol = opts.XTolerance
		yTol = opts.YTolerance
	}

	// Size-relative tolerance: the whitespace that separates words scales
	// with font size, so a fixed absolute tolerance mis-splits documents
	// that mix type sizes. When XToleranceRatio is set, derive the
	// intraline tolerance from the previous glyph's size (pdfplumber's
	// x_tolerance_ratio semantics), falling back to the absolute XTolerance
	// when the size is unknown.
	if opts.XToleranceRatio > 0 && prev.FontSize > 0 {
		xTol = opts.XToleranceRatio * prev.FontSize
	}

	intraline := cx < ax || cx > bx+xTol
	interline := math.Abs(cy-ay) > yTol
	return intraline || interline
}

// buildWord assembles a Word from the chars that should join it. Text
// is concatenated with ligature expansion if Expand=true. Bbox is the
// union of all char bboxes. FontName/FontSize/Upright are copied from
// the first char.
func buildWord(chars []Char, dir string, opts WordOpts) Word {
	var sb strings.Builder
	sb.Grow(len(chars))
	for _, c := range chars {
		if opts.Expand {
			sb.WriteString(expandLigatures(c.Text))
		} else {
			sb.WriteString(c.Text)
		}
	}
	bbox := BBoxOfChars(chars)
	w := Word{
		Text:      sb.String(),
		X0:        bbox.X0,
		Y0:        bbox.Y0,
		X1:        bbox.X1,
		Y1:        bbox.Y1,
		Upright:   chars[0].Upright,
		Direction: dir,
		FontName:  chars[0].FontName,
		FontSize:  chars[0].FontSize,
	}
	if opts.KeepChars {
		copyChars := make([]Char, len(chars))
		copy(copyChars, chars)
		w.Chars = copyChars
	}
	return w
}

// isAllSpace returns true if every rune in s is whitespace (matching
// Python's str.isspace()). An empty string returns false — same as
// Python's "".isspace() == False.
func isAllSpace(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// extractTextFromChars implements the dense (non-layout) text-extraction
// path: words → cluster into lines → join words with spaces → join lines
// with newlines.
func extractTextFromChars(chars []Char, opts TextOpts) string {
	if len(chars) == 0 {
		return ""
	}
	words := extractWordsFromChars(chars, textOptsToWordOpts(opts))
	if len(words) == 0 {
		return ""
	}

	// Cluster words into lines by visual top (Y1 in PDF coords).
	lines := clusterObjects(words, func(w Word) float64 { return -w.Y1 }, opts.YTolerance, false)

	// Within each line, sort by X0 ascending (ltr) or X1 descending (rtl).
	dir := "ltr"
	if !opts.HorizontalLTR {
		dir = "rtl"
	}
	for i := range lines {
		sortWordsByDir(lines[i], dir)
	}

	var sb strings.Builder
	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		for j, w := range line {
			if j > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(w.Text)
		}
	}
	return sb.String()
}

// sortWordsByDir is the Word equivalent of sortCharsByDir. Only
// horizontal directions are supported in the dense path (rotated text
// extraction falls back to per-word direction inside the chars).
func sortWordsByDir(words []Word, dir string) {
	switch dir {
	case "ltr":
		sort.SliceStable(words, func(i, j int) bool { return words[i].X0 < words[j].X0 })
	case "rtl":
		sort.SliceStable(words, func(i, j int) bool { return words[i].X1 > words[j].X1 })
	}
}

// extractTextWithLayout implements the layout-preserving path. It
// builds a fixed-width grid of characters where each glyph's column
// is proportional to its X0 (divided by XDensity) and each line's row
// is proportional to its Y1 (divided by YDensity).
//
// The output approximates what `pdftotext -layout` or pdfplumber's
// `extract_text(layout=True)` would produce — useful for callers that
// want to feed structured text to a downstream layout-aware consumer
// (form scrapers, LLM prompts that benefit from preserved indentation).
//
// We DON'T attempt the per-column-cell expansion that pdfplumber does
// for non-ttb / non-ltr text. The simple horizontal-ltr path is the
// common case and covers >95% of real PDFs.
func extractTextWithLayout(chars []Char, pageWidth, pageHeight float64, opts TextOpts) string {
	if len(chars) == 0 {
		return ""
	}

	words := extractWordsFromChars(chars, textOptsToWordOpts(opts))
	if len(words) == 0 {
		return ""
	}

	// Determine grid dimensions.
	widthChars := opts.LayoutWidthChars
	heightChars := opts.LayoutHeightChars
	if widthChars == 0 {
		widthChars = int(math.Round(pageWidth / opts.XDensity))
	}
	if heightChars == 0 {
		heightChars = int(math.Round(pageHeight / opts.YDensity))
	}
	if widthChars < 1 {
		widthChars = 1
	}
	if heightChars < 1 {
		heightChars = 1
	}

	// Cluster words by visual top (matching dense path) so we know
	// which words share a line.
	lines := clusterObjects(words, func(w Word) float64 { return -w.Y1 }, opts.YTolerance, false)

	// Determine the page-space y of each line's top: largest Y1 in
	// the cluster wins (so layout indentation is calibrated against
	// the visually-top edge of the line).
	type lineInfo struct {
		topY  float64
		words []Word
	}
	infos := make([]lineInfo, len(lines))
	for i, line := range lines {
		// Sort line ltr; cluster ordering means this is already mostly
		// in order but be explicit.
		dir := "ltr"
		if !opts.HorizontalLTR {
			dir = "rtl"
		}
		sortWordsByDir(line, dir)

		topY := line[0].Y1
		for _, w := range line[1:] {
			if w.Y1 > topY {
				topY = w.Y1
			}
		}
		infos[i] = lineInfo{topY: topY, words: line}
	}

	// PDF user space has Y growing UP, so "top of page" = largest Y.
	// The first line in reading order is the one with the largest
	// topY; lines lower on the page have smaller topY. cluster
	// ordering is ascending key (-Y1 ascending = Y1 descending = top-
	// to-bottom reading order), so infos is already in reading order.

	// Lay each line into a row of widthChars columns, calibrating
	// each word's column = round(X0 / XDensity).
	// Lay each line's row at row = round((pageTopY - line.topY) / YDensity).
	pageTopY := pageHeight
	rows := make([][]rune, heightChars)
	for i := range rows {
		rows[i] = make([]rune, widthChars)
		for j := range rows[i] {
			rows[i][j] = ' '
		}
	}

	for _, info := range infos {
		row := int(math.Round((pageTopY - info.topY) / opts.YDensity))
		if row < 0 {
			row = 0
		}
		if row >= heightChars {
			// Out-of-range row: extend the rows slice rather than drop
			// the text, since heightChars is heuristic.
			for r := heightChars; r <= row; r++ {
				blank := make([]rune, widthChars)
				for j := range blank {
					blank[j] = ' '
				}
				rows = append(rows, blank)
			}
			heightChars = row + 1
		}

		for _, w := range info.words {
			col := int(math.Round(w.X0 / opts.XDensity))
			if col < 0 {
				col = 0
			}
			for _, r := range w.Text {
				if col >= widthChars {
					// Extend the row to fit overflow text. We do this
					// across ALL rows to keep the grid rectangular.
					oldWidth := widthChars
					for ; widthChars <= col; widthChars++ {
					}
					if widthChars > oldWidth {
						for ri := range rows {
							ext := make([]rune, widthChars-oldWidth)
							for j := range ext {
								ext[j] = ' '
							}
							rows[ri] = append(rows[ri], ext...)
						}
					}
				}
				if col < widthChars {
					rows[row][col] = r
				}
				col++
			}
			// Insert a separator space if there's room — but only if
			// the next position isn't already non-blank.
			if col < widthChars && rows[row][col] == ' ' {
				rows[row][col] = ' '
			}
		}
	}

	// Trim trailing spaces on each row, then join.
	var sb strings.Builder
	for i, r := range rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		end := len(r)
		for end > 0 && r[end-1] == ' ' {
			end--
		}
		for j := 0; j < end; j++ {
			sb.WriteRune(r[j])
		}
	}
	return sb.String()
}

// textOptsToWordOpts converts a TextOpts into the WordOpts shape used
// by the word extractor.
func textOptsToWordOpts(t TextOpts) WordOpts {
	return WordOpts{
		XTolerance:        nonZero(t.XTolerance, 3),
		XToleranceRatio:   t.XToleranceRatio,
		YTolerance:        nonZero(t.YTolerance, 3),
		UseTextFlow:       t.UseTextFlow,
		HorizontalLTR:     t.HorizontalLTR,
		VerticalTTB:       t.VerticalTTB,
		ExtraAttrs:        t.ExtraAttrs,
		Expand:            t.Expand,
		UseExplicitSpaces: t.UseExplicitSpaces,
	}
}

func nonZero(v, dflt float64) float64 {
	if v == 0 {
		return dflt
	}
	return v
}

// Page-level entry points. We define them as methods on the page
// struct (the unexported implementation of the Page interface). The
// new methods get added to the Page interface in page.go.

// Words extracts words from the page using the supplied options.
// Float fields left at their zero value get replaced with pdfplumber-
// matching defaults (XTolerance=3, YTolerance=3). Pass
// DefaultWordOpts() for the explicit default set.
func (p *page) Words(opts WordOpts) ([]Word, error) {
	opts = applyWordOptDefaults(opts)
	chars, err := p.Chars()
	if err != nil {
		return nil, err
	}
	return extractWordsFromChars(chars, opts), nil
}

// ExtractText extracts the text of the page as a single string. See
// TextOpts for the layout / non-layout split.
func (p *page) ExtractText(opts TextOpts) (string, error) {
	opts = applyTextOptDefaults(opts)
	chars, err := p.Chars()
	if err != nil {
		return "", err
	}
	if opts.Layout {
		return extractTextWithLayout(chars, p.Width(), p.Height(), opts), nil
	}
	return extractTextFromChars(chars, opts), nil
}

// applyWordOptDefaults fills in zero-valued float fields with
// pdfplumber-matching defaults. The presence-or-absence semantics for
// bool fields are caller-defined (a Go zero-value bool is false, which
// matches pdfplumber's default for all bool kwargs).
func applyWordOptDefaults(opts WordOpts) WordOpts {
	if opts.XTolerance == 0 {
		opts.XTolerance = 3
	}
	if opts.YTolerance == 0 {
		opts.YTolerance = 3
	}
	return opts
}

// applyTextOptDefaults is the TextOpts analogue.
func applyTextOptDefaults(opts TextOpts) TextOpts {
	if opts.XTolerance == 0 {
		opts.XTolerance = 3
	}
	if opts.YTolerance == 0 {
		opts.YTolerance = 3
	}
	if opts.XDensity == 0 {
		opts.XDensity = 7.25
	}
	if opts.YDensity == 0 {
		opts.YDensity = 13
	}
	return opts
}

// ExtractTextSimple is the dead-simple text-extraction primitive that
// just clusters chars by line and joins them with single spaces / new
// lines. It ports pdfplumber's extract_text_simple — useful as a
// baseline when ExtractText's word-grouping heuristics produce
// undesired results on adversarial input.
func (p *page) ExtractTextSimple(xTolerance, yTolerance float64) (string, error) {
	if xTolerance == 0 {
		xTolerance = 3
	}
	if yTolerance == 0 {
		yTolerance = 3
	}
	chars, err := p.Chars()
	if err != nil {
		return "", err
	}
	if len(chars) == 0 {
		return "", nil
	}

	// Drop empty-text chars (failed glyph resolution) — they have no
	// printable representation.
	filtered := chars[:0:0]
	for _, c := range chars {
		if c.Text != "" {
			filtered = append(filtered, c)
		}
	}

	clustered := clusterObjects(filtered, func(c Char) float64 { return -c.Y1 }, yTolerance, false)
	var sb strings.Builder
	for i, line := range clustered {
		if i > 0 {
			sb.WriteByte('\n')
		}
		// Sort by X0 ascending and merge with " " between non-adjacent
		// runs (gap > xTolerance).
		sort.SliceStable(line, func(a, b int) bool { return line[a].X0 < line[b].X0 })
		lastX1 := math.Inf(-1)
		for _, c := range line {
			if !math.IsInf(lastX1, -1) && c.X0 > lastX1+xTolerance {
				sb.WriteByte(' ')
			}
			lastX1 = c.X1
			if c.Text == " " {
				// pdfplumber's collate_line drops spaces (they're
				// implied by the gap detector). We do the same.
				continue
			}
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}
