// Copyright (c) 2026 Halleluyah Oludele
// Licensed under the MIT License.

package pdf

import (
	"fmt"
	"unicode/utf8"
)

// Font is the interpreter's view of a single PDF font resource. Each
// font on a page (named under `/Font` in the page resources) becomes
// one of these. The interpreter resolves /Tf operators by looking up
// the font name in the page's font map and stashing the *Font on the
// text state — every subsequent text-showing op uses font.Decode to
// turn the input byte string into a sequence of (CID, Unicode, width)
// triples.
//
// A Font is constructed once per page (or once and reused across pages,
// when the same font dict is reachable from multiple pages — pdfcpu
// dereferences indirect references for us so we always get the same
// *Font pointer back).
type Font struct {
	// BaseFont is the PostScript name from the font dictionary's
	// /BaseFont entry, e.g. "Helvetica-Bold" or "ABCDEF+Times". Surfaced
	// verbatim to the caller as Char.FontName.
	BaseFont string

	// IsSimple is true for Type1 and TrueType fonts (single-byte CIDs,
	// /Encoding name + optional /Differences array). Composite fonts
	// (CIDFontType0/2) have IsSimple = false and use a Type0 cmap to
	// segment the byte stream into multi-byte CIDs.
	IsSimple bool

	// cid2unicodeEncoding is the base encoding table for simple fonts:
	// 256 entries indexed by byte value, each a Unicode string. Built
	// from the standard PDF encoding name (WinAnsi/MacRoman/Standard/
	// PDFDoc) overlaid with any /Differences entries from the font dict.
	cid2unicodeEncoding [256]string

	// ToUnicode is the optional parsed /ToUnicode CMap. When present
	// it is consulted FIRST, in front of the encoding table — the PDF
	// spec is unambiguous about this (PDF 1.7 §9.10.2). Many PDFs ship
	// a ToUnicode map even for fonts that already have a usable
	// encoding, because it's the only way to map ligature glyphs back
	// to "fi"/"ffi"/etc.
	ToUnicode *CMap

	// Widths maps CID → advance width in /1000ths of a font unit. For
	// simple fonts the keys are bytes 0..255; for composite fonts they
	// are 2-byte CIDs. DefaultWidth is used for CIDs not in the map.
	Widths       map[uint16]float64
	DefaultWidth float64

	// Standard14 is the Adobe Font Metrics width table (Unicode rune →
	// /1000ths of an em) for one of the 14 standard PDF fonts, set when
	// BaseFont names one of them (see Standard14Widths) AND the font
	// dict carried no /Widths array of its own -- which is spec-legal
	// for these 14 fonts, since viewers are expected to already know
	// their metrics. nil for every other font. CharWidth consults this
	// before falling back to the flat 500 guess.
	Standard14 map[rune]float64

	// Ascent and Descent are the font's typographic extrema in
	// /1000ths of a font unit, read from /FontDescriptor. Descent is
	// always stored negative (PDF spec) — we normalise on read.
	Ascent  float64
	Descent float64
}

// Decode walks a PDF text-showing operand (a byte string) and yields
// the sequence of CIDs it represents. For simple fonts that's just the
// bytes; for composite fonts (Identity-H is by far the most common
// composite encoding) bytes are paired into 2-byte CIDs.
//
// The returned slice is fresh — callers may retain it. Per-CID
// resolution (Unicode lookup, width) happens in DecodeUnicode and
// CharWidth, separately, so callers that only want one of those can
// avoid the cost of the other.
func (f *Font) Decode(b []byte) []uint16 {
	if f == nil {
		// No font has been selected; treat each byte as its own CID.
		// This is the same fallback pdfminer takes in non-STRICT mode,
		// and it lets us still emit chars (with empty Unicode) when a
		// content stream references a missing font dict.
		out := make([]uint16, len(b))
		for i, v := range b {
			out[i] = uint16(v)
		}
		return out
	}
	if f.IsSimple {
		out := make([]uint16, len(b))
		for i, v := range b {
			out[i] = uint16(v)
		}
		return out
	}
	// Composite (Type0) font: 2-byte CIDs, big-endian. Trailing odd
	// byte is dropped (would mean a malformed stream).
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	out := make([]uint16, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		out[i/2] = uint16(b[i])<<8 | uint16(b[i+1])
	}
	return out
}

// DecodeUnicode returns the Unicode text for a single CID. Lookup order:
//
//  1. ToUnicode CMap (if present).
//  2. Encoding table (simple fonts only).
//  3. The literal placeholder "(cid:NNN)" — same convention as
//     pdfminer.six. Layout code can detect this prefix and treat such
//     chars as "positioned but unreadable", which is still useful
//     (the bbox carries the table grid even when the text doesn't
//     come back).
func (f *Font) DecodeUnicode(cid uint16) string {
	if f == nil {
		// No font; map the CID as Latin-1 — better than nothing for
		// printable bytes, and consistent with how a viewer would
		// fall back.
		if cid < 256 {
			return string(rune(cid))
		}
		return fmt.Sprintf("(cid:%d)", cid)
	}
	if s, ok := f.ToUnicode.Lookup(cid); ok && s != "" {
		return s
	}
	if f.IsSimple && cid < 256 {
		if s := f.cid2unicodeEncoding[cid]; s != "" {
			return s
		}
		// Fall back to Latin-1 for unmapped bytes in simple fonts.
		// This is the same recovery pdfplumber-via-pdfminer applies
		// in practice — a font with WinAnsi base encoding still
		// produces sensible text for ASCII even when no /Encoding
		// was declared.
		if cid >= 0x20 && cid < 0x7f {
			return string(rune(cid))
		}
	}
	return fmt.Sprintf("(cid:%d)", cid)
}

// CharWidth returns the advance width for cid in /1000ths of the
// font's design unit. Multiply by FontSize/1000 to get the user-space
// advance (text-space units, before applying the text matrix).
func (f *Font) CharWidth(cid uint16) float64 {
	if f == nil {
		// Fall back to 500/1000 of the size — half an em, a reasonable
		// guess for unknown glyphs.
		return 500
	}
	if w, ok := f.Widths[cid]; ok {
		return w
	}
	if f.Standard14 != nil {
		if u := f.DecodeUnicode(cid); u != "" {
			if r, size := utf8.DecodeRuneInString(u); size == len(u) && r != utf8.RuneError {
				if w, ok := f.Standard14[r]; ok {
					return w
				}
			}
		}
	}
	if f.DefaultWidth != 0 {
		return f.DefaultWidth
	}
	return 500
}

// --- Predefined encodings ---------------------------------------------------
//
// The four PDF base encodings (StandardEncoding, WinAnsiEncoding,
// MacRomanEncoding, PDFDocEncoding) are built from a single source of
// truth — encodingRows — that mirrors pdfminer.six's latin_enc.py and
// PDF Reference 1.7 Appendix D.2 ("Latin Character Set and Encodings",
// pp. 925 in the 1.6 edition). Each row binds a glyph name to its
// codepoint in each of the four encodings (or -1 if absent).
//
// The named glyphs are mapped to Unicode via adobeGlyphTable, which
// covers the full set of glyphs that appear in any of the encodings
// plus the common additions that show up in PDFs' /Differences arrays
// (Polish, Czech, Vietnamese accents, mathematical symbols, etc.).
//
// Indexing the encodings at runtime is O(1) — they're materialised
// into [256]string tables in init().

// EncodingByName returns the 256-entry cid→Unicode table for a base
// encoding name. Returns the StandardEncoding (the PDF spec's default)
// if the name is unrecognised.
func EncodingByName(name string) [256]string {
	switch name {
	case "WinAnsiEncoding":
		return winAnsiEncoding
	case "MacRomanEncoding":
		return macRomanEncoding
	case "StandardEncoding":
		return standardEncoding
	case "PDFDocEncoding":
		return pdfDocEncoding
	default:
		return standardEncoding
	}
}

// ApplyDifferences overlays a /Differences array on a base encoding.
// The array is a flat sequence alternating integer-start values with
// glyph-name entries — see PDF 1.7 §9.6.5.5:
//
//	[ 39 /quotesingle 96 /grave /quoteleft ]
//
// means "glyph 39 is /quotesingle, glyph 96 is /grave, glyph 97 is
// /quoteleft". The integer resets the running CID; each subsequent
// name occupies CID++.
//
// names is a (cid, name) sequence as decoded by the caller (the
// content interpreter does the array walking). out is the table
// returned to the font.
func ApplyDifferences(base [256]string, entries []Difference) [256]string {
	return ApplyDifferencesWith(base, entries, AdobeGlyphToUnicode)
}

// ApplyDifferencesWith is ApplyDifferences with a caller-supplied glyph-name
// resolver.
//
// It exists for ZapfDingbats. Its "aNN" glyph names are font-specific and are
// deliberately unreachable from AdobeGlyphToUnicode, so resolving a
// /Differences array for that font through the global resolver would write ""
// into slots we can in fact resolve -- overwriting the correct built-in
// encoding value and leaving the glyph to decode as "(cid:N)" with no width.
// readFont passes a font-scoped resolver for the standard 14; everything else
// keeps using AdobeGlyphToUnicode.
//
// An unresolved name still clears the slot rather than leaving the base value
// in place. That is intentional: /Differences has explicitly reassigned that
// code to some other glyph, so whatever the base encoding said about it is no
// longer true, and a "(cid:N)" placeholder is the honest answer.
func ApplyDifferencesWith(base [256]string, entries []Difference, resolve func(string) string) [256]string {
	out := base
	for _, e := range entries {
		if e.CID >= 0 && e.CID < 256 {
			out[e.CID] = resolve(e.GlyphName)
		}
	}
	return out
}

// Difference is one (cid, glyph-name) pair from a /Differences array.
type Difference struct {
	CID       int
	GlyphName string
}

// AdobeGlyphToUnicode resolves Adobe glyph names (e.g. "A", "comma",
// "fi", "Adieresis", "uni0041") to Unicode strings.
//
// Lookup order:
//
//   - Exact match in adobeGlyphTable (~250 entries; the full set of
//     glyphs referenced by any of the four PDF base encodings, plus
//     common additions like fractions and arrows that appear in
//     real-world /Differences arrays).
//   - Compound names with "_" separators are split and each part is
//     resolved recursively (per AGL spec §2 — "f_i" → "fi").
//   - Variant suffixes (".alt", ".sc", ...) are stripped before lookup.
//   - "uniXXXX"/"uniXXXXXXXX" → one or more UTF-16 hex codepoints.
//   - "uXXXX".."uXXXXXX" → a single hex codepoint.
//
// Anything else returns "" — the caller falls back to a (cid:NNN)
// placeholder.
func AdobeGlyphToUnicode(name string) string {
	if name == "" {
		return ""
	}
	// AGL §2: strip variant suffix (".alt", ".sc", etc.).
	if i := indexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	// AGL §2: handle compound glyph names ("f_i", "f_f_i").
	if i := indexByte(name, '_'); i >= 0 {
		var out string
		start := 0
		for k := 0; k <= len(name); k++ {
			if k == len(name) || name[k] == '_' {
				part := AdobeGlyphToUnicode(name[start:k])
				if part == "" {
					return ""
				}
				out += part
				start = k + 1
			}
		}
		_ = i
		return out
	}
	if r, ok := adobeGlyphTable[name]; ok {
		return r
	}
	// Symbol-font glyph names ("Alpha", "universal", "club", ...). These
	// are genuine AGL entries, so they resolve the same way in any font
	// that names them -- a /Differences array is free to reference them.
	// Consulted after adobeGlyphTable so the ~45 names the two tables
	// share keep their existing values.
	//
	// ZapfDingbats' "aNN" names are deliberately NOT reachable here: they
	// are font-specific, not AGL, and are resolved only via
	// standard14GlyphToUnicode (reached from Standard14GlyphResolver).
	if r, ok := symbolGlyphTable[name]; ok {
		return r
	}
	// uniXXXX, uniXXXXXXXX ... — concatenated 4-hex codepoints.
	if len(name) > 3 && name[:3] == "uni" {
		rest := name[3:]
		if len(rest)%4 == 0 && allHex(rest) {
			var out []rune
			for i := 0; i < len(rest); i += 4 {
				v := parseHex(rest[i : i+4])
				out = append(out, rune(v))
			}
			return string(out)
		}
	}
	// uXXXX..uXXXXXXXX — single hex codepoint, 4–6 digits.
	if len(name) > 1 && name[0] == 'u' {
		rest := name[1:]
		if (len(rest) >= 4 && len(rest) <= 6) && allHex(rest) {
			return string(rune(parseHex(rest)))
		}
	}
	return ""
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func allHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func parseHex(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		v <<= 4
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v |= int(c - '0')
		case c >= 'a' && c <= 'f':
			v |= int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= int(c-'A') + 10
		}
	}
	return v
}

// adobeGlyphTable maps Adobe glyph names to their Unicode string
// equivalents. The entries are drawn from two sources:
//
//   - Every glyph referenced by the four PDF base encodings
//     (StandardEncoding, MacRomanEncoding, WinAnsiEncoding,
//     PDFDocEncoding) — required so that EncodingByName/ApplyDifferences
//     produce correct output.
//   - Common additions that appear in real-world /Differences arrays:
//     fractions, math operators, ligatures, accented Eastern European
//     letters, arrows.
//
// Greek and the wider math/symbol repertoire are NOT here — they live in
// symbolGlyphTable, which AdobeGlyphToUnicode consults immediately after
// this table.
//
// The mapping is exact-match with pdfminer.six's glyphlist.py for the
// shared entries; anything not here falls through to the uniXXXX /
// uXXXX recognisers in AdobeGlyphToUnicode.
var adobeGlyphTable = map[string]string{
	// --- ASCII letters and digits (printable identity range) --------
	"A": "A", "B": "B", "C": "C", "D": "D", "E": "E", "F": "F",
	"G": "G", "H": "H", "I": "I", "J": "J", "K": "K", "L": "L",
	"M": "M", "N": "N", "O": "O", "P": "P", "Q": "Q", "R": "R",
	"S": "S", "T": "T", "U": "U", "V": "V", "W": "W", "X": "X",
	"Y": "Y", "Z": "Z",
	"a": "a", "b": "b", "c": "c", "d": "d", "e": "e", "f": "f",
	"g": "g", "h": "h", "i": "i", "j": "j", "k": "k", "l": "l",
	"m": "m", "n": "n", "o": "o", "p": "p", "q": "q", "r": "r",
	"s": "s", "t": "t", "u": "u", "v": "v", "w": "w", "x": "x",
	"y": "y", "z": "z",
	"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4",
	"five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9",

	// --- ASCII punctuation ------------------------------------------
	"space":        " ",
	"exclam":       "!",
	"quotedbl":     "\"",
	"numbersign":   "#",
	"dollar":       "$",
	"percent":      "%",
	"ampersand":    "&",
	"quotesingle":  "'",
	"parenleft":    "(",
	"parenright":   ")",
	"asterisk":     "*",
	"plus":         "+",
	"comma":        ",",
	"hyphen":       "-",
	"period":       ".",
	"slash":        "/",
	"colon":        ":",
	"semicolon":    ";",
	"less":         "<",
	"equal":        "=",
	"greater":      ">",
	"question":     "?",
	"at":           "@",
	"bracketleft":  "[",
	"backslash":    "\\",
	"bracketright": "]",
	"asciicircum":  "^",
	"underscore":   "_",
	"grave":        "`",
	"braceleft":    "{",
	"bar":          "|",
	"braceright":   "}",
	"asciitilde":   "~",

	// --- Typographic punctuation (PDF Reference 1.7 Appendix D.2,
	// the high range of StandardEncoding) ----------------------------
	"quoteleft":      "‘", // ‘
	"quoteright":     "’", // ’
	"quotedblleft":   "“", // “
	"quotedblright":  "”", // ”
	"quotesinglbase": "‚", // ‚
	"quotedblbase":   "„", // „
	"endash":         "–", // –
	"emdash":         "—", // —
	"bullet":         "•", // •
	"dagger":         "†", // †
	"daggerdbl":      "‡", // ‡
	"ellipsis":       "…", // …
	"perthousand":    "‰", // ‰
	"guilsinglleft":  "‹", // ‹
	"guilsinglright": "›", // ›
	"fraction":       "⁄", // ⁄
	"trademark":      "™", // ™
	"minus":          "−", // −

	// --- Diacritical marks (PDF Reference 1.7 Appendix D.2) ---------
	"acute":        "´",
	"breve":        "˘",
	"caron":        "ˇ",
	"cedilla":      "¸",
	"circumflex":   "ˆ",
	"dieresis":     "¨",
	"dotaccent":    "˙",
	"hungarumlaut": "˝",
	"macron":       "¯",
	"ogonek":       "˛",
	"ring":         "˚",
	"tilde":        "˜",

	// --- Latin-1 supplement (printable, 0xA0..0xFF) -----------------
	"nbspace":        " ",
	"exclamdown":     "¡",
	"cent":           "¢",
	"sterling":       "£",
	"currency":       "¤",
	"yen":            "¥",
	"brokenbar":      "¦",
	"section":        "§",
	"copyright":      "©",
	"ordfeminine":    "ª",
	"guillemotleft":  "«",
	"logicalnot":     "¬",
	"registered":     "®",
	"degree":         "°",
	"plusminus":      "±",
	"twosuperior":    "²",
	"threesuperior":  "³",
	"mu":             "µ",
	"paragraph":      "¶",
	"periodcentered": "·",
	"onesuperior":    "¹",
	"ordmasculine":   "º",
	"guillemotright": "»",
	"onequarter":     "¼",
	"onehalf":        "½",
	"threequarters":  "¾",
	"questiondown":   "¿",

	// --- Florin and Euro (PDF base encodings' typographic glyphs) ---
	"florin": "ƒ", // ƒ
	"Euro":   "€", // €

	// --- Accented uppercase letters ---------------------------------
	"AE":          "Æ",
	"Aacute":      "Á",
	"Acircumflex": "Â",
	"Adieresis":   "Ä",
	"Agrave":      "À",
	"Aring":       "Å",
	"Atilde":      "Ã",
	"Ccedilla":    "Ç",
	"Eacute":      "É",
	"Ecircumflex": "Ê",
	"Edieresis":   "Ë",
	"Egrave":      "È",
	"Eth":         "Ð",
	"Iacute":      "Í",
	"Icircumflex": "Î",
	"Idieresis":   "Ï",
	"Igrave":      "Ì",
	"Lslash":      "Ł",
	"Ntilde":      "Ñ",
	"OE":          "Œ",
	"Oacute":      "Ó",
	"Ocircumflex": "Ô",
	"Odieresis":   "Ö",
	"Ograve":      "Ò",
	"Oslash":      "Ø",
	"Otilde":      "Õ",
	"Scaron":      "Š",
	"Thorn":       "Þ",
	"Uacute":      "Ú",
	"Ucircumflex": "Û",
	"Udieresis":   "Ü",
	"Ugrave":      "Ù",
	"Yacute":      "Ý",
	"Ydieresis":   "Ÿ",
	"Zcaron":      "Ž",

	// --- Accented lowercase letters ---------------------------------
	"ae":          "æ",
	"aacute":      "á",
	"acircumflex": "â",
	"adieresis":   "ä",
	"agrave":      "à",
	"aring":       "å",
	"atilde":      "ã",
	"ccedilla":    "ç",
	"dotlessi":    "ı",
	"eacute":      "é",
	"ecircumflex": "ê",
	"edieresis":   "ë",
	"egrave":      "è",
	"eth":         "ð",
	"germandbls":  "ß",
	"iacute":      "í",
	"icircumflex": "î",
	"idieresis":   "ï",
	"igrave":      "ì",
	"lslash":      "ł",
	"ntilde":      "ñ",
	"oe":          "œ",
	"oacute":      "ó",
	"ocircumflex": "ô",
	"odieresis":   "ö",
	"ograve":      "ò",
	"oslash":      "ø",
	"otilde":      "õ",
	"scaron":      "š",
	"thorn":       "þ",
	"uacute":      "ú",
	"ucircumflex": "û",
	"udieresis":   "ü",
	"ugrave":      "ù",
	"yacute":      "ý",
	"ydieresis":   "ÿ",
	"zcaron":      "ž",

	// --- Arithmetic symbols (Mac/WinAnsi/PDFDoc) --------------------
	"multiply": "×",
	"divide":   "÷",

	// --- Common ligatures (used in /Differences arrays) -------------
	"fi":    "ﬁ",
	"fl":    "ﬂ",
	"ff":    "ﬀ",
	"ffi":   "ﬃ",
	"ffl":   "ﬄ",
	"longs": "ſ",
}

// --- Encoding tables -------------------------------------------------------
//
// Initialised lazily in init() from encodingRows below. encodingRows
// mirrors the canonical PDF 1.7 Appendix D.2 table (also in
// pdfminer.six's pdfminer/latin_enc.py); each row binds a glyph name
// to its codepoint in each of the four base encodings (or -1 if the
// glyph is unmapped in that encoding).

var (
	standardEncoding [256]string
	winAnsiEncoding  [256]string
	macRomanEncoding [256]string
	pdfDocEncoding   [256]string
)

// encodingRow is one row of the PDF base-encoding table: a glyph name
// plus its codepoint in StandardEncoding / MacRomanEncoding /
// WinAnsiEncoding / PDFDocEncoding (each -1 if the glyph is unmapped
// in that encoding).
type encodingRow struct {
	name               string
	std, mac, win, pdf int
}

// encodingRows is the canonical PDF 1.7 Appendix D.2 table. Mirrors
// pdfminer.six's pdfminer/latin_enc.py exactly so that any glyph
// pdfplumber resolves, we also resolve.
var encodingRows = []encodingRow{
	{"A", 65, 65, 65, 65},
	{"AE", 225, 174, 198, 198},
	{"Aacute", -1, 231, 193, 193},
	{"Acircumflex", -1, 229, 194, 194},
	{"Adieresis", -1, 128, 196, 196},
	{"Agrave", -1, 203, 192, 192},
	{"Aring", -1, 129, 197, 197},
	{"Atilde", -1, 204, 195, 195},
	{"B", 66, 66, 66, 66},
	{"C", 67, 67, 67, 67},
	{"Ccedilla", -1, 130, 199, 199},
	{"D", 68, 68, 68, 68},
	{"E", 69, 69, 69, 69},
	{"Eacute", -1, 131, 201, 201},
	{"Ecircumflex", -1, 230, 202, 202},
	{"Edieresis", -1, 232, 203, 203},
	{"Egrave", -1, 233, 200, 200},
	{"Eth", -1, -1, 208, 208},
	{"Euro", -1, -1, 128, 160},
	{"F", 70, 70, 70, 70},
	{"G", 71, 71, 71, 71},
	{"H", 72, 72, 72, 72},
	{"I", 73, 73, 73, 73},
	{"Iacute", -1, 234, 205, 205},
	{"Icircumflex", -1, 235, 206, 206},
	{"Idieresis", -1, 236, 207, 207},
	{"Igrave", -1, 237, 204, 204},
	{"J", 74, 74, 74, 74},
	{"K", 75, 75, 75, 75},
	{"L", 76, 76, 76, 76},
	{"Lslash", 232, -1, -1, 149},
	{"M", 77, 77, 77, 77},
	{"N", 78, 78, 78, 78},
	{"Ntilde", -1, 132, 209, 209},
	{"O", 79, 79, 79, 79},
	{"OE", 234, 206, 140, 150},
	{"Oacute", -1, 238, 211, 211},
	{"Ocircumflex", -1, 239, 212, 212},
	{"Odieresis", -1, 133, 214, 214},
	{"Ograve", -1, 241, 210, 210},
	{"Oslash", 233, 175, 216, 216},
	{"Otilde", -1, 205, 213, 213},
	{"P", 80, 80, 80, 80},
	{"Q", 81, 81, 81, 81},
	{"R", 82, 82, 82, 82},
	{"S", 83, 83, 83, 83},
	{"Scaron", -1, -1, 138, 151},
	{"T", 84, 84, 84, 84},
	{"Thorn", -1, -1, 222, 222},
	{"U", 85, 85, 85, 85},
	{"Uacute", -1, 242, 218, 218},
	{"Ucircumflex", -1, 243, 219, 219},
	{"Udieresis", -1, 134, 220, 220},
	{"Ugrave", -1, 244, 217, 217},
	{"V", 86, 86, 86, 86},
	{"W", 87, 87, 87, 87},
	{"X", 88, 88, 88, 88},
	{"Y", 89, 89, 89, 89},
	{"Yacute", -1, -1, 221, 221},
	{"Ydieresis", -1, 217, 159, 152},
	{"Z", 90, 90, 90, 90},
	{"Zcaron", -1, -1, 142, 153},
	{"a", 97, 97, 97, 97},
	{"aacute", -1, 135, 225, 225},
	{"acircumflex", -1, 137, 226, 226},
	{"acute", 194, 171, 180, 180},
	{"adieresis", -1, 138, 228, 228},
	{"ae", 241, 190, 230, 230},
	{"agrave", -1, 136, 224, 224},
	{"ampersand", 38, 38, 38, 38},
	{"aring", -1, 140, 229, 229},
	{"asciicircum", 94, 94, 94, 94},
	{"asciitilde", 126, 126, 126, 126},
	{"asterisk", 42, 42, 42, 42},
	{"at", 64, 64, 64, 64},
	{"atilde", -1, 139, 227, 227},
	{"b", 98, 98, 98, 98},
	{"backslash", 92, 92, 92, 92},
	{"bar", 124, 124, 124, 124},
	{"braceleft", 123, 123, 123, 123},
	{"braceright", 125, 125, 125, 125},
	{"bracketleft", 91, 91, 91, 91},
	{"bracketright", 93, 93, 93, 93},
	{"breve", 198, 249, -1, 24},
	{"brokenbar", -1, -1, 166, 166},
	{"bullet", 183, 165, 149, 128},
	{"c", 99, 99, 99, 99},
	{"caron", 207, 255, -1, 25},
	{"ccedilla", -1, 141, 231, 231},
	{"cedilla", 203, 252, 184, 184},
	{"cent", 162, 162, 162, 162},
	{"circumflex", 195, 246, 136, 26},
	{"colon", 58, 58, 58, 58},
	{"comma", 44, 44, 44, 44},
	{"copyright", -1, 169, 169, 169},
	{"currency", 168, 219, 164, 164},
	{"d", 100, 100, 100, 100},
	{"dagger", 178, 160, 134, 129},
	{"daggerdbl", 179, 224, 135, 130},
	{"degree", -1, 161, 176, 176},
	{"dieresis", 200, 172, 168, 168},
	{"divide", -1, 214, 247, 247},
	{"dollar", 36, 36, 36, 36},
	{"dotaccent", 199, 250, -1, 27},
	{"dotlessi", 245, 245, -1, 154},
	{"e", 101, 101, 101, 101},
	{"eacute", -1, 142, 233, 233},
	{"ecircumflex", -1, 144, 234, 234},
	{"edieresis", -1, 145, 235, 235},
	{"egrave", -1, 143, 232, 232},
	{"eight", 56, 56, 56, 56},
	{"ellipsis", 188, 201, 133, 131},
	{"emdash", 208, 209, 151, 132},
	{"endash", 177, 208, 150, 133},
	{"equal", 61, 61, 61, 61},
	{"eth", -1, -1, 240, 240},
	{"exclam", 33, 33, 33, 33},
	{"exclamdown", 161, 193, 161, 161},
	{"f", 102, 102, 102, 102},
	{"fi", 174, 222, -1, 147},
	{"five", 53, 53, 53, 53},
	{"fl", 175, 223, -1, 148},
	{"florin", 166, 196, 131, 134},
	{"four", 52, 52, 52, 52},
	{"fraction", 164, 218, -1, 135},
	{"g", 103, 103, 103, 103},
	{"germandbls", 251, 167, 223, 223},
	{"grave", 193, 96, 96, 96},
	{"greater", 62, 62, 62, 62},
	{"guillemotleft", 171, 199, 171, 171},
	{"guillemotright", 187, 200, 187, 187},
	{"guilsinglleft", 172, 220, 139, 136},
	{"guilsinglright", 173, 221, 155, 137},
	{"h", 104, 104, 104, 104},
	{"hungarumlaut", 205, 253, -1, 28},
	{"hyphen", 45, 45, 45, 45},
	{"i", 105, 105, 105, 105},
	{"iacute", -1, 146, 237, 237},
	{"icircumflex", -1, 148, 238, 238},
	{"idieresis", -1, 149, 239, 239},
	{"igrave", -1, 147, 236, 236},
	{"j", 106, 106, 106, 106},
	{"k", 107, 107, 107, 107},
	{"l", 108, 108, 108, 108},
	{"less", 60, 60, 60, 60},
	{"logicalnot", -1, 194, 172, 172},
	{"lslash", 248, -1, -1, 155},
	{"m", 109, 109, 109, 109},
	{"macron", 197, 248, 175, 175},
	{"minus", -1, -1, -1, 138},
	{"mu", -1, 181, 181, 181},
	{"multiply", -1, -1, 215, 215},
	{"n", 110, 110, 110, 110},
	{"nbspace", -1, 202, 160, -1},
	{"nine", 57, 57, 57, 57},
	{"ntilde", -1, 150, 241, 241},
	{"numbersign", 35, 35, 35, 35},
	{"o", 111, 111, 111, 111},
	{"oacute", -1, 151, 243, 243},
	{"ocircumflex", -1, 153, 244, 244},
	{"odieresis", -1, 154, 246, 246},
	{"oe", 250, 207, 156, 156},
	{"ogonek", 206, 254, -1, 29},
	{"ograve", -1, 152, 242, 242},
	{"one", 49, 49, 49, 49},
	{"onehalf", -1, -1, 189, 189},
	{"onequarter", -1, -1, 188, 188},
	{"onesuperior", -1, -1, 185, 185},
	{"ordfeminine", 227, 187, 170, 170},
	{"ordmasculine", 235, 188, 186, 186},
	{"oslash", 249, 191, 248, 248},
	{"otilde", -1, 155, 245, 245},
	{"p", 112, 112, 112, 112},
	{"paragraph", 182, 166, 182, 182},
	{"parenleft", 40, 40, 40, 40},
	{"parenright", 41, 41, 41, 41},
	{"percent", 37, 37, 37, 37},
	{"period", 46, 46, 46, 46},
	{"periodcentered", 180, 225, 183, 183},
	{"perthousand", 189, 228, 137, 139},
	{"plus", 43, 43, 43, 43},
	{"plusminus", -1, 177, 177, 177},
	{"q", 113, 113, 113, 113},
	{"question", 63, 63, 63, 63},
	{"questiondown", 191, 192, 191, 191},
	{"quotedbl", 34, 34, 34, 34},
	{"quotedblbase", 185, 227, 132, 140},
	{"quotedblleft", 170, 210, 147, 141},
	{"quotedblright", 186, 211, 148, 142},
	{"quoteleft", 96, 212, 145, 143},
	{"quoteright", 39, 213, 146, 144},
	{"quotesinglbase", 184, 226, 130, 145},
	{"quotesingle", 169, 39, 39, 39},
	{"r", 114, 114, 114, 114},
	{"registered", -1, 168, 174, 174},
	{"ring", 202, 251, -1, 30},
	{"s", 115, 115, 115, 115},
	{"scaron", -1, -1, 154, 157},
	{"section", 167, 164, 167, 167},
	{"semicolon", 59, 59, 59, 59},
	{"seven", 55, 55, 55, 55},
	{"six", 54, 54, 54, 54},
	{"slash", 47, 47, 47, 47},
	{"space", 32, 32, 32, 32},
	{"space", -1, 202, 160, -1},
	{"space", -1, 202, 173, -1},
	{"sterling", 163, 163, 163, 163},
	{"t", 116, 116, 116, 116},
	{"thorn", -1, -1, 254, 254},
	{"three", 51, 51, 51, 51},
	{"threequarters", -1, -1, 190, 190},
	{"threesuperior", -1, -1, 179, 179},
	{"tilde", 196, 247, 152, 31},
	{"trademark", -1, 170, 153, 146},
	{"two", 50, 50, 50, 50},
	{"twosuperior", -1, -1, 178, 178},
	{"u", 117, 117, 117, 117},
	{"uacute", -1, 156, 250, 250},
	{"ucircumflex", -1, 158, 251, 251},
	{"udieresis", -1, 159, 252, 252},
	{"ugrave", -1, 157, 249, 249},
	{"underscore", 95, 95, 95, 95},
	{"v", 118, 118, 118, 118},
	{"w", 119, 119, 119, 119},
	{"x", 120, 120, 120, 120},
	{"y", 121, 121, 121, 121},
	{"yacute", -1, -1, 253, 253},
	{"ydieresis", -1, 216, 255, 255},
	{"yen", 165, 180, 165, 165},
	{"z", 122, 122, 122, 122},
	{"zcaron", -1, -1, 158, 158},
	{"zero", 48, 48, 48, 48},
}

func init() {
	for _, r := range encodingRows {
		u := AdobeGlyphToUnicode(r.name)
		if u == "" {
			// All names in encodingRows have an entry in
			// adobeGlyphTable by construction. If a future edit
			// adds a row without a glyph mapping the encoding
			// will silently lose that slot; the unit test in
			// font_test.go guards against this.
			continue
		}
		if r.std >= 0 && r.std < 256 {
			standardEncoding[r.std] = u
		}
		if r.mac >= 0 && r.mac < 256 {
			macRomanEncoding[r.mac] = u
		}
		if r.win >= 0 && r.win < 256 {
			winAnsiEncoding[r.win] = u
		}
		if r.pdf >= 0 && r.pdf < 256 {
			pdfDocEncoding[r.pdf] = u
		}
	}
}
